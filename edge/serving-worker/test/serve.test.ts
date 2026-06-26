// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the public serving path's routing, manifest resolution, and
// content-addressed blob streaming. Everything is exercised through in-memory
// KV + R2 mocks (and a mock Cache API), so the suite runs without a live edge
// (no Miniflare/Wrangler) on the plain vitest node pool.

import { createHash } from "node:crypto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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
// The contract validator (`@dropway/contracts`) requires UUID identifiers, so
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

/**
 * In-memory KV: route:<host> → RouteValue, AND the `revoked:*` / `org_status:*`
 * denylist+status string keys (Phase 4). Mirrors Cloudflare KV's overloaded
 * `get`: `get(key, "json")` returns the parsed route object; `get(key)` returns
 * the raw string (or null). Tests seed string keys via the second arg.
 */
function mockRoutes(
  routes: Record<string, RouteValue>,
  strings: Record<string, string> = {},
) {
  function get(key: string, type: "json"): Promise<unknown>;
  function get(key: string): Promise<string | null>;
  async function get(key: string, type?: "json"): Promise<unknown> {
    if (type === "json") {
      return key in routes ? routes[key] : null;
    }
    return key in strings ? strings[key]! : null;
  }
  return { get };
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

const HOST = "acme.dropwaycontent.com";

/** Serve with the Cache API disabled (the default for most assertions). */
function serveNoCache(req: Request, env: Env) {
  return serve(req, env, { cache: null });
}

// --- normalizeHost / routeKey ----------------------------------------------

describe("normalizeHost", () => {
  it("lowercases, strips port and trailing dot", () => {
    expect(normalizeHost("Acme.DropwayContent.com")).toBe(
      "acme.dropwaycontent.com",
    );
    expect(normalizeHost("acme.dropwaycontent.com:8787")).toBe(
      "acme.dropwaycontent.com",
    );
    expect(normalizeHost("acme.dropwaycontent.com.")).toBe(
      "acme.dropwaycontent.com",
    );
  });

  it("builds the route key", () => {
    expect(routeKey("Acme.dropwaycontent.com")).toBe(
      "route:acme.dropwaycontent.com",
    );
  });
});

// --- parseRouteValue (delegates to @dropway/contracts) ----------------------

describe("parseRouteValue", () => {
  it("accepts a well-formed public route", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE })).toEqual(PUBLIC_ROUTE);
  });

  it("accepts schema_version 1, 2 and 3 (v2 added expires_at, v3 added plan_tier)", () => {
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
    // v3: optional plan_tier allowed.
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 3 })).not.toBeNull();
    expect(
      parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 3, plan_tier: "free" }),
    ).toEqual({ ...PUBLIC_ROUTE, schema_version: 3, plan_tier: "free" });
  });

  it("rejects an out-of-range / mistyped schema_version", () => {
    // 4 is newer than this build understands; "1" is a string, not a number.
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 4 })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 0 })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: "1" })).toBeNull();
  });

  it("rejects a non-string plan_tier", () => {
    expect(
      parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 3, plan_tier: 7 }),
    ).toBeNull();
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

  it("lists the directory (autoindex) for an upload with no index.html", async () => {
    const { objects } = deploy({
      "notes.md": { body: "# notes", content_type: "text/markdown" },
      "readme.txt": { body: "hello", content_type: "text/plain" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    // Short-TTL HTML cache policy (served path is treated as index.html).
    expect(res.headers.get("Cache-Control")).toBe(
      "public, max-age=60, must-revalidate",
    );
    const body = await res.text();
    expect(body).toContain("Index of /");
    expect(body).toContain('href="/notes.md"');
    expect(body).toContain('href="/readme.txt"');
  });

  it("lists a subdirectory that has no index.html", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
      "docs/a.md": { body: "a", content_type: "text/markdown" },
      "docs/b.md": { body: "b", content_type: "text/markdown" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/docs/"), env);
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain("Index of /docs/");
    expect(body).toContain('href="/docs/a.md"');
    expect(body).toContain('href="/docs/b.md"');
    expect(body).toContain("Parent directory");
  });

  it("lists a directory for a pretty path with no trailing slash", async () => {
    const { objects } = deploy({
      "docs/a.md": { body: "a", content_type: "text/markdown" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/docs"), env);
    expect(res.status).toBe(200);
    // Absolute links resolve correctly even without the trailing slash.
    expect(await res.text()).toContain('href="/docs/a.md"');
  });

  it("serves index.html in preference to a listing when one exists", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
      "notes.md": { body: "# notes", content_type: "text/markdown" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>home</h1>");
  });

  it("still 404s a directory request with no matching descendants", async () => {
    const { objects } = deploy({
      "notes.md": { body: "# notes", content_type: "text/markdown" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/missing/"), env);
    expect(res.status).toBe(404);
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
    const res = await serveNoCache(get("ghost.dropwaycontent.com", "/"), env);
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

// --- edge rate limiting: native binding preferred (H9) ----------------------

describe("edge rate limiting — native Rate Limiting binding (H9)", () => {
  it("429s when the native RATE_LIMITER denies (it actually counts a flood)", async () => {
    const { objects } = deploy({
      "index.html": { body: "x", content_type: "text/html; charset=utf-8" },
    });
    const env: Env = {
      ...envFor(PUBLIC_ROUTE, HOST, objects),
      RATE_LIMITER: { limit: async () => ({ success: false }) },
    };
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(429);
    expect(res.headers.get("Retry-After")).toBeTruthy();
  });

  it("serves normally when the native RATE_LIMITER allows", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>ok</h1>", content_type: "text/html; charset=utf-8" },
    });
    const env: Env = {
      ...envFor(PUBLIC_ROUTE, HOST, objects),
      RATE_LIMITER: { limit: async () => ({ success: true }) },
    };
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(200);
  });
});

// --- service-worker registration block (M3) ---------------------------------

describe("service-worker registration is blocked under any path (M3)", () => {
  it("404s a `Service-Worker: script` fetch regardless of the path name", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html; charset=utf-8" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    // Non-conventional SW name the filename list would MISS, but the request header
    // (sent by the browser on every SW-script fetch) is caught.
    const swReq = new Request(`https://${HOST}/assets/app-worker.js`, {
      method: "GET",
      headers: { "Service-Worker": "script" },
    });
    const res = await serveNoCache(swReq, env);
    expect(res.status).toBe(404);
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

const GATED_HOST = "private.dropwaycontent.com";
const EDGE_ISSUER = "https://api.dropway.dev/edge";
const JWKS_URL = "https://api.test/.well-known/edge-jwks";
const AUTHZ_URL = "https://app.dropway.dev/authz";

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
      // Private + no-store; never written to the shared public cache.
      expect(res.headers.get("Cache-Control")).toBe(
        "private, no-store, max-age=0, must-revalidate",
      );
      expect(res.headers.get("Vary")).toBe("Cookie");
      expect(cache.store.size).toBe(0);
    });
  }

  it("records a site_visit when serving gated (org_only) HTML to an authed viewer", async () => {
    // Regression: gated sites took servePublicBody, which did NOT scheduleVisit, so
    // an org whose sites are all org_only saw ZERO site_visit events even for
    // logged-in page views. A served gated HTML page must now emit site_visit.
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const scheduled: Promise<unknown>[] = [];

    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env: Env = { ...gatedEnv(route, GATED_HOST, objects), POSTHOG_KEY: "phc_test" };
    const token = await signer.mint({ mode: "org_only", host: GATED_HOST, siteId: SITE_ID });

    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      // The JWKS fetch and the analytics POST both go through this stub; the JWKS
      // call is matched by URL, everything else resolves to the 200 above.
      fetchImpl: mockJwksFetch(signer.jwks),
      waitUntil: (p) => scheduled.push(p),
    });

    expect(res.status).toBe(200);
    expect(scheduled.length).toBeGreaterThanOrEqual(1);
    await Promise.all(scheduled);

    const visit = fetchMock.mock.calls.find(([, init]) => {
      try {
        return JSON.parse((init as { body: string }).body).event === "site_visit";
      } catch {
        return false;
      }
    });
    expect(visit, "expected a site_visit capture POST").toBeTruthy();
    const body = JSON.parse((visit![1] as { body: string }).body);
    expect(body.properties.site_id).toBe(SITE_ID);
    expect(body.properties.access_mode).toBe("org_only");
  });

  afterEach(() => vi.unstubAllGlobals());

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
    const token = await signer.mint({ host: "other.dropwaycontent.com", siteId: SITE_ID });

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

  it("302s when the token's MODE does not match the route's access_mode (H1)", async () => {
    const signer = await makeEdgeSigner();
    // Route is org_only now, but the held cookie was minted for `password` (e.g. the
    // site was switched password→org_only without republishing). The token must NOT
    // be accepted to serve org_only content.
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = gatedDeploy();
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID, mode: "password" });

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
    const token = await signer.mint({ host: "other.dropwaycontent.com", siteId: SITE_ID });

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

// ============================================================================
// Phase 4 — security/ops hardening (edge)
// ============================================================================
// Content-security headers on every response, service-worker registration block,
// edge rate limiting + denial-of-wallet (429 + org suspension), and hard
// revocation via the KV denylist (min_iat). All driven by the same mocked
// KV/R2/clock the rest of the suite uses — no live edge.

import {
  CONTENT_CSP,
  PLATFORM_CSP,
  contentSecurityHeaders,
  isServiceWorkerScript,
  platformSecurityHeaders,
} from "../src/security";
import {
  DEFAULT_RATE_LIMIT,
  type CounterKVLike,
  isBlockingStatus,
  rateLimitDecision,
  rateLimitIdentity,
  readOrgStatus,
  windowKey,
} from "../src/ratelimit";
import {
  denylistKeys,
  isRevoked,
  parseRevokedEntry,
  type RevokedKVLike,
} from "../src/revoke";

/** In-memory counter KV (get/put with TTL) for the rate limiter + org-status. */
function mockCounterKV(seed: Record<string, string> = {}): CounterKVLike & {
  store: Map<string, string>;
} {
  const store = new Map<string, string>(Object.entries(seed));
  return {
    store,
    async get(key: string): Promise<string | null> {
      return store.has(key) ? store.get(key)! : null;
    },
    async put(key: string, value: string): Promise<void> {
      store.set(key, value);
    },
  };
}

/** A read-only denylist KV over a seeded string map. */
function mockRevokedKV(seed: Record<string, string> = {}): RevokedKVLike {
  const store = new Map<string, string>(Object.entries(seed));
  return {
    async get(key: string): Promise<string | null> {
      return store.has(key) ? store.get(key)! : null;
    },
  };
}

// --- security headers --------------------------------------------------------

describe("security headers (content vs platform)", () => {
  it("content headers carry the permissive-but-safe CSP + COOP/CORP + SW posture", () => {
    const h = contentSecurityHeaders();
    expect(h["Content-Security-Policy"]).toBe(CONTENT_CSP);
    // A static site's own inline script/style must be allowed (CSP is not the
    // isolation control — domain separation is).
    expect(CONTENT_CSP).toContain("script-src 'self' 'unsafe-inline'");
    expect(CONTENT_CSP).toContain("frame-ancestors 'none'");
    expect(h["X-Content-Type-Options"]).toBe("nosniff");
    expect(h["Referrer-Policy"]).toBe("no-referrer");
    expect(h["Cross-Origin-Opener-Policy"]).toBe("same-origin");
    expect(h["Cross-Origin-Resource-Policy"]).toBe("same-site");
    expect(h["X-Frame-Options"]).toBe("DENY");
  });

  it("platform headers carry a STRICT self-only CSP (no scripts)", () => {
    const h = platformSecurityHeaders();
    expect(h["Content-Security-Policy"]).toBe(PLATFORM_CSP);
    expect(PLATFORM_CSP).toContain("default-src 'none'");
    expect(PLATFORM_CSP).not.toContain("script-src");
    expect(h["X-Content-Type-Options"]).toBe("nosniff");
    expect(h["Referrer-Policy"]).toBe("no-referrer");
  });
});

describe("security headers are present on EVERY response", () => {
  function assertHardened(res: Response, kind: "content" | "platform") {
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
    expect(res.headers.get("Referrer-Policy")).toBe("no-referrer");
    expect(res.headers.get("Cross-Origin-Opener-Policy")).toBe("same-origin");
    expect(res.headers.get("Cross-Origin-Resource-Policy")).toBe("same-site");
    expect(res.headers.get("X-Frame-Options")).toBe("DENY");
    const csp = res.headers.get("Content-Security-Policy");
    expect(csp).toBe(kind === "content" ? CONTENT_CSP : PLATFORM_CSP);
  }

  it("PUBLIC content response is hardened (tenant CSP)", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html; charset=utf-8" },
    });
    const res = await serveNoCache(get(HOST, "/"), envFor(PUBLIC_ROUTE, HOST, objects));
    expect(res.status).toBe(200);
    assertHardened(res, "content");
  });

  it("GATED content response is hardened (tenant CSP) AND private", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html; charset=utf-8" },
    });
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID });
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(200);
    assertHardened(res, "content");
    expect(res.headers.get("Cache-Control")).toContain("no-store");
  });

  it("PLATFORM 404 / 410 / 429 / 503 pages get the STRICT platform CSP", async () => {
    // Default 404 (no manifest match, no custom page) → platform.
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const notFoundRes = await serveNoCache(get(HOST, "/missing"), envFor(PUBLIC_ROUTE, HOST, objects));
    expect(notFoundRes.status).toBe(404);
    assertHardened(notFoundRes, "platform");

    // 410 link-expired.
    const expiredRoute = {
      ...PUBLIC_ROUTE,
      schema_version: 2,
      expires_at: "2020-01-01T00:00:00Z",
    } as RouteValue;
    const expiredRes = await serveNoCache(get(HOST, "/"), gatedEnv(expiredRoute, HOST, objects));
    expect(expiredRes.status).toBe(410);
    assertHardened(expiredRes, "platform");
  });

  it("405 method-not-allowed carries content security headers too", async () => {
    const env = envFor(PUBLIC_ROUTE, HOST, {});
    const res = await serve(new Request(`https://${HOST}/`, { method: "POST" }), env, {
      cache: null,
    });
    expect(res.status).toBe(405);
    expect(res.headers.get("Content-Security-Policy")).toBe(CONTENT_CSP);
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
  });

  it("a version's CUSTOM 404 page keeps the tenant CSP (it may load site assets)", async () => {
    const { objects } = deploy({
      "404.html": { body: "<h1>custom 404</h1>", content_type: "text/html" },
    });
    const res = await serveNoCache(get(HOST, "/missing"), envFor(PUBLIC_ROUTE, HOST, objects));
    expect(res.status).toBe(404);
    expect(await res.text()).toBe("<h1>custom 404</h1>");
    expect(res.headers.get("Content-Security-Policy")).toBe(CONTENT_CSP);
  });
});

