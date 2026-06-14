// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the public serving path's routing, manifest resolution, and
// content-addressed blob streaming. Everything is exercised through in-memory
// KV + R2 mocks (and a mock Cache API), so the suite runs without a live edge
// (no Miniflare/Wrangler) on the plain vitest node pool.

import { createHash } from "node:crypto";
import { beforeEach, describe, expect, it } from "vitest";

import {
  cleanPath,
  normalizeHost,
  parseRouteValue,
  routeKey,
  type RouteValue,
} from "../src/route";
import {
  blobKey,
  candidatePaths,
  manifestKey,
  parseManifest,
  resolveManifestEntry,
  type Manifest,
} from "../src/manifest";
import { cacheControlFor, contentTypeFor, isHashedAsset } from "../src/http";
import {
  serve,
  type BucketLike,
  type CacheLike,
  type Env,
  type R2ObjectLike,
} from "../src/index";

// --- Fixtures ---------------------------------------------------------------
// The contract validator (`@shipped/contracts`) requires UUID identifiers, so
// the fixtures use real UUIDs (the old `org_1`/`v_abc` placeholders would now
// fail closed — which is itself covered below).

const ORG_ID = "11111111-1111-1111-1111-111111111111";
const SITE_ID = "22222222-2222-2222-2222-222222222222";
const VERSION_ID = "33333333-3333-3333-3333-333333333333";

const PUBLIC_ROUTE: RouteValue = {
  org_id: ORG_ID,
  site_id: SITE_ID,
  version_id: VERSION_ID,
  access_mode: "public",
  schema_version: 1,
};

const MANIFEST_KEY = `manifests/${ORG_ID}/${SITE_ID}/${VERSION_ID}.json`;

/** Lowercase-hex SHA-256, matching how the Go API content-addresses blobs. */
function sha256(text: string): string {
  return createHash("sha256").update(text).digest("hex");
}

// --- Mocks ------------------------------------------------------------------

/** In-memory KV: route:<host> → RouteValue. */
function mockRoutes(routes: Record<string, RouteValue>) {
  return {
    async get(key: string, _type: "json"): Promise<unknown> {
      return key in routes ? routes[key] : null;
    },
  };
}

/**
 * In-memory R2 over a key→bytes map. The same store holds the manifest JSON
 * (under its `manifests/...` key) and content blobs (under `blobs/<org>/<sha>`).
 * The returned object exposes `.json()` (as the real R2ObjectBody does) so the
 * Worker reads the manifest without buffering it itself.
 */
function mockBucket(objects: Record<string, string>): BucketLike {
  return {
    async get(key: string): Promise<R2ObjectLike | null> {
      if (!(key in objects)) return null;
      const text = objects[key]!;
      return {
        body: new ReadableStream({
          start(controller) {
            controller.enqueue(new TextEncoder().encode(text));
            controller.close();
          },
        }),
        httpEtag: `"etag-${key.length}"`,
        uploaded: new Date("2026-01-01T00:00:00Z"),
        size: text.length,
        async json() {
          return JSON.parse(text);
        },
      };
    },
  };
}

/** A simple in-memory Cache API double for asserting cache reads/writes. */
function mockCache(): CacheLike & { store: Map<string, Response> } {
  const store = new Map<string, Response>();
  return {
    store,
    async match(request: Request): Promise<Response | undefined> {
      const hit = store.get(request.url);
      return hit ? hit.clone() : undefined;
    },
    async put(request: Request, response: Response): Promise<void> {
      store.set(request.url, response.clone());
    },
  };
}

/**
 * Build a manifest mapping request paths → entries, content-addressing each
 * file by the SHA-256 of its bytes, and an R2 store holding both the manifest
 * JSON and every referenced blob.
 */
function deploy(
  files: Record<string, { body: string; content_type: string }>,
): { manifest: Manifest; objects: Record<string, string> } {
  const objects: Record<string, string> = {};
  const manifestFiles: Manifest["files"] = {};
  for (const [path, { body, content_type }] of Object.entries(files)) {
    const hash = sha256(body);
    manifestFiles[path] = { sha256: hash, content_type, size: body.length };
    objects[blobKey(ORG_ID, hash)] = body;
  }
  const manifest: Manifest = { schema_version: 1, files: manifestFiles };
  objects[MANIFEST_KEY] = JSON.stringify(manifest);
  return { manifest, objects };
}

function envFor(
  route: RouteValue,
  host: string,
  objects: Record<string, string>,
): Env {
  return {
    ROUTES: mockRoutes({ [routeKey(host)]: route }),
    BUCKET: mockBucket(objects),
  };
}

