// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shipped serving Worker — *.shippedusercontent.com (Cloudflare Workers, Module
// syntax). Phase 1 implements the PUBLIC serve path only: the 95% case that is
// JWT-free and cacheable (docs/ARCHITECTURE.md §3/§6).
//
// Request lifecycle (public), content-addressed layout:
//   browser → PoP → resolve `route:<host>` from KV (ROUTES) →
//   if access_mode === "public":
//     fetch the deploy manifest from R2 at
//       manifests/<org_id>/<site_id>/<version_id>.json
//     resolve the request path (index.html + directory fallback) to its
//       { sha256, content_type }, then stream the blob from R2 at
//       blobs/<org_id>/<sha256> with the right Content-Type + Cache-Control
//       (immutable for hashed assets, short for HTML); 404 page otherwise.
//
// The public path NEVER reads a JWT. Identity-gated modes
// (password|allowlist|org_only) are Phase-2 stubs that return a clearly-marked
// 501 (see `gatedStub`) and DO NOT exchange identity here — that is the
// host-scoped `/authz` exchange on app.shipped.app, built in Phase 2.

import { type RouteValue, cleanPath, parseRouteValue, routeKey } from "./route";
import {
  type Manifest,
  NOT_FOUND_PATH,
  blobKey,
  manifestKey,
  parseManifest,
  resolveManifestEntry,
} from "./manifest";
import { publicResponseHeaders, securityHeaders } from "./http";

// --- Binding interfaces -----------------------------------------------------
// Narrow structural types over the R2/KV bindings, so the serving logic can be
// driven by in-memory mocks in tests without @cloudflare/workers-types runtime.

/** Minimal R2 object shape we consume (subset of R2ObjectBody). */
export interface R2ObjectLike {
  body: ReadableStream | null;
  httpEtag?: string;
  uploaded?: Date;
  size?: number;
  /** Present on R2ObjectBody; lets us read the manifest JSON in one call. */
  json?: () => Promise<unknown>;
}

/** Minimal R2 bucket binding: a content-addressed get by key. */
export interface BucketLike {
  get(key: string): Promise<R2ObjectLike | null>;
}

/** Minimal KV binding: read the route value as parsed JSON. */
export interface RoutesKVLike {
  get(key: string, type: "json"): Promise<unknown>;
}

/** Worker environment bindings (declared in wrangler.toml). */
export interface Env {
  /** KV namespace: `route:<host>` → RouteValue (written only by the Go API). */
  ROUTES: RoutesKVLike;
  /** Single private R2 bucket holding all tenant content, per-org prefixed. */
  BUCKET: BucketLike;
}

/**
 * Minimal Cache API surface. The public path caches successful blob responses
 * so a warm PoP serves without an R2 Class-B op. Injected/overridable so tests
 * run on the node pool without the global `caches`.
 */
export interface CacheLike {
  match(request: Request): Promise<Response | undefined>;
  put(request: Request, response: Response): Promise<void>;
}

/** The default (per-PoP) cache, when the runtime exposes one. */
function defaultCache(): CacheLike | null {
  const c = (globalThis as { caches?: { default?: CacheLike } }).caches;
  return c?.default ?? null;
}

// --- Worker entry -----------------------------------------------------------

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    return serve(request, env, { waitUntil: (p) => ctx.waitUntil(p) });
  },
};

/** Side-channels the Worker uses but tests can stub (cache + background work). */
export interface ServeOptions {
  /** Cache API instance; defaults to `caches.default` when available. */
  cache?: CacheLike | null;
  /** Schedules background work (cache writes) past the response. */
  waitUntil?: (p: Promise<unknown>) => void;
}

/**
 * Core request handler, exported for tests. Pure with respect to the injected
 * `env` bindings; performs no global side effects beyond an optional best-effort
 * cache write (scheduled via `waitUntil`).
 */