// --- service-worker registration block --------------------------------------

describe("service-worker registration is blocked on content origins", () => {
  it("recognizes the conventional SW script names (case-insensitive)", () => {
    for (const p of ["sw.js", "service-worker.js", "ngsw-worker.js", "Service-Worker.JS", "a/b/sw.js"]) {
      expect(isServiceWorkerScript(p)).toBe(true);
    }
    for (const p of ["app.js", "index.html", "assets/main.4f3a9c2b.js", "swagger.json"]) {
      expect(isServiceWorkerScript(p)).toBe(false);
    }
  });

  it("404s a request for /sw.js even when the manifest ships one (no scriptable body)", async () => {
    // Put a real entry at sw.js in the manifest; the Worker must STILL refuse it.
    const { objects } = deploy({
      "sw.js": { body: "self.addEventListener('install', () => {})", content_type: "text/javascript" },
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects);
    const res = await serveNoCache(get(HOST, "/sw.js"), env);
    expect(res.status).toBe(404);
    // The platform 404 body, NOT the service-worker script bytes.
    const body = await res.text();
    expect(body).not.toContain("addEventListener");
    expect(body).toContain("404");
  });

  it("blocks the SW script on the GATED path too (after a valid token)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "service-worker.js": { body: "self.x=1", content_type: "text/javascript" },
    });
    const env = gatedEnv(route, GATED_HOST, objects);
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID });
    const res = await serve(getWithCookie(GATED_HOST, "/service-worker.js", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(404);
    expect(await res.text()).not.toContain("self.x");
  });
});