function get(host: string, path: string): Request {
  return new Request(`https://${host}${path}`, { method: "GET" });
}

const HOST = "acme.shippedusercontent.com";

/** Serve with the Cache API disabled (the default for most assertions). */
function serveNoCache(req: Request, env: Env) {
  return serve(req, env, { cache: null });
}

// --- normalizeHost / routeKey ----------------------------------------------

describe("normalizeHost", () => {
  it("lowercases, strips port and trailing dot", () => {
    expect(normalizeHost("Acme.ShippedUserContent.com")).toBe(
      "acme.shippedusercontent.com",
    );
    expect(normalizeHost("acme.shippedusercontent.com:8787")).toBe(
      "acme.shippedusercontent.com",
    );
    expect(normalizeHost("acme.shippedusercontent.com.")).toBe(
      "acme.shippedusercontent.com",
    );
  });

  it("builds the route key", () => {
    expect(routeKey("Acme.shippedusercontent.com")).toBe(
      "route:acme.shippedusercontent.com",
    );
  });
});

// --- parseRouteValue (delegates to @shipped/contracts) ----------------------

describe("parseRouteValue", () => {
  it("accepts a well-formed public route", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE })).toEqual(PUBLIC_ROUTE);
  });

  it("accepts schema_version 1 and 2 (Phase 2 added optional expires_at)", () => {
    // v1: no expires_at (read as non-expiring).
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 1 })).not.toBeNull();
    // v2: optional expires_at allowed.
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 2 })).not.toBeNull();
    expect(
      parseRouteValue({
        ...PUBLIC_ROUTE,
        schema_version: 2,
        expires_at: "2030-01-01T00:00:00Z",
      }),
    ).not.toBeNull();
  });

  it("rejects an out-of-range / mistyped schema_version", () => {
    // 3 is newer than this build understands; "1" is a string, not a number.
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 3 })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 0 })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: "1" })).toBeNull();
  });

  it("rejects a malformed expires_at", () => {
    expect(
      parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 2, expires_at: "not-a-date" }),
    ).toBeNull();
  });

  it("rejects non-UUID ids, missing fields, and bad access_mode", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE, org_id: "org_1" })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, org_id: "" })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, version_id: undefined })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, access_mode: "secret" })).toBeNull();
    expect(parseRouteValue(null)).toBeNull();
    expect(parseRouteValue("nope")).toBeNull();
  });

  it("accepts each valid access mode", () => {
    for (const mode of ["public", "password", "allowlist", "org_only"] as const) {
      expect(parseRouteValue({ ...PUBLIC_ROUTE, access_mode: mode })).not.toBeNull();
    }
  });
});

// --- cleanPath (traversal safety) ------------------------------------------

describe("cleanPath", () => {
  it("strips leading slash and collapses dot segments", () => {
    expect(cleanPath("/a/./b/c")).toBe("a/b/c");
    expect(cleanPath("/")).toBe("");
    expect(cleanPath("/dir/")).toBe("dir/");
  });

  it("rejects parent traversal", () => {
    expect(cleanPath("/../etc/passwd")).toBeNull();
    expect(cleanPath("/a/../../b")).toBeNull();
  });

  it("rejects encoded traversal and NUL/backslash", () => {
    expect(cleanPath("/%2e%2e/secret")).toBeNull(); // decodes to ../secret
    expect(cleanPath("/a%00b")).toBeNull();
    expect(cleanPath("/a\\b")).toBeNull();
  });

  it("rejects malformed percent-encoding", () => {
    expect(cleanPath("/%zz")).toBeNull();
  });
});

// --- manifest keys ----------------------------------------------------------

describe("manifest + blob keys", () => {
  it("builds the per-version manifest key", () => {
    expect(manifestKey(PUBLIC_ROUTE)).toBe(MANIFEST_KEY);
  });

  it("builds a per-org content-addressed blob key", () => {
    expect(blobKey(ORG_ID, "a".repeat(64))).toBe(`blobs/${ORG_ID}/${"a".repeat(64)}`);
  });
});

// --- parseManifest ----------------------------------------------------------

