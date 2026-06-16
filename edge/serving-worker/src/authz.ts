// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The gated serving path — `password` | `allowlist` | `org_only` (the /authz
// EXCHANGE, docs/ARCHITECTURE.md §6). The Worker is a thin gate: it verifies the
// host-scoped EDGE TOKEN (cookie) and either serves the content (private, never
// shared-cached) or bounces the viewer to the dashboard `/authz` exchange. It
// NEVER reads the operator Better Auth JWT — only the edge token.
//
// Flow:
//   GET https://<host>/<path>  (access_mode != public)
//     ├─ __Host-edge cookie present + valid (aud==host, site_id matches, EdDSA,
//     │    iss, exp)  →  serve the content (caller streams the blob)            ✅
//     └─ absent / invalid  → 302 https://app.dropway.dev/authz?host=<host>&next=<path>
//
//   GET https://<host>/__authz/callback?token=<edge-token>&next=<path>
//     (the dashboard 302s here AFTER minting the token)
//     ├─ verify token (aud==host, site_id matches the route, …)
//     ├─ Set-Cookie __Host-edge (host-only, Secure, HttpOnly, SameSite=Lax)
//     └─ 302 → a SAFE same-host `next` path (open-redirect / off-host rejected)  ✅
//
// "Private cache-control, never shared cache" is enforced for every gated
// response (the §10 cache-key-isolation invariant): the public Cache API and any
// shared cache must never hold protected bytes.

import type { RouteValue } from "./route";
import { EDGE_COOKIE_NAME, type GatedConfig } from "./config";
import { type EdgeClaims, type FetchLike, verifyEdgeToken } from "./edgetoken";
import { securityHeaders } from "./http";

// Re-export the cookie name so consumers/tests have a single import surface for
// the gated path (the canonical definition lives in config.ts).
export { EDGE_COOKIE_NAME };

/** The callback path the dashboard 302s to after minting (on the content host). */
export const AUTHZ_CALLBACK_PATH = "/__authz/callback";

/** True when a request is the post-mint callback the dashboard redirects to. */
export function isAuthzCallback(pathname: string): boolean {
  return pathname === AUTHZ_CALLBACK_PATH;
}

/** Headers that keep a gated response out of every shared/edge cache (§10). */
function noStoreHeaders(): Headers {
  const h = new Headers(securityHeaders());
  // `private` + `no-store` + `must-revalidate`: never stored in caches.default,
  // a shared CDN cache, or a browser disk cache that another viewer could read.
  h.set("Cache-Control", "private, no-store, max-age=0, must-revalidate");
  h.set("Vary", "Cookie");
  return h;
}

/** Parse the `__Host-edge` cookie value out of a request's Cookie header. */
export function readEdgeCookie(request: Request): string | null {
  const header = request.headers.get("Cookie");
  if (!header) return null;
  for (const part of header.split(";")) {
    const eq = part.indexOf("=");
    if (eq === -1) continue;
    const name = part.slice(0, eq).trim();
    if (name === EDGE_COOKIE_NAME) {
      const value = part.slice(eq + 1).trim();
      return value === "" ? null : value;
    }
  }
  return null;
}

/**
 * Build the 302 to the dashboard `/authz` exchange for an unauthenticated /
 * invalid-token gated request. `host` and the (safe) `next` path are passed so
 * the dashboard can mint a token bound to this host and return the viewer to the
 * page they wanted. The dashboard requires a Better Auth session there — the
 * Worker stays JWT-free.
 */
export function redirectToAuthz(cfg: GatedConfig, host: string, nextPath: string): Response {
  const url = new URL(cfg.appAuthzUrl);
  url.searchParams.set("host", host);
  // `next` is the path (+query) the viewer was trying to reach; the dashboard
  // echoes it back to the content-host callback. It is a redirect hint only,
  // never an authorization input.
  url.searchParams.set("next", safeNextPath(nextPath));
  const headers = noStoreHeaders();
  headers.set("Location", url.toString());
  return new Response(null, { status: 302, headers });
}

/**
 * Normalize an untrusted `next` into a SAFE, same-host absolute PATH (open-
 * redirect defense). We accept only a path that starts with a single "/" and is
 * not protocol-relative ("//evil") or scheme-bearing. Anything else collapses to
 * "/". The returned value always begins with exactly one "/".
 */