// --- edge rate limiting (pure) ----------------------------------------------

describe("rateLimitDecision (fixed-window KV counter)", () => {
  const NOW = 1_700_000_000_000; // fixed epoch-ms

  it("allows requests under the limit, then 429s once over", async () => {
    const kv = mockCounterKV();
    const policy = { limit: 3, windowSeconds: 60 };
    const id = "ip:1.2.3.4";
    const results = [];
    for (let i = 0; i < 4; i++) {
      results.push(await rateLimitDecision(kv, id, NOW, policy));
    }
    expect(results.map((r) => r.allowed)).toEqual([true, true, true, false]);
    expect(results[3]!.retryAfterSeconds).toBeGreaterThan(0);
    expect(results[3]!.retryAfterSeconds).toBeLessThanOrEqual(60);
  });

  it("resets in the NEXT window (a later window is a fresh counter key)", async () => {
    const kv = mockCounterKV();
    const policy = { limit: 1, windowSeconds: 60 };
    const id = "ip:9.9.9.9";
    expect((await rateLimitDecision(kv, id, NOW, policy)).allowed).toBe(true);
    expect((await rateLimitDecision(kv, id, NOW, policy)).allowed).toBe(false);
    // Advance past the window → new key → allowed again.
    const next = NOW + 61_000;
    expect((await rateLimitDecision(kv, id, next, policy)).allowed).toBe(true);
    expect(windowKey(id, NOW, policy)).not.toBe(windowKey(id, next, policy));
  });

  it("is a no-op (fail open) when no counter KV is bound", async () => {
    const r = await rateLimitDecision(undefined, "ip:1.1.1.1", NOW, DEFAULT_RATE_LIMIT);
    expect(r.allowed).toBe(true);
  });

  it("derives the identity from CF-Connecting-IP, else falls back to host", () => {
    const withIp = new Request("https://h/", { headers: { "CF-Connecting-IP": "203.0.113.7" } });
    expect(rateLimitIdentity(withIp, "h")).toBe("ip:203.0.113.7");
    expect(rateLimitIdentity(new Request("https://h/"), "acme.example")).toBe("host:acme.example");
  });
});