describe("parseManifest", () => {
  it("accepts a well-formed manifest", () => {
    const m = parseManifest({
      schema_version: 1,
      files: { "index.html": { sha256: "a".repeat(64), content_type: "text/html" } },
    });
    expect(m).not.toBeNull();
    expect(m!.files["index.html"]!.content_type).toBe("text/html");
  });

  it("rejects an unsupported manifest schema_version", () => {
    expect(
      parseManifest({ schema_version: 2, files: {} }),
    ).toBeNull();
  });

  it("rejects a malformed entry (bad sha / missing content_type)", () => {
    expect(
      parseManifest({
        schema_version: 1,
        files: { "x": { sha256: "not-hex", content_type: "text/html" } },
      }),
    ).toBeNull();
    expect(
      parseManifest({
        schema_version: 1,
        files: { "x": { sha256: "a".repeat(64) } },
      }),
    ).toBeNull();
  });

  it("rejects non-object input", () => {
    expect(parseManifest(null)).toBeNull();
    expect(parseManifest("nope")).toBeNull();
    expect(parseManifest([])).toBeNull();
    expect(parseManifest({ schema_version: 1, files: [] })).toBeNull();
  });
});

// --- candidatePaths / resolveManifestEntry ----------------------------------

describe("candidatePaths", () => {
  it("maps root and trailing-slash dirs to index.html", () => {
    expect(candidatePaths("")).toEqual(["index.html"]);
    expect(candidatePaths("blog/")).toEqual(["blog/index.html"]);
  });

  it("serves an explicit asset path directly", () => {
    expect(candidatePaths("assets/app.css")).toEqual(["assets/app.css"]);
  });

  it("offers index.html and .html fallbacks for extension-less paths", () => {
    expect(candidatePaths("about")).toEqual([
      "about",
      "about/index.html",
      "about.html",
    ]);
  });
});

describe("resolveManifestEntry", () => {
  const manifest: Manifest = {
    schema_version: 1,
    files: {
      "index.html": { sha256: "a".repeat(64), content_type: "text/html" },
      "about/index.html": { sha256: "b".repeat(64), content_type: "text/html" },
      "assets/app.css": { sha256: "c".repeat(64), content_type: "text/css" },
    },
  };

  it("resolves a directory request to its index.html", () => {
    expect(resolveManifestEntry(manifest, "")?.path).toBe("index.html");
  });

  it("resolves a pretty path to about/index.html", () => {
    expect(resolveManifestEntry(manifest, "about")?.path).toBe("about/index.html");
  });

  it("resolves an explicit asset", () => {
    const m = resolveManifestEntry(manifest, "assets/app.css");
    expect(m?.path).toBe("assets/app.css");
    expect(m?.entry.content_type).toBe("text/css");
  });

  it("returns null when nothing matches", () => {
    expect(resolveManifestEntry(manifest, "missing")).toBeNull();
  });
});

// --- Content-Type + Cache-Control ------------------------------------------

describe("content type + cache policy", () => {
  it("derives content types by extension (fallback path)", () => {
    expect(contentTypeFor("a/index.html")).toBe("text/html; charset=utf-8");
    expect(contentTypeFor("a/app.css")).toBe("text/css; charset=utf-8");
    expect(contentTypeFor("a/blob")).toBe("application/octet-stream");
  });

  it("flags hashed assets as immutable and HTML as short-lived", () => {
    expect(isHashedAsset("assets/app.4f3a9c2b.js")).toBe(true);
    expect(isHashedAsset("index.html")).toBe(false);

    expect(cacheControlFor("assets/app.4f3a9c2b.js")).toBe(
      "public, max-age=31536000, immutable",
    );
    expect(cacheControlFor("index.html")).toBe(
      "public, max-age=60, must-revalidate",
    );
  });
});

// --- End-to-end serve() with mocks (manifest → blob) ------------------------

