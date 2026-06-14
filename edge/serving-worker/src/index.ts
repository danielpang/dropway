// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shipped serving Worker — *.shippedusercontent.com (Cloudflare Workers, Module
// syntax). Phase 1 implements the PUBLIC serve path only: the 95% case that is
// JWT-free and cacheable (docs/ARCHITECTURE.md §3/§6).
//
// Request lifecycle (public):
//   browser → PoP → resolve `route:<host>` from KV (ROUTES) →
//   if access_mode === "public": resolve the path to an R2 object key under the
//   version's manifest prefix, stream it from R2 (BUCKET) with the right
//   Content-Type + Cache-Control, with index.html directory fallback and a
//   404 page.
//
// The public path NEVER reads a JWT. Identity-gated modes
// (password|allowlist|org_only) are Phase-2 stubs that return a clearly-marked
// 501 (see `gatedStub`) and DO NOT exchange identity here — that is the
// host-scoped `/authz` exchange on app.shipped.app, built in Phase 2.

import {
  type RouteValue,
  notFoundKey,
  parseRouteValue,
  resolveObjectKeys,
  routeKey,
} from "./route";
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

// --- Worker entry -----------------------------------------------------------

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    return serve(request, env);
  },
};

/**
 * Core request handler, exported for tests. Pure with respect to the injected
 * `env` bindings; performs no global side effects.
 */
export async function serve(request: Request, env: Env): Promise<Response> {
  // Only GET/HEAD are meaningful for static content.
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("Method Not Allowed", {
      status: 405,
      headers: { Allow: "GET, HEAD", ...securityHeaders() },
    });
  }

  const url = new URL(request.url);

  // 1. Resolve the host → route value from KV. The Host header is the tenant
  //    identity on the content domain; there is no JWT to consult.
  const raw = await env.ROUTES.get(routeKey(url.host), "json");
  const route = parseRouteValue(raw);
  if (route === null) {
    // Unknown host or malformed/old projection → fail closed.
    return notFound(null);
  }

  // 2. Dispatch by access mode. Phase 1 = public only.
  switch (route.access_mode) {
    case "public":
      return servePublic(request, env, route, url);
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
 * PUBLIC serve path — no JWT, cacheable. Resolves the request path to an R2
 * object key under the version prefix, streams it, falling back to a directory
 * index and then a 404 page.
 */
async function servePublic(
  request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
): Promise<Response> {
  const candidates = resolveObjectKeys(route, url.pathname);
  if (candidates.length === 0) {
    // Unsafe path (traversal etc.) → 404, never an error that leaks structure.
    return notFound(route, env);
  }

  for (const key of candidates) {
    const object = await env.BUCKET.get(key);
    if (object === null) continue;

    const headers = publicResponseHeaders(key, {
      etag: object.httpEtag,
      lastModified: object.uploaded,
      contentLength: object.size,
    });
    // HEAD must not carry a body; GET streams the object.
    const body = request.method === "HEAD" ? null : object.body;
    return new Response(body, { status: 200, headers });
  }

  // No candidate existed → serve the version's 404 page (or a default).
  return notFound(route, env);
}

/**
 * 404 response. If a route is known and a bucket is available, prefer the
 * version's own `404.html`; otherwise a minimal platform 404. Always carries
 * security headers and short, public cache (a 404 is still safe to cache).
 */
async function notFound(route: RouteValue | null, env?: Env): Promise<Response> {
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "public, max-age=30",
    ...securityHeaders(),
  });

  if (route && env) {
    const custom = await env.BUCKET.get(notFoundKey(route));
    if (custom !== null) {
      return new Response(custom.body, { status: 404, headers });
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