describe("serve() enforces the edge rate limit end-to-end", () => {
  it("serves under the limit, then returns 429 with Retry-After + platform CSP", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const LIMITS = mockCounterKV();
    const env: Env = {
      ...envFor(PUBLIC_ROUTE, HOST, objects),
      LIMITS,
      RATE_LIMIT_MAX: "2",
      RATE_LIMIT_WINDOW_SECONDS: "60",
    };
    const req = () =>
      new Request(`https://${HOST}/`, {
        method: "GET",
        headers: { "CF-Connecting-IP": "198.51.100.42" },
      });

    const a = await serve(req(), env, { cache: null });
    const b = await serve(req(), env, { cache: null });
    const c = await serve(req(), env, { cache: null });
    expect(a.status).toBe(200);
    expect(b.status).toBe(200);
    expect(c.status).toBe(429);
    const retry = c.headers.get("Retry-After");
    expect(retry).not.toBeNull();
    expect(Number(retry)).toBeGreaterThan(0);
    expect(c.headers.get("Content-Security-Policy")).toBe(PLATFORM_CSP);
    expect(c.headers.get("Cache-Control")).toBe("no-store");
  });

  it("does not rate-limit when no LIMITS binding is present (public fast path stays cheap)", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const env = envFor(PUBLIC_ROUTE, HOST, objects); // no LIMITS
    for (let i = 0; i < 50; i++) {
      const res = await serve(
        new Request(`https://${HOST}/`, { headers: { "CF-Connecting-IP": "10.0.0.1" } }),
        env,
        { cache: null },
      );
      expect(res.status).toBe(200);
    }
  });
});