export async function serve(
  request: Request,
  env: Env,
  opts: ServeOptions = {},
): Promise<Response> {
  // Only GET/HEAD are meaningful for static content.
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("Method Not Allowed", {
      status: 405,
      headers: { Allow: "GET, HEAD", ...securityHeaders() },
    });
  }

  const url = new URL(request.url);

  // 1. Resolve the host → route value from KV. The Host header is the tenant
  //    identity on the content domain; there is no JWT to consult. The shared
  //    contract validator also pins `schema_version` and fails closed on an
  //    unsupported (or malformed) projection value.
  const raw = await env.ROUTES.get(routeKey(url.host), "json");
  const route = parseRouteValue(raw);
  if (route === null) {
    // Unknown host or malformed/old projection → fail closed.
    return notFound(null);
  }

  // 2. Dispatch by access mode. Phase 1 = public only.
  switch (route.access_mode) {
    case "public":
      return servePublic(request, env, route, url, opts);
    case "password":
    case "allowlist":
    case "org_only":
      return gatedStub(route, url);
    default:
      // Unreachable given parseRouteValue, but fail closed.
      return notFound(route);
  }
}

/**
 * PUBLIC serve path — no JWT, cacheable. Fetches the deploy manifest, resolves
 * the request path (with index.html + directory fallback) to a content-addressed
 * blob, then streams it from R2 with the manifest's Content-Type and the policy
 * Cache-Control. Successful responses are written to the Cache API so a warm PoP
 * serves without an R2 op.
 */
async function servePublic(
  request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
  opts: ServeOptions,
): Promise<Response> {
  const cache = opts.cache !== undefined ? opts.cache : defaultCache();

  // Cache hit? Serve straight from the PoP (HEAD reuses the GET-keyed entry,
  // stripping the body below). The cache key is content-version-scoped because
  // the manifest is per version and the blob key is the sha256 (see cacheKey).
  if (cache) {
    const key = cacheKey(route, url);
    const hit = await cache.match(key);
    if (hit) return bodyFor(request, hit);
  }

  // Sanitize the request path before resolving it against the manifest.
  const clean = cleanPath(url.pathname);
  if (clean === null) {
    // Unsafe path (traversal etc.) → 404, never an error that leaks structure.
    return notFound(route);
  }

  // Fetch + cache (within this request) the deploy manifest.
  const manifest = await loadManifest(env, route);
  if (manifest === null) {
    // Missing/corrupt manifest → fail closed with the default 404 (we have no
    // manifest, so no custom 404 page to look up).
    return notFound(route);
  }

  const match = resolveManifestEntry(manifest, clean);
  if (match === null) {
    // No served path matched → the version's custom 404 page, else the default.
    return notFound(route, env, manifest);
  }

  const object = await env.BUCKET.get(blobKey(route.org_id, match.entry.sha256));
  if (object === null) {
    // Manifest referenced a blob that is not in R2 — projection drift. Fail
    // closed rather than serving the wrong/empty bytes.
    return notFound(route, env, manifest);
  }

  const headers = publicResponseHeaders(match.path, {
    contentType: match.entry.content_type,
    etag: object.httpEtag,
    lastModified: object.uploaded,
    contentLength: object.size ?? match.entry.size,
  });

  const response = new Response(object.body, { status: 200, headers });

  // Best-effort: populate the PoP cache for subsequent requests. We cache a
  // clone so the streamed body is still available to the caller.
  if (cache) {
    const key = cacheKey(route, url);
    const write = cache.put(key, response.clone());
    if (opts.waitUntil) opts.waitUntil(write);
    else await write.catch(() => {});
  }

  return bodyFor(request, response);
}

/**
 * Fetch + parse a version's deploy manifest from R2. The result is cached on the
 * route object for the lifetime of this request (`_manifest`), so a path that
 * probes several candidates does not re-fetch. Returns null when the manifest is
 * missing or fails validation (so the caller fails closed).
 */
async function loadManifest(env: Env, route: RouteValue): Promise<Manifest | null> {
  const cached = (route as RouteWithManifest)._manifest;
  if (cached !== undefined) return cached;

  let manifest: Manifest | null = null;
  const object = await env.BUCKET.get(manifestKey(route));
  if (object !== null) {
    // Prefer R2's streaming `.json()` when present; fall back to parsing the body.
    const raw = object.json ? await object.json() : await readBodyJson(object);
    manifest = parseManifest(raw);
  }

  (route as RouteWithManifest)._manifest = manifest;
  return manifest;
}