describe("serve() public path — manifest resolution + content-addressed blobs", () => {
  it("serves index.html at the root with html headers and the manifest type", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html; charset=utf-8" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(200);
    // Content-Type comes from the MANIFEST, not from re-sniffing bytes.
    expect(res.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    // Cache-Control is policy-derived from the SERVED path (index.html → short).
    expect(res.headers.get("Cache-Control")).toBe("public, max-age=60, must-revalidate");
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
    expect(res.headers.get("Referrer-Policy")).toBe("no-referrer");
    expect(await res.text()).toBe("<h1>home</h1>");
  });

  it("fetches the blob by its content address (blobs/<org>/<sha256>)", async () => {
    const body = "console.log(1)";
    const { objects } = deploy({
      "assets/app.4f3a9c2b.js": { body, content_type: "text/javascript; charset=utf-8" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);

    // The blob really does live at the content-addressed key.
    expect(objects[blobKey(ORG_ID, sha256(body))]).toBe(body);

    const res = await serveNoCache(get(HOST, "/assets/app.4f3a9c2b.js"), env);
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("text/javascript; charset=utf-8");
    // Hashed asset → immutable, regardless of the blob key.
    expect(res.headers.get("Cache-Control")).toBe(
      "public, max-age=31536000, immutable",
    );
    expect(await res.text()).toBe(body);
  });

  it("falls back to about/index.html for a pretty path", async () => {
    const { objects } = deploy({
      "about/index.html": { body: "<h1>about</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/about"), env);
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>about</h1>");
  });

  it("serves a trailing-slash directory's index.html", async () => {
    const { objects } = deploy({
      "blog/index.html": { body: "<h1>blog</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/blog/"), env);
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>blog</h1>");
  });

  it("serves the version's custom 404 page (from the manifest) when nothing matches", async () => {
    const { objects } = deploy({
      "404.html": { body: "<h1>custom missing</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/nope"), env);
    expect(res.status).toBe(404);
    expect(await res.text()).toBe("<h1>custom missing</h1>");
  });

  it("serves the default 404 when the manifest ships none", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/nope"), env);
    expect(res.status).toBe(404);
    expect(res.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    expect(await res.text()).toContain("404");
  });

  it("404s when the manifest is missing entirely (no projection)", async () => {
    // KV route exists, but no manifest object in R2 → fail closed.
    const env = envFor(PUBLIC_ROUTE, HOST, {});
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(404);
  });

  it("404s when the manifest references a blob that is missing (drift)", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    // Delete the referenced blob, keep the manifest → drift.
    delete objects[blobKey(ORG_ID, sha256("<h1>home</h1>"))];
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(404);
  });

  it("404s an unknown host (no route in KV)", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get("ghost.shippedusercontent.com", "/"), env);
    expect(res.status).toBe(404);
  });

  it("404s a traversal attempt rather than escaping the prefix", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/../../other/secret"), env);
    expect(res.status).toBe(404);
  });

  it("never reads a JWT — Authorization header is ignored on the public path", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const req = new Request(`https://${HOST}/`, {
      method: "GET",
      headers: { Authorization: "Bearer should-be-ignored" },
    });
    const res = await serve(req, env, { cache: null });
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>home</h1>");
  });

  it("returns an empty body for HEAD but keeps headers", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html; charset=utf-8" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const req = new Request(`https://${HOST}/`, { method: "HEAD" });
    const res = await serve(req, env, { cache: null });
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    expect(await res.text()).toBe("");
  });

  it("405s a non-GET/HEAD method", async () => {
    const env = envFor(PUBLIC_ROUTE, HOST, {});
    const req = new Request(`https://${HOST}/`, { method: "POST" });
    const res = await serve(req, env, { cache: null });
    expect(res.status).toBe(405);
    expect(res.headers.get("Allow")).toBe("GET, HEAD");
  });

  it("serves a schema_version 2 route (Phase 2 projection) like any public route", async () => {
    // The Go API now writes SCHEMA_VERSION 2; a v2 public route serves normally.
    const v2Route = { ...PUBLIC_ROUTE, schema_version: 2 } as RouteValue;
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(v2Route, HOST, objects);
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>home</h1>");
  });

  it("fails closed (404) when KV carries a schema_version newer than this build", async () => {
    // The Go API published a value this Worker does not understand (v > MAX).
    const badRoute = { ...PUBLIC_ROUTE, schema_version: 99 } as RouteValue;
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(badRoute, HOST, objects);
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(404);
  });

  it("fails closed (404) when the manifest carries an unsupported schema_version", async () => {
    const body = "<h1>home</h1>";
    const objects: Record<string, string> = {
      [MANIFEST_KEY]: JSON.stringify({
        schema_version: 2, // unsupported manifest shape
        files: { "index.html": { sha256: sha256(body), content_type: "text/html" } },
      }),
      [blobKey(ORG_ID, sha256(body))]: body,
    };
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(404);
  });
});

// --- Cache API behavior -----------------------------------------------------