// --- per-org suspension / over-limit ----------------------------------------

describe("readOrgStatus + isBlockingStatus", () => {
  it("classifies suspended / over_limit as blocking; active / absent as not", () => {
    expect(isBlockingStatus("suspended")).toBe(true);
    expect(isBlockingStatus("over_limit")).toBe(true);
    expect(isBlockingStatus("active")).toBe(false);
    expect(isBlockingStatus(null)).toBe(false);
  });

  it("reads a bare status string or a {status} JSON envelope", async () => {
    const kv = mockCounterKV({
      [`org_status:${ORG_ID}`]: "suspended",
      [`org_status:${SITE_ID}`]: JSON.stringify({ status: "over_limit", reason: "egress" }),
    });
    expect(await readOrgStatus(kv, ORG_ID)).toBe("suspended");
    expect(await readOrgStatus(kv, SITE_ID)).toBe("over_limit");
    expect(await readOrgStatus(kv, "00000000-0000-0000-0000-000000000000")).toBeNull();
  });
});

describe("serve() blocks a suspended / over-limit org with a platform page", () => {
  for (const status of ["suspended", "over_limit"] as const) {
    it(`serves the platform 503 page (not tenant content) when org is ${status}`, async () => {
      const { objects } = deploy({
        "index.html": { body: "<h1>secret tenant</h1>", content_type: "text/html" },
      });
      const LIMITS = mockCounterKV({ [`org_status:${ORG_ID}`]: status });
      const env: Env = { ...envFor(PUBLIC_ROUTE, HOST, objects), LIMITS };
      const res = await serveNoCache(get(HOST, "/"), env);
      expect(res.status).toBe(503);
      const body = await res.text();
      expect(body).not.toContain("secret tenant");
      expect(body).toContain("unavailable");
      expect(res.headers.get("Retry-After")).toBe("300");
      expect(res.headers.get("Content-Security-Policy")).toBe(PLATFORM_CSP);
      expect(res.headers.get("Cache-Control")).toBe("no-store");
    });
  }

  it("blocks a GATED route's org too (before serving protected bytes)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const LIMITS = mockCounterKV({ [`org_status:${ORG_ID}`]: "suspended" });
    const env: Env = { ...gatedEnv(route, GATED_HOST, objects), LIMITS };
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID });
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(503);
    expect(await res.text()).not.toContain("secret");
  });

  it("serves normally when the org status is active / absent", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const LIMITS = mockCounterKV({ [`org_status:${ORG_ID}`]: "active" });
    const env: Env = { ...envFor(PUBLIC_ROUTE, HOST, objects), LIMITS };
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>home</h1>");
  });
});

// --- hard revocation (KV denylist / min_iat) --------------------------------

describe("parseRevokedEntry + denylistKeys", () => {
  it("parses the {min_iat} envelope and a bare numeric string", () => {
    expect(parseRevokedEntry(JSON.stringify({ min_iat: 1700 }))).toEqual({ min_iat: 1700 });
    expect(parseRevokedEntry("1700")).toEqual({ min_iat: 1700 });
    expect(parseRevokedEntry(null)).toBeNull();
    expect(parseRevokedEntry("not-json{")).toBeNull();
    expect(parseRevokedEntry(JSON.stringify({ min_iat: -5 }))).toBeNull();
  });

  it("builds the three denylist keys", () => {
    const k = denylistKeys("user-1", "site-1", "org-1");
    expect(k.user).toBe("revoked:user:user-1");
    expect(k.site).toBe("revoked:site:site-1");
    expect(k.org).toBe("revoked:org:org-1");
  });
});

describe("isRevoked (pure denylist check)", () => {
  const subject = { sub: "u1", siteId: "s1", orgId: "o1", iat: 1000 };

  it("is NOT revoked on a clean denylist", async () => {
    expect(await isRevoked(mockRevokedKV(), subject)).toBe(false);
  });

  it("revokes when ANY dimension's min_iat > token.iat", async () => {
    expect(
      await isRevoked(mockRevokedKV({ "revoked:user:u1": JSON.stringify({ min_iat: 1001 }) }), subject),
    ).toBe(true);
    expect(
      await isRevoked(mockRevokedKV({ "revoked:site:s1": JSON.stringify({ min_iat: 2000 }) }), subject),
    ).toBe(true);
    expect(
      await isRevoked(mockRevokedKV({ "revoked:org:o1": JSON.stringify({ min_iat: 5000 }) }), subject),
    ).toBe(true);
  });

  it("does NOT revoke a token issued at or after min_iat", async () => {
    // min_iat == iat → token issued before the cutoff second is invalid; a token
    // at iat=1000 with min_iat=1000 is NOT revoked (1000 is the first valid sec).
    expect(
      await isRevoked(mockRevokedKV({ "revoked:user:u1": JSON.stringify({ min_iat: 1000 }) }), subject),
    ).toBe(false);
    expect(
      await isRevoked(mockRevokedKV({ "revoked:user:u1": JSON.stringify({ min_iat: 999 }) }), subject),
    ).toBe(false);
  });

  it("fails CLOSED (revoked) when no denylist KV is bound", async () => {
    expect(await isRevoked(undefined, subject)).toBe(true);
  });

  it("fails CLOSED (revoked) when a denylist read throws", async () => {
    const throwing: RevokedKVLike = {
      async get() {
        throw new Error("KV down");
      },
    };
    expect(await isRevoked(throwing, subject)).toBe(true);
  });
});