export function safeNextPath(next: string | null): string {
  if (!next) return "/";

  let candidate = next;
  // Decode once so an encoded "//" or "\" can't smuggle past the checks; a
  // malformed encoding is treated as unsafe.
  try {
    candidate = decodeURIComponent(next);
  } catch {
    return "/";
  }

  // Must be a path reference: starts with "/", but NOT "//" (protocol-relative)
  // and NOT a backslash form (browsers normalize "\" → "/", so "/\evil.com" or
  // "\\evil.com" would escape the host).
  if (!candidate.startsWith("/")) return "/";
  if (candidate.startsWith("//")) return "/";
  if (candidate.includes("\\")) return "/";
  // Reject control chars / whitespace: CR/LF would split the Location header,
  // and NUL and friends are never legitimate in a redirect target.
  for (let i = 0; i < candidate.length; i++) {
    const code = candidate.charCodeAt(i);
    if (code <= 0x20 || code === 0x7f) return "/";
  }

  return candidate;
}

/**
 * Build the host-only `Set-Cookie` for the edge token. The `__Host-` prefix
 * REQUIRES `Secure`, `Path=/`, and NO `Domain=` — the browser enforces this, so
 * the token is pinned to the exact content host (a sibling tenant can't read or
 * overwrite it). HttpOnly keeps it out of tenant JS; SameSite=Lax allows the
 * top-level navigation from the dashboard callback to carry it.
 */
export function edgeCookie(token: string, maxAgeSeconds: number): string {
  return (
    `${EDGE_COOKIE_NAME}=${token}` +
    `; Path=/` +
    `; Secure` +
    `; HttpOnly` +
    `; SameSite=Lax` +
    `; Max-Age=${Math.max(0, Math.floor(maxAgeSeconds))}`
  );
}

/** Max-Age for the edge cookie — matches the edge-token TTL (15m) on the Go side. */
export const EDGE_COOKIE_MAX_AGE = 15 * 60;

/**
 * Handle GET /__authz/callback?token=&next= on the content host. Verifies the
 * minted token against THIS route (aud==host, site_id matches), and on success
 * sets the `__Host-edge` cookie and 302s to the safe same-host `next`. On any
 * verification failure it 302s back to the `/authz` exchange (fail closed). The
 * dashboard is the only legitimate caller, but the token is fully re-verified
 * here regardless — the query param is untrusted.
 */
export async function handleAuthzCallback(
  _request: Request,
  route: RouteValue,
  url: URL,
  cfg: GatedConfig,
  fetchImpl: FetchLike,
  now?: number,
): Promise<Response> {
  const token = url.searchParams.get("token") ?? "";
  const nextPath = safeNextPath(url.searchParams.get("next"));

  const claims =
    token === ""
      ? null
      : await verifyTokenSafely({
          token,
          host: url.host,
          siteId: route.site_id,
          expectedMode: route.access_mode,
          jwksUrl: cfg.jwksUrl,
          fetchImpl,
          now,
        });

  if (claims === null) {
    // Bad/expired/mismatched token at the callback → back to the exchange.
    return redirectToAuthz(cfg, url.host, nextPath);
  }

  const headers = noStoreHeaders();
  headers.set("Set-Cookie", edgeCookie(token, EDGE_COOKIE_MAX_AGE));
  // Redirect to the SAFE same-host path the viewer originally wanted.
  headers.set("Location", new URL(nextPath, `https://${url.host}`).toString());
  return new Response(null, { status: 302, headers });
}

/**
 * Verify the edge cookie for a gated request. Returns the claims when the cookie
 * is present and valid for this route; null otherwise (caller 302s to /authz).
 * A JWKS outage on a cold cache surfaces as null too — we'd rather bounce the
 * viewer to the exchange than serve protected bytes on a verification we could
 * not complete.
 */
export async function authorizeGated(
  request: Request,
  route: RouteValue,
  url: URL,
  cfg: GatedConfig,
  fetchImpl: FetchLike,
  now?: number,
): Promise<EdgeClaims | null> {
  const token = readEdgeCookie(request);
  if (token === null) return null;
  return verifyTokenSafely({
    token,
    host: url.host,
    siteId: route.site_id,
    expectedMode: route.access_mode,
    jwksUrl: cfg.jwksUrl,
    fetchImpl,
    now,
  });
}

/** verifyEdgeToken wrapper that turns a JWKS-outage throw into a null (fail closed). */
async function verifyTokenSafely(p: {
  token: string;
  host: string;
  siteId: string;
  expectedMode?: string;
  jwksUrl: string;
  fetchImpl: FetchLike;
  now?: number;
}): Promise<EdgeClaims | null> {
  try {
    return await verifyEdgeToken(p);
  } catch {
    return null;
  }
}

/** Wrap a gated success response (built by the caller) with no-store headers. */
export function asPrivate(headers: Headers): Headers {
  // The caller's content headers (Content-Type, etc.) are preserved; we OVERRIDE
  // any caching directive so a gated body can never enter a shared cache.
  for (const [k, v] of Object.entries(securityHeaders())) {
    if (v !== "") headers.set(k, v);
  }
  headers.set("Cache-Control", "private, no-store, max-age=0, must-revalidate");
  headers.set("Vary", "Cookie");
  return headers;
}