describe("serve() caches public blob responses via the Cache API", () => {
  it("writes a successful response to the cache, then serves the next from it", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const cache = mockCache();

    // First request: cache miss → served from R2 → written to cache.
    const res1 = await serve(get(HOST, "/"), env, { cache });
    expect(res1.status).toBe(200);
    expect(await res1.text()).toBe("<h1>home</h1>");
    expect(cache.store.size).toBe(1);

    // Second request: now a cache HIT even if R2 is emptied.
    const emptyEnv = envFor(PUBLIC_ROUTE, HOST, {});
    const res2 = await serve(get(HOST, "/"), emptyEnv, { cache });
    expect(res2.status).toBe(200);
    expect(await res2.text()).toBe("<h1>home</h1>");
  });

  it("keys the cache by version so a pointer flip is a fresh entry", async () => {
    const v1 = deploy({
      "index.html": { body: "<h1>v1</h1>", content_type: "text/html" },
    });
    const cache = mockCache();
    await serve(get(HOST, "/"), envFor(PUBLIC_ROUTE, HOST, v1.objects), { cache });

    // Publish a new version: same host, new version_id + new bytes.
    const NEW_VERSION = "44444444-4444-4444-4444-444444444444";
    const v2Route: RouteValue = { ...PUBLIC_ROUTE, version_id: NEW_VERSION };
    const v2Body = "<h1>v2</h1>";
    const v2Objects: Record<string, string> = {
      [`manifests/${ORG_ID}/${SITE_ID}/${NEW_VERSION}.json`]: JSON.stringify({
        schema_version: 1,
        files: { "index.html": { sha256: sha256(v2Body), content_type: "text/html" } },
      }),
      [blobKey(ORG_ID, sha256(v2Body))]: v2Body,
    };
    const res = await serve(get(HOST, "/"), envFor(v2Route, HOST, v2Objects), { cache });
    expect(await res.text()).toBe("<h1>v2</h1>");
    // Both versions are distinct cache entries.
    expect(cache.store.size).toBe(2);
  });
});

// ============================================================================
// Phase 2 — gated serving (password | allowlist | org_only)
// ============================================================================
// These exercise the real edge-token verification + /authz exchange. We mint
// test tokens with a jose-generated Ed25519 key and serve its public JWK from a
// mocked JWKS endpoint — exactly mirroring the Go edge signer's claim set and the
// /.well-known/edge-jwks shape (OKP/Ed25519, kid, x).

import { SignJWT, exportJWK, generateKeyPair } from "jose";
import { isRouteExpired } from "../src/route";
import { safeNextPath, readEdgeCookie, EDGE_COOKIE_NAME } from "../src/authz";
import { __resetJwksCacheForTests, type FetchLike } from "../src/edgetoken";

const GATED_HOST = "private.shippedusercontent.com";
const EDGE_ISSUER = "https://api.shipped.app/edge";
const JWKS_URL = "https://api.test/.well-known/edge-jwks";
const AUTHZ_URL = "https://app.shipped.app/authz";

/** A test edge signer: an Ed25519 keypair + its JWKS, mirroring the Go signer. */
async function makeEdgeSigner(kid = "edge-test-kid") {
  const { publicKey, privateKey } = await generateKeyPair("EdDSA", { extractable: true });
  const pubJwk = await exportJWK(publicKey);
  // The Go signer serves OKP/Ed25519 with kid + use + alg (see internal/edgetoken).
  const jwks = {
    keys: [{ ...pubJwk, kid, use: "sig", alg: "EdDSA" }],
  };

  async function mint(opts: {
    host?: string;
    siteId?: string;
    sub?: string;
    mode?: "password" | "allowlist" | "org_only";
    iss?: string;
    alg?: string;
    expSecondsFromNow?: number;
    omitExp?: boolean;
    headerKid?: string;
  }): Promise<string> {
    const now = Math.floor(Date.now() / 1000);
    const builder = new SignJWT({
      site_id: opts.siteId ?? SITE_ID,
      mode: opts.mode ?? "org_only",
    })
      .setProtectedHeader({ alg: opts.alg ?? "EdDSA", kid: opts.headerKid ?? kid })
      .setIssuer(opts.iss ?? EDGE_ISSUER)
      .setSubject(opts.sub ?? "44444444-4444-4444-4444-444444444444")
      .setAudience(opts.host ?? GATED_HOST)
      .setIssuedAt(now)
      .setJti("test-jti");
    if (!opts.omitExp) {
      builder.setExpirationTime(now + (opts.expSecondsFromNow ?? 900));
    }
    return builder.sign(privateKey);
  }

  return { kid, jwks, mint };
}

/** A fetch double that serves a fixed JWKS document at JWKS_URL (else 404). */
function mockJwksFetch(jwks: unknown): FetchLike & { calls: number } {
  let calls = 0;
  const fn = (async (input: string) => {
    if (input === JWKS_URL) {
      calls++;
      return { ok: true, status: 200, json: async () => jwks };
    }
    return { ok: false, status: 404, json: async () => ({}) };
  }) as FetchLike & { calls: number };
  Object.defineProperty(fn, "calls", { get: () => calls });
  return fn;
}