describe("serve() gated path — hard revocation end-to-end", () => {
  /** Seed a denylist key into the ROUTES KV (the default denylist namespace). */
  function gatedEnvWithDenylist(
    route: RouteValue,
    host: string,
    objects: Record<string, string>,
    denylist: Record<string, string>,
  ): Env {
    return {
      ROUTES: mockRoutes({ [routeKey(host)]: route }, denylist),
      BUCKET: mockBucket(objects),
      EDGE_JWKS_URL: JWKS_URL,
      APP_AUTHZ_URL: AUTHZ_URL,
    };
  }

  it("REJECTS a token whose iat < revoked min_iat → 302 to /authz (re-auth)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    // The token is minted at "now"; set the user's min_iat 1000s in the FUTURE so
    // the token's iat is strictly before it.
    const future = Math.floor(Date.now() / 1000) + 1000;
    const VIEWER = "44444444-4444-4444-4444-444444444444";
    const env = gatedEnvWithDenylist(route, GATED_HOST, objects, {
      [`revoked:user:${VIEWER}`]: JSON.stringify({ min_iat: future }),
    });
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID, sub: VIEWER });

    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
    const loc = new URL(res.headers.get("Location")!);
    expect(loc.origin + loc.pathname).toBe(AUTHZ_URL);
  });

  it("PASSES a token whose iat > revoked min_iat (re-issued after the revocation)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    // min_iat is in the PAST; the freshly minted token's iat is after it → valid.
    const past = Math.floor(Date.now() / 1000) - 1000;
    const VIEWER = "44444444-4444-4444-4444-444444444444";
    const env = gatedEnvWithDenylist(route, GATED_HOST, objects, {
      [`revoked:user:${VIEWER}`]: JSON.stringify({ min_iat: past }),
    });
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID, sub: VIEWER });

    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>secret</h1>");
  });

  it("revokes via the SITE dimension (unshare) even for a different viewer", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const future = Math.floor(Date.now() / 1000) + 1000;
    const env = gatedEnvWithDenylist(route, GATED_HOST, objects, {
      [`revoked:site:${SITE_ID}`]: JSON.stringify({ min_iat: future }),
    });
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID });
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("revokes via the ORG dimension (taken from the ROUTE, not a token claim)", async () => {
    const signer = await makeEdgeSigner();
    const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };
    const { objects } = deploy({
      "index.html": { body: "<h1>secret</h1>", content_type: "text/html" },
    });
    const future = Math.floor(Date.now() / 1000) + 1000;
    const env = gatedEnvWithDenylist(route, GATED_HOST, objects, {
      [`revoked:org:${ORG_ID}`]: JSON.stringify({ min_iat: future }),
    });
    const token = await signer.mint({ host: GATED_HOST, siteId: SITE_ID });
    const res = await serve(getWithCookie(GATED_HOST, "/", token), env, {
      cache: null,
      fetchImpl: mockJwksFetch(signer.jwks),
    });
    expect(res.status).toBe(302);
  });

  it("does NOT consult the denylist on the PUBLIC path (revocation is gated-only)", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    // Even with a wide-open org revocation seeded, a PUBLIC route serves normally
    // (the public path is identity-free; revocation applies to edge tokens only).
    const future = Math.floor(Date.now() / 1000) + 100000;
    const env: Env = {
      ROUTES: mockRoutes(
        { [routeKey(HOST)]: PUBLIC_ROUTE },
        { [`revoked:org:${ORG_ID}`]: JSON.stringify({ min_iat: future }) },
      ),
      BUCKET: mockBucket(objects),
    };
    const res = await serveNoCache(get(HOST, "/"), env);
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>home</h1>");
  });
});

// --- LLM access: robots.txt / llms.txt / AI-crawler gating ------------------

