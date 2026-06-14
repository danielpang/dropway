// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the public serving path's routing, manifest resolution, and
// content-addressed blob streaming. Everything is exercised through in-memory
// KV + R2 mocks (and a mock Cache API), so the suite runs without a live edge
// (no Miniflare/Wrangler) on the plain vitest node pool.

import { createHash } from "node:crypto";
import { describe, expect, it } from "vitest";

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

  it("rejects an unsupported / mistyped schema_version", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 2 })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: "1" })).toBeNull();
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

  it("fails closed (404) when KV carries an unsupported schema_version", async () => {
    // The Go API published a value this Worker does not understand.
    const badRoute = { ...PUBLIC_ROUTE, schema_version: 2 } as RouteValue;
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

// --- Phase-2 gated stubs ----------------------------------------------------

describe("serve() gated modes are Phase-2 stubs (never serve content)", () => {
  const host = "private.shippedusercontent.com";

  for (const mode of ["password", "allowlist", "org_only"] as const) {
    it(`returns a 501 Phase-2 stub for access_mode=${mode} and never streams the blob`, async () => {
      const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: mode };
      // A full, valid deploy exists — proving the 501 is policy, not a miss.
      const { objects } = deploy({
        "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
      });
      const env = envFor(route, host, objects);
      const res = await serve(get(host, "/"), env, { cache: null });
      expect(res.status).toBe(501);
      expect(res.headers.get("X-Shipped-Phase")).toBe("2");
      expect(res.headers.get("X-Shipped-Access-Mode")).toBe(mode);
      // Gated responses must never land in the shared public cache (§10).
      expect(res.headers.get("Cache-Control")).toBe("private, no-store");
      const body = await res.text();
      expect(body).toContain("/authz exchange");
      // It must NOT have streamed the protected object.
      expect(body).not.toContain("secret");
    });
  }

  it("does not write a gated response to the Cache API", async () => {
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const cache = mockCache();
    const res = await serve(get(host, "/"), envFor(route, host, objects), { cache });
    expect(res.status).toBe(501);
    expect(cache.store.size).toBe(0);
  });
});