function gatedEnv(
  route: RouteValue,
  host: string,
  objects: Record<string, string>,
): Env {
  return {
    ...envFor(route, host, objects),
    EDGE_JWKS_URL: JWKS_URL,
    APP_AUTHZ_URL: AUTHZ_URL,
  };
}

/** A GET with a __Host-edge cookie carrying `token`. */
function getWithCookie(host: string, path: string, token: string): Request {
  return new Request(`https://${host}${path}`, {
    method: "GET",
    headers: { Cookie: `${EDGE_COOKIE_NAME}=${token}` },
  });
}

beforeEach(() => __resetJwksCacheForTests());

// --- route expiry helper (contract re-export) -------------------------------

describe("isRouteExpired (edge link-expiry, v2 RouteValue)", () => {
  it("never expires a route without expires_at (v1 or non-expiring v2)", () => {
    expect(isRouteExpired(PUBLIC_ROUTE)).toBe(false);
  });

  it("expires once now >= expires_at", () => {
    const past = { ...PUBLIC_ROUTE, schema_version: 2, expires_at: "2020-01-01T00:00:00Z" };
    const future = { ...PUBLIC_ROUTE, schema_version: 2, expires_at: "2999-01-01T00:00:00Z" };
    expect(isRouteExpired(past as RouteValue)).toBe(true);
    expect(isRouteExpired(future as RouteValue)).toBe(false);
  });
});

// --- safeNextPath (open-redirect defense) -----------------------------------

describe("safeNextPath rejects off-host / open-redirect targets", () => {
  it("keeps a safe same-host path (+query)", () => {
    expect(safeNextPath("/docs/intro")).toBe("/docs/intro");
    expect(safeNextPath("/a?b=1&c=2")).toBe("/a?b=1&c=2");
    expect(safeNextPath("/")).toBe("/");
  });

  it("collapses absolute, protocol-relative, backslash, and scheme targets to /", () => {
    expect(safeNextPath("https://evil.com")).toBe("/");
    expect(safeNextPath("//evil.com")).toBe("/");
    expect(safeNextPath("/\\evil.com")).toBe("/");
    expect(safeNextPath("\\\\evil.com")).toBe("/");
    expect(safeNextPath("javascript:alert(1)")).toBe("/");
    expect(safeNextPath("")).toBe("/");
    expect(safeNextPath(null)).toBe("/");
  });

  it("rejects encoded protocol-relative + control chars (CRLF header split)", () => {
    expect(safeNextPath("/%2f%2fevil.com")).toBe("/"); // decodes to //evil.com
    expect(safeNextPath("/a%0d%0aSet-Cookie:x")).toBe("/"); // CRLF injection
  });
});

// --- the public Cache API and __Host-edge cookie ----------------------------

describe("readEdgeCookie", () => {
  it("extracts the __Host-edge value among other cookies", () => {
    const req = new Request("https://h/", {
      headers: { Cookie: `a=1; ${EDGE_COOKIE_NAME}=tok.en.value; b=2` },
    });
    expect(readEdgeCookie(req)).toBe("tok.en.value");
  });
  it("returns null when absent or empty", () => {
    expect(readEdgeCookie(new Request("https://h/"))).toBeNull();
    expect(
      readEdgeCookie(new Request("https://h/", { headers: { Cookie: `${EDGE_COOKIE_NAME}=` } })),
    ).toBeNull();
  });
});

// --- gated dispatch: valid token serves; absent/invalid → 302 ---------------