describe("LLM access", () => {
  const GATED: RouteValue = { ...PUBLIC_ROUTE, access_mode: "org_only" };

  function reqUA(path: string, ua: string): Request {
    return new Request(`https://${HOST}${path}`, {
      method: "GET",
      headers: { "User-Agent": ua },
    });
  }

  it("public robots.txt allows all crawlers", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const res = await serveNoCache(
      get(HOST, "/robots.txt"),
      envFor(PUBLIC_ROUTE, HOST, objects),
    );
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain("Allow: /");
    expect(body).not.toContain("Disallow: /");
  });

  it("gated robots.txt disallows all crawlers", async () => {
    const res = await serveNoCache(
      get(HOST, "/robots.txt"),
      envFor(GATED, HOST, {}),
    );
    expect(res.status).toBe(200);
    expect(await res.text()).toContain("Disallow: /");
  });

  it("gated site 403s an AI crawler", async () => {
    const res = await serve(
      reqUA("/", "Mozilla/5.0 (compatible; GPTBot/1.1)"),
      envFor(GATED, HOST, {}),
      { cache: null },
    );
    expect(res.status).toBe(403);
  });

  it("public site serves content to an AI crawler", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
    });
    const res = await serve(
      reqUA("/", "ClaudeBot/1.0 (+https://www.anthropic.com)"),
      envFor(PUBLIC_ROUTE, HOST, objects),
      { cache: null },
    );
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>home</h1>");
  });

  it("gated /llms.txt is not exposed to crawlers (403)", async () => {
    const res = await serve(
      reqUA("/llms.txt", "CCBot/2.0"),
      envFor(GATED, HOST, {}),
      { cache: null },
    );
    expect(res.status).toBe(403);
  });

  it("public /llms.txt lists HTML pages, not assets", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>home</h1>", content_type: "text/html" },
      "about.html": { body: "<h1>about</h1>", content_type: "text/html" },
      "docs/index.html": { body: "<h1>docs</h1>", content_type: "text/html" },
      "style.css": { body: "body{}", content_type: "text/css" },
    });
    const res = await serveNoCache(
      get(HOST, "/llms.txt"),
      envFor(PUBLIC_ROUTE, HOST, objects),
    );
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body.startsWith(`# ${HOST}`)).toBe(true);
    expect(body).toContain(`https://${HOST}/`);
    expect(body).toContain(`https://${HOST}/about`);
    expect(body).toContain(`https://${HOST}/docs/`);
    expect(body).not.toContain("style.css");
  });
});

// --- Free-tier attribution banner -------------------------------------------

describe("attribution banner", () => {
  const FREE_ROUTE: RouteValue = {
    ...PUBLIC_ROUTE,
    schema_version: 3,
    plan_tier: "free",
  };

  /** Env for the free-tier route with the banner feature enabled. */
  function bannerEnv(objects: Record<string, string>, route = FREE_ROUTE): Env {
    return { ...envFor(route, HOST, objects), ATTRIBUTION_BANNER: "true" };
  }

  it("injects the banner into HTML for a free-tier org", async () => {
    const { objects } = deploy({
      "index.html": {
        body: "<html><body><h1>home</h1></body></html>",
        content_type: "text/html; charset=utf-8",
      },
    });
    const res = await serveNoCache(get(HOST, "/"), bannerEnv(objects));
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain("Deployed with");
    expect(body).toContain('href="https://dropway.dev"');
    expect(body).toContain('id="dropway-banner"');
    // Dismiss control + persistence hook are present.
    expect(body).toContain("dropway-banner-dismissed");
    // Original content is preserved.
    expect(body).toContain("<h1>home</h1>");
    // Injected right after <body>, before the tenant's own content.
    expect(body.indexOf("dropway-banner")).toBeLessThan(body.indexOf("<h1>home</h1>"));
    // Content-Length reflects the (larger) injected body.
    expect(Number(res.headers.get("Content-Length"))).toBe(
      new TextEncoder().encode(body).length,
    );
  });

  it("prepends the banner when the HTML has no <body>", async () => {
    const { objects } = deploy({
      "index.html": { body: "<h1>fragment</h1>", content_type: "text/html" },
    });
    const res = await serveNoCache(get(HOST, "/"), bannerEnv(objects));
    const body = await res.text();
    expect(body.indexOf("dropway-banner")).toBeLessThan(body.indexOf("<h1>fragment</h1>"));
  });

  it("does NOT inject for a non-free tier", async () => {
    const { objects } = deploy({
      "index.html": { body: "<body><h1>home</h1></body>", content_type: "text/html" },
    });
    const proRoute: RouteValue = { ...FREE_ROUTE, plan_tier: "pro" };
    const res = await serveNoCache(get(HOST, "/"), bannerEnv(objects, proRoute));
    const body = await res.text();
    expect(body).not.toContain("dropway-banner");
    expect(body).toBe("<body><h1>home</h1></body>");
  });

  it("does NOT inject when plan_tier is absent (older projection)", async () => {
    const { objects } = deploy({
      "index.html": { body: "<body><h1>home</h1></body>", content_type: "text/html" },
    });
    // PUBLIC_ROUTE has no plan_tier.
    const res = await serveNoCache(get(HOST, "/"), bannerEnv(objects, PUBLIC_ROUTE));
    const body = await res.text();
    expect(body).not.toContain("dropway-banner");
  });

  it("does NOT inject when the feature flag is unset", async () => {
    const { objects } = deploy({
      "index.html": { body: "<body><h1>home</h1></body>", content_type: "text/html" },
    });
    // envFor() leaves ATTRIBUTION_BANNER unset.
    const res = await serveNoCache(get(HOST, "/"), envFor(FREE_ROUTE, HOST, objects));
    const body = await res.text();
    expect(body).not.toContain("dropway-banner");
  });

  it("leaves non-HTML assets byte-identical", async () => {
    const css = "body{color:red}";
    const { objects } = deploy({
      "index.html": { body: "<body></body>", content_type: "text/html" },
      "style.css": { body: css, content_type: "text/css" },
    });
    const res = await serveNoCache(get(HOST, "/style.css"), bannerEnv(objects));
    const body = await res.text();
    expect(body).toBe(css);
    expect(body).not.toContain("dropway-banner");
  });

  it("caches the banner-injected body (warm PoP hit carries the banner)", async () => {
    const { objects } = deploy({
      "index.html": { body: "<body><h1>home</h1></body>", content_type: "text/html" },
    });
    const cache = mockCache();
    const env = bannerEnv(objects);
    // Cold miss populates the cache with the injected body.
    const cold = await serve(get(HOST, "/"), env, { cache });
    expect(await cold.text()).toContain("dropway-banner");
    // Warm hit returns the cached (already-injected) body without re-resolving.
    const warm = await serve(get(HOST, "/"), { ...env, BUCKET: mockBucket({}) }, { cache });
    expect(warm.status).toBe(200);
    expect(await warm.text()).toContain("dropway-banner");
  });

  it("does NOT inject into non-UTF-8 HTML (would corrupt the bytes)", async () => {
    const html = "<body><h1>café</h1></body>";
    const { objects } = deploy({
      "index.html": { body: html, content_type: "text/html; charset=iso-8859-1" },
    });
    const res = await serveNoCache(get(HOST, "/"), bannerEnv(objects));
    const body = await res.text();
    expect(body).not.toContain("dropway-banner");
    expect(body).toBe(html); // streamed through untouched
  });

  it("a HEAD reports the injected Content-Length with an empty body (no buffering)", async () => {
    const { objects } = deploy({
      "index.html": {
        body: "<html><body><h1>home</h1></body></html>",
        content_type: "text/html; charset=utf-8",
      },
    });
    const getRes = await serveNoCache(get(HOST, "/"), bannerEnv(objects));
    const getLen = Number(getRes.headers.get("Content-Length"));
    expect((await getRes.text()).length).toBeGreaterThan(0);

    const headReq = new Request(`https://${HOST}/`, { method: "HEAD" });
    const headRes = await serveNoCache(headReq, bannerEnv(objects));
    expect(headRes.status).toBe(200);
    // Same Content-Length a GET would return — derived arithmetically, body empty.
    expect(Number(headRes.headers.get("Content-Length"))).toBe(getLen);
    expect(await headRes.text()).toBe("");
  });

  it("flips the banner immediately on a tier change (plan_tier is in the cache key)", async () => {
    const { objects } = deploy({
      "index.html": { body: "<body><h1>home</h1></body>", content_type: "text/html" },
    });
    const cache = mockCache();
    // Free-tier request caches the banner-injected body under a free-keyed entry.
    const free = await serve(get(HOST, "/"), bannerEnv(objects), { cache });
    expect(await free.text()).toContain("dropway-banner");

    // The org upgrades: same version_id, plan_tier now "pro" (reprojected in KV).
    // Because plan_tier is part of the cache key, this MISSES the free entry and
    // serves a fresh, un-bannered body immediately — not the stale cached banner.
    const proRoute: RouteValue = { ...FREE_ROUTE, plan_tier: "pro" };
    const proEnv = { ...envFor(proRoute, HOST, objects), ATTRIBUTION_BANNER: "true" };
    const pro = await serve(get(HOST, "/"), proEnv, { cache });
    expect(await pro.text()).not.toContain("dropway-banner");
  });
});

