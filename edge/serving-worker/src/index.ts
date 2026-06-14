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

import {
  type RouteValue,
  cleanPath,
  isRouteExpired,
  parseRouteValue,
  routeKey,
} from "./route";
import {
  type Manifest,
  NOT_FOUND_PATH,
  blobKey,
  manifestKey,
  parseManifest,
  resolveManifestEntry,
} from "./manifest";
import { publicResponseHeaders, securityHeaders } from "./http";
import { type GatedConfig, gatedConfig } from "./config";
import type { FetchLike } from "./edgetoken";
import { serveGated } from "./gated";

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

/** Worker environment bindings + vars (declared in wrangler.toml). */
export interface Env {
  /** KV namespace: `route:<host>` → RouteValue (written only by the Go API). */
  ROUTES: RoutesKVLike;
  /** Single private R2 bucket holding all tenant content, per-org prefixed. */
  BUCKET: BucketLike;
  /**
   * Edge JWKS endpoint (the Go API's edge signer public keys). The gated path
   * fetches + caches this to verify the host-scoped edge token. Optional: falls
   * back to the production default in config.ts.
   */
  EDGE_JWKS_URL?: string;
  /**
   * Dashboard `/authz` exchange origin. A gated request with no/invalid edge
   * token 302s here. Optional: falls back to the production default.
   */
  APP_AUTHZ_URL?: string;
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
  /**
   * Fetch used by the gated path to load the edge JWKS. Defaults to the runtime
   * `fetch`. Injected in tests to serve a mock JWKS without network.
   */
  fetchImpl?: FetchLike;
  /** Clock injection for tests (edge-token exp + route expiry). */
  now?: Date;
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
    // Unknown host or malformed/old projection → fail closed. The contract
    // validator accepts schema_version 1 AND 2 (v2 adds optional expires_at);
    // anything else (or a malformed shape) parses to null here.
    return notFound(null);
  }

  // 2. Edge link-expiry (v2 RouteValue.expires_at). A public/unlisted share can
  //    carry an expiry the Go API serializes into the projection; once past, the
  //    Worker refuses to serve and shows a platform "link expired" page. (Gated
  //    expiry is refused earlier, at mint time in the Go API.)
  const now = opts.now ?? new Date();
  if (isRouteExpired(route, now)) {
    return linkExpired();
  }

  // 3. Dispatch by access mode.
  switch (route.access_mode) {
    case "public":
      return servePublic(request, env, route, url, opts);
    case "password":
    case "allowlist":
    case "org_only":
      return serveGated(request, env, route, url, gateOpts(env, opts), {
        serveContent: () => servePublicBody(request, env, route, url),
      });
    default:
      // Unreachable given parseRouteValue, but fail closed.
      return notFound(route);
  }
}

/** Resolve the gated-path config + injected fetch from env/opts. */
function gateOpts(
  env: Env,
  opts: ServeOptions,
): { cfg: GatedConfig; fetchImpl: FetchLike; now: number } {
  return {
    cfg: gatedConfig(env),
    fetchImpl: opts.fetchImpl ?? defaultFetch(),
    now: (opts.now ?? new Date()).getTime(),
  };
}

/** The runtime `fetch`, narrowed to FetchLike (tests inject their own). */
function defaultFetch(): FetchLike {
  return (input: string) => fetch(input);
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

  const resolved = await resolveBlob(request, env, route, url);
  if (resolved.kind === "not-found") return resolved.response;

  const headers = publicResponseHeaders(resolved.servedPath, {
    contentType: resolved.contentType,
    etag: resolved.etag,
    lastModified: resolved.lastModified,
    contentLength: resolved.contentLength,
  });

  const response = new Response(resolved.body, { status: 200, headers });

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
 * Build the body of a GATED (password/allowlist/org_only) success response —
 * the SAME manifest→blob resolution as the public path, but WITHOUT the public
 * Cache API and with the caller (gated module) overriding Cache-Control to
 * `private, no-store` (§10: protected bytes never enter a shared cache). Returns
 * a 200 Response with content headers, or the appropriate 404 on a miss/drift.
 * `bodyFor` strips the body for HEAD. Never consults or writes any cache.
 */
async function servePublicBody(
  request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
): Promise<Response> {
  const resolved = await resolveBlob(request, env, route, url);
  if (resolved.kind === "not-found") return resolved.response;

  const headers = publicResponseHeaders(resolved.servedPath, {
    contentType: resolved.contentType,
    etag: resolved.etag,
    lastModified: resolved.lastModified,
    contentLength: resolved.contentLength,
  });
  const response = new Response(resolved.body, { status: 200, headers });
  return bodyFor(request, response);
}

/** Outcome of resolving a request path to its R2 blob (shared public/gated). */
type BlobResolution =
  | {
      kind: "ok";
      servedPath: string;
      contentType: string;
      body: ReadableStream | null;
      etag?: string;
      lastModified?: Date;
      contentLength?: number;
    }
  | { kind: "not-found"; response: Response };

/**
 * Resolve a request to its content-addressed blob via the deploy manifest:
 * sanitize the path, load + validate the manifest, match an entry (with the
 * index/pretty-URL fallbacks), and fetch the blob from R2. Returns the blob
 * stream + metadata, or a ready 404 (custom page when the manifest ships one).
 * This is the shared core of BOTH the public and gated serve paths; only the
 * surrounding Cache-Control / Cache-API behavior differs.
 */
async function resolveBlob(
  _request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
): Promise<BlobResolution> {
  // Sanitize the request path before resolving it against the manifest.
  const clean = cleanPath(url.pathname);
  if (clean === null) {
    // Unsafe path (traversal etc.) → 404, never an error that leaks structure.
    return { kind: "not-found", response: await notFound(route) };
  }

  const manifest = await loadManifest(env, route);
  if (manifest === null) {
    // Missing/corrupt manifest → fail closed with the default 404.
    return { kind: "not-found", response: await notFound(route) };
  }

  const match = resolveManifestEntry(manifest, clean);
  if (match === null) {
    // No served path matched → the version's custom 404 page, else the default.
    return { kind: "not-found", response: await notFound(route, env, manifest) };
  }

  const object = await env.BUCKET.get(blobKey(route.org_id, match.entry.sha256));
  if (object === null) {
    // Manifest referenced a blob not in R2 — projection drift. Fail closed.
    return { kind: "not-found", response: await notFound(route, env, manifest) };
  }

  return {
    kind: "ok",
    servedPath: match.path,
    contentType: match.entry.content_type,
    body: object.body,
    etag: object.httpEtag,
    lastModified: object.uploaded,
    contentLength: object.size ?? match.entry.size,
  };
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
 * Platform "link expired" page — served when a public/unlisted route carries an
 * `expires_at` (v2 RouteValue) that is now past (docs/ARCHITECTURE.md §6, edge
 * link-expiry). 410 Gone is the right status (the resource intentionally no
 * longer exists at this URL). Never shared-cached so a future re-publish (new
 * expiry) is visible immediately.
 */
export function linkExpired(): Response {
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "no-store",
    ...securityHeaders(),
  });
  return new Response(LINK_EXPIRED_HTML, { status: 410, headers });
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

/**
 * Platform-controlled "link expired" page (served on a past `expires_at`). Kept
 * static + self-contained (no tenant content, no scripts) — anti-phishing parity
 * with the password gate (§10): an expired link must show a platform page tenant
 * JS can neither render nor script.
 */
const LINK_EXPIRED_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Link expired</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>This link has expired</h1>
    <p>The share link for this site is no longer active. Ask the site owner for a new one.</p>
  </main>
</body>
</html>
`;