describe("serve() gated path — edge-token verification", () => {
  function gatedDeploy() {
    return deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html; charset=utf-8" },
    });
  }

  for (const mode of ["password", "allowlist", "org_only"] as const) {
    it(`serves protected content for a VALID ${mode} token (private, never shared-cached)`, async () => {
      const signer = await makeEdgeSigner();
      const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: mode };
      const { objects } = gatedDeploy();
      const env = gatedEnv(route, GATED_HOST, objects);
      const token = await signer.mint({ mode, host: GATED_HOST, siteId: SITE_ID });
      const cache = mockCache();

      const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
        cache,
        fetchImpl: mockJwksFetch(signer.jwks),
      });

      expect(res.status).toBe(200);
      expect(await res.text()).toBe("<h1>secret</h1>");
      // Private + no-store; never written to the shared public cache (§10).
      expect(res.headers.get("Cache-Control")).toBe(
        "private, no-store, max-age=0, must-revalidate",
      );
      expect(res.headers.get("Vary")).toBe("Cookie");
      expect(cache.store.size).toBe(0);
    });
  }

  it("302s to /authz when the edge cookie is ABSENT (carrying host + safe next)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);

    const res = await serve(get(GATED_HOST, "/docs/x?y=1"), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });

    expect(res.status).toBe(302);
    const loc = new URL(res.headers.get("Location")!);
    expect(loc.origin + loc.pathname).toBe(AUTHZ_URL);
    expect(loc.searchParams.get("host")).toBe(GATED_HOST);
    expect(loc.searchParams.get("next")).toBe("/docs/x?y=1");
    expect(res.headers.get("Cache-Control")).toContain("no-store");
  });

  it("302s to /authz when the token has the WRONG aud (host mismatch / replay)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);
    // Minted for a DIFFERENT host → must not be accepted here.
    const token = await signer.mint({ host: "other.shippedusercontent.com", siteId: SITE_ID });

    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("302s when the token's site_id does NOT match the route's site", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({
      host: GATED_HOST,
      siteId: "99999999-9999-9999-9999-999999999999", // a different site
    });
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("302s when the token is EXPIRED", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({
      host: GATED_HOST,
      siteId: SITE_ID,
      expSecondsFromNow: -60, // already expired
    });
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("302s when the token uses a NON-EdDSA alg / unsigned (algorithm confusion)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);

    // An `alg: none` token (unsecured JWS) — must be rejected outright.
    const noneToken =
      btoa(JSON.stringify({ alg: "none", kid: signer.kid })).replace(/=+$/, "") +
      "." +
      btoa(
        JSON.stringify({
          iss: EDGE_ISSUER,
          aud: GATED_HOST,
          sub: "u",
          exp: Math.floor(Date.now() / 1000) + 900,
          site_id: SITE_ID,
          mode: "org_only",
        }),
      ).replace(/=+$/, "") +
      ".";

    const res = await serve(getWithCookie(GATED_HOST, "/", noneToken), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("302s on an HS256 token forged with the public key as the HMAC secret (alg confusion)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);

    // Classic attack: sign HS256 using the raw Ed25519 public key bytes as the
    // shared secret. The Worker pins alg=EdDSA, so this must be rejected. We sign
    // with jose's own HMAC so the MAC is otherwise valid for the forged secret.
    const { SignJWT: HsSign } = await import("jose");
    const pubX = signer.jwks.keys[0]!.x as string;
    const secret = new Uint8Array(
      atob(pubX.replace(/-/g, "+").replace(/_/g, "/"))
        .split("")
        .map((c) => c.charCodeAt(0)),
    );
    const now = Math.floor(Date.now() / 1000);
    const forged = await new HsSign({ site_id: SITE_ID, mode: "org_only" })
      .setProtectedHeader({ alg: "HS256", kid: signer.kid })
      .setIssuer(EDGE_ISSUER)
      .setSubject("u")
      .setAudience(GATED_HOST)
      .setIssuedAt(now)
      .setExpirationTime(now + 900)
      .sign(secret);

    const res = await serve(getWithCookie(GATED_HOST, "/", forged), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("302s when the token's iss is not the edge signer", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID, iss: "https://evil/" });
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("302s when the token's kid is unknown (signed by a different key)", async () => {
    const signer = await makeEdgeSigner("edge-real-kid");
    const other = await makeEdgeSigner("edge-other-kid");
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);
    // Token signed by `other`, but only `signer`'s key is in the served JWKS.
    const token = await other.mint({ host: GATED_HOST, siteId: SITE_ID });
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("never writes a gated body to the public Cache API even on a valid token", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "password" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({ mode: "password", host: GATED_HOST, siteId: SITE_ID, sub: "anon:abc" });
    const cache = mockCache();
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(200);
    expect(cache.store.size).toBe(0);
  });

  it("honors the injected clock — a token valid now is rejected once `now` is past its exp", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID, expSecondsFromNow: 900 });

    // Valid at the real time…
    const ok = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(ok.status).toBe(200);

    // …but rejected when the Worker's clock is advanced past exp (+16m).
    const later = new Date(Date.now() + 16 * 60_000);
    const expired = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
      now: later,
    });
    expect(expired.status).toBe(302);
  });
});

// --- /__authz/callback: set cookie + safe redirect --------------------------