// --- serve_404 PostHog emission (the "why can't a user reach the site?" event) --

describe("serve_404 emission", () => {
  afterEach(() => vi.unstubAllGlobals());

  function pageGet(host: string): Request {
    // A top-level navigation (Sec-Fetch-Dest: document) is what is404Reportable
    // counts as "a user trying to load a page".
    return new Request(`https://${host}/missing`, {
      method: "GET",
      headers: { "Sec-Fetch-Dest": "document" },
    });
  }

  it("schedules a serve_404 capture for a page 404 when POSTHOG_KEY is set", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const scheduled: Promise<unknown>[] = [];
    const env: Env = {
      ROUTES: mockRoutes({}), // unknown host → route_not_found
      BUCKET: mockBucket({}),
      POSTHOG_KEY: "phc_test",
    };

    const res = await serve(pageGet("unknown.dropwaycontent.com"), env, {
      cache: null,
      waitUntil: (p) => scheduled.push(p),
    });

    expect(res.status).toBe(404);
    expect(scheduled).toHaveLength(1);
    await Promise.all(scheduled);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [endpoint, init] = fetchMock.mock.calls[0]!;
    expect(String(endpoint)).toContain("/capture/");
    const body = JSON.parse((init as { body: string }).body);
    expect(body.event).toBe("serve_404");
    expect(body.properties.reason).toBe("route_not_found");
  });

  it("does not schedule a capture without a POSTHOG_KEY", async () => {
    const scheduled: Promise<unknown>[] = [];
    const env: Env = { ROUTES: mockRoutes({}), BUCKET: mockBucket({}) };
    const res = await serve(pageGet("unknown.dropwaycontent.com"), env, {
      cache: null,
      waitUntil: (p) => scheduled.push(p),
    });
    expect(res.status).toBe(404);
    expect(scheduled).toHaveLength(0);
  });
});