/** Internal: a route carrying its per-request memoized manifest. */
type RouteWithManifest = RouteValue & { _manifest?: Manifest | null };

/** Read + JSON-parse an R2 object body when `.json()` is unavailable (mocks). */
async function readBodyJson(object: R2ObjectLike): Promise<unknown> {
  if (object.body === null) return null;
  const text = await new Response(object.body).text();
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/**
 * The Cache API key for a public response. We key on the ORIGIN + version + path
 * so a pointer flip (new version_id) is a fresh key, and one host's cache can
 * never satisfy another's. Method is normalized to GET so HEAD reuses the entry.
 */
function cacheKey(route: RouteValue, url: URL): Request {
  const keyUrl = new URL(url.toString());
  keyUrl.search = ""; // static content does not vary by query string
  // Fold the version into the key path so a publish/rollback never serves stale.
  keyUrl.pathname = `/${route.version_id}${keyUrl.pathname}`;
  return new Request(keyUrl.toString(), { method: "GET" });
}

/** Return a response suitable for the request method (HEAD carries no body). */
function bodyFor(request: Request, response: Response): Response {
  if (request.method !== "HEAD") return response;
  return new Response(null, {
    status: response.status,
    headers: response.headers,
  });
}

/**
 * 404 response. If a route + manifest are known, prefer the version's own
 * `404.html` (resolved through the manifest → blob); otherwise a minimal
 * platform 404. Always carries security headers and short, public cache (a 404
 * is still safe to cache).
 */
async function notFound(
  route: RouteValue | null,
  env?: Env,
  manifest?: Manifest,
): Promise<Response> {
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "public, max-age=30",
    ...securityHeaders(),
  });

  if (route && env && manifest) {
    const entry = manifest.files[NOT_FOUND_PATH];
    if (entry !== undefined) {
      const object = await env.BUCKET.get(blobKey(route.org_id, entry.sha256));
      if (object !== null) {
        // Serve the custom 404 with its manifest Content-Type but the 404 status.
        headers.set("Content-Type", entry.content_type);
        return new Response(object.body, { status: 404, headers });
      }
    }
  }

  return new Response(DEFAULT_404_HTML, { status: 404, headers });
}

/**
 * Phase-2 stub for identity-gated access modes. We DELIBERATELY do not read a
 * JWT or perform any identity exchange here. The real implementation is the
 * host-scoped `/authz` exchange on app.shipped.app (docs/ARCHITECTURE.md §6):
 * the Worker will 302 there and later verify a host-scoped token — never the
 * operator dashboard JWT. Until then, fail closed with a clear 501.
 *
 * TODO(phase-2): replace with the `/authz` exchange:
 *   - password  → prompt + verify → host-scoped signed cookie (no identity).
 *   - allowlist / org_only → 302 → app.shipped.app/authz?return=<host>.
 */
function gatedStub(route: RouteValue, url: URL): Response {
  const headers = new Headers({
    "Content-Type": "text/plain; charset=utf-8",
    // Never cache a gated response in the shared namespace (§10 invariant).
    "Cache-Control": "private, no-store",
    "X-Shipped-Phase": "2",
    "X-Shipped-Access-Mode": route.access_mode,
    ...securityHeaders(),
  });
  return new Response(
    `501 Not Implemented — Phase 2: /authz exchange.\n` +
      `Host "${url.host}" is served with access_mode="${route.access_mode}", ` +
      `which requires the host-scoped identity exchange on app.shipped.app ` +
      `(not yet implemented). The public serve path is JWT-free; gated tiers ` +
      `are Phase 2.\n`,
    { status: 501, headers },
  );
}

const DEFAULT_404_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>404 — Not Found</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; }
  h1 { font-size: 3rem; margin: 0 0 .25rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>404</h1>
    <p>This page could not be found.</p>
  </main>
</body>
</html>
`;