describe("serve() /__authz/callback — cookie + safe redirect", () => {
  it("verifies the token, sets __Host-edge, and 302s to the safe next path", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID });

    const res = await serve(
      get(GATED_HOST, `/__authz/callback?token=${encodeURIComponent(token)}&next=${encodeURIComponent("/docs/x")}`),
      env,
      { cache: null, fetchImpl: mockJwksFetch(signer.jwks) },
    );

    expect(res.status).toBe(302);
    expect(res.headers.get("Location")).toBe(`https://${GATED_HOST}/docs/x`);
    const setCookie = res.headers.get("Set-Cookie")!;
    expect(setCookie).toContain(`${EDGE_COOKIE_NAME}=${token}`);
    expect(setCookie).toContain("Path=/");
    expect(setCookie).toContain("Secure");
    expect(setCookie).toContain("HttpOnly");
    expect(setCookie).toContain("SameSite=Lax");
    expect(setCookie).not.toContain("Domain="); // __Host- => host-only
    expect(res.headers.get("Cache-Control")).toContain("no-store");
  });

  it("rejects an OFF-HOST next at the callback (no open redirect) — collapses to /", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID });

    const res = await serve(
      get(
        GATED_HOST,
        `/__authz/callback?token=${encodeURIComponent(token)}&next=${encodeURIComponent("https://evil.com/x")}`,
      ),
      env,
      { cache: null, fetchImpl: mockJwksFetch(signer.jwks) },
    );

    expect(res.status).toBe(302);
    // Must stay on the content host, collapsed to "/".
    expect(res.headers.get("Location")).toBe(`https://${GATED_HOST}/`);
    expect(res.headers.get("Set-Cookie")).toContain(EDGE_COOKIE_NAME);
  });

  it("does NOT set a cookie and 302s back to /authz when the callback token is invalid", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const env = gatedEnv(route, GATED_HOST, objects);
    // Token for the wrong host → invalid at this callback.
    const token = await signer.mint({ host: "other.shippedusercontent.com", siteId: SITE_ID });

    const res = await serve(
      get(GATED_HOST, `/__authz/callback?token=${encodeURIComponent(token)}&next=/x`),
      env,
      { cache: null, fetchImpl: mockJwksFetch(signer.jwks) },
    );

    expect(res.status).toBe(302);
    expect(res.headers.get("Set-Cookie")).toBeNull();
    const loc = new URL(res.headers.get("Location")!);
    expect(loc.origin + loc.pathname).toBe(AUTHZ_URL);
  });

  it("the callback path is gated-only — a public route serves it as ordinary content (404)", async () => {
    // On a PUBLIC route, /__authz/callback is just a path; it has no manifest
    // entry, so it 404s (the callback handler only runs for gated modes).
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/__authz/callback?token=x"), env);
    expect(res.status).toBe(404);
  });
});

// --- edge link-expiry at the public path ------------------------------------

describe("serve() public path — edge link expiry (v2 expires_at)", () => {
  it("serves a non-expired link normally", async () => {
    const route = {
      ...PUBLIC_ROUTE,
      schema_version: 2,
      expires_at: "2999-01-01T00:00:00Z",
    } as RouteValue;
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const res = await serveNoCache(get(HOST, "/"), gatedEnv(route, HOST, objects));
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>home</h1>");
  });

  it("serves the platform 'link expired' page (410) once past expires_at — and never the content", async () => {
    const route = {
      ...PUBLIC_ROUTE,
      schema_version: 2,
      expires_at: "2020-01-01T00:00:00Z",
    } as RouteValue;
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const cache = mockCache();
    const res = await serve(get(HOST, "/"), gatedEnv(route, HOST, objects), { cache });
    expect(res.status).toBe(410);
    const body = await res.text();
    expect(body).toContain("expired");
    expect(body).not.toContain("secret");
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    // The expired page is never written to the shared public cache.
    expect(cache.store.size).toBe(0);
  });

  it("enforces expiry on gated routes too (refused before any token check)", async () => {
    const route = {
      ...PUBLIC_ROUTE,
      access_mode: "org_only",
      schema_version: 2,
      expires_at: "2020-01-01T00:00:00Z",
    } as RouteValue;
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const res = await serveNoCache(get(GATED_HOST, "/"), gatedEnv(route, GATED_HOST, objects));
    expect(res.status).toBe(410);
  });
});

// --- never reads the operator JWT on the gated path -------------------------

describe("serve() gated path stays operator-JWT-free", () => {
  it("ignores an Authorization header; only the __Host-edge cookie counts", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const env = gatedEnv(route, GATED_HOST, objects);
    // A bearer token is present but NO edge cookie → still 302 (the operator JWT
    // is never consulted on the content host).
    const req = new Request(`https://${GATED_HOST}/`, {
      method: "GET",
      headers: { Authorization: "Bearer operator-jwt-should-be-ignored" },
    });
    const res = await serve(req, env, { cache: null, fetchImpl: mockJwksFetch(signer.jwks) });
    expect(res.status).toBe(302);
  });
});
