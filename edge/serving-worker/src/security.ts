// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Content-security response headers for EVERY response the Worker emits — served
// tenant content (public + gated) AND the platform pages (404, link-expired,
// 429, account-suspended). This is the Phase-4 edge hardening surface
// (docs/ARCHITECTURE.md §10, "[MEDIUM] CSP / block service-worker registration
// on content origins").
//
// IMPORTANT framing (§10): CSP is NOT the tenant-isolation control here — the
// separate PSL content domain (`*.shippedusercontent.com`) is. Hostile tenant JS
// is already firewalled from the `app.shipped.app` session by domain separation;
// these headers are DEFENSE IN DEPTH that (a) stop MIME-confusion and referrer
// leaks, (b) keep a tenant page from being framed by a sibling, (c) block a
// tenant from installing a service worker that could persist on the content
// origin, and (d) give a conservative cross-origin isolation posture.
//
// Two header sets, both built from one shared base:
//   - contentSecurityHeaders() — applied to served TENANT bytes. The CSP here is
//     deliberately PERMISSIVE enough that an ordinary static site (its own HTML,
//     CSS, JS, images, fonts, inline styles/scripts, XHR/fetch back to itself)
//     keeps working, while still denying the dangerous primitives.
//   - platformSecurityHeaders() — applied to platform-owned pages we fully
//     control (404/410/429/suspended). These get a STRICT, self-only CSP because
//     they ship no third-party or tenant content.

/**
 * Default Content-Security-Policy for served TENANT content.
 *
 * Design goals (documented so a per-site override later is an informed choice):
 *  - `default-src 'self'` — a site's resources load from its own origin by
 *    default; cross-origin loads must be explicit (we widen the obvious media
 *    classes below so typical static sites don't break).
 *  - `script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:` — static sites and
 *    client-side React bundles frequently inline a bootstrap script and (for dev
 *    or wasm-glue) use eval/blob workers. We ALLOW these: CSP is not the
 *    isolation boundary, so we optimize for "static sites just work" over a
 *    strict script policy. A per-site stricter CSP can be layered in later.
 *  - `style-src 'self' 'unsafe-inline'` — inline styles are ubiquitous.
 *  - `img/font/media-src` widened to `data:`/`blob:`/`https:` — covers data-URI
 *    images, blob object URLs, and CDN-hosted assets.
 *  - `connect-src 'self' https:` — XHR/fetch/websocket to self + any https API.
 *  - `frame-ancestors 'none'` — this tenant page may NOT be embedded by anyone
 *    (clickjacking / UI-redress defense; the modern replacement for
 *    X-Frame-Options, which we also still send for old browsers).
 *  - `base-uri 'self'` + `form-action 'self'` — a tenant page can't retarget
 *    relative URLs or post a form to an attacker origin.
 *  - `object-src 'none'` — no Flash/`<object>` plugin surface.
 *
 * NOTE: we intentionally do NOT set `upgrade-insecure-requests` or `sandbox`
 * (sandbox would break same-origin static JS). The serving Worker always runs
 * over HTTPS so mixed content is already minimized.
 */
export const CONTENT_CSP =
  "default-src 'self'; " +
  "script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:; " +
  "style-src 'self' 'unsafe-inline'; " +
  "img-src 'self' data: blob: https:; " +
  "font-src 'self' data: https:; " +
  "media-src 'self' data: blob: https:; " +
  "connect-src 'self' https:; " +
  "frame-ancestors 'none'; " +
  "base-uri 'self'; " +
  "form-action 'self'; " +
  "object-src 'none'";

/**
 * STRICT CSP for platform-owned pages (404/410/429/suspended). They carry only
 * our own minimal inline HTML/CSS, so we lock everything to `'self'` + the inline
 * style we ship, deny all scripts, and forbid framing.
 */
export const PLATFORM_CSP =
  "default-src 'none'; " +
  "style-src 'unsafe-inline'; " +
  "img-src 'self' data:; " +
  "frame-ancestors 'none'; " +
  "base-uri 'none'; " +
  "form-action 'none'";

/**
 * Headers common to BOTH content and platform responses (the always-on baseline):
 *  - `X-Content-Type-Options: nosniff` — never MIME-sniff untrusted tenant bytes.
 *  - `Referrer-Policy: no-referrer` — never leak the (possibly unguessable)
 *    content URL to a third party (§10 HIGH: hashed/preview URLs only hide).
 *  - `X-Frame-Options: DENY` — legacy clickjacking defense (CSP frame-ancestors
 *    is the modern one; both are sent).
 *  - `Cross-Origin-Opener-Policy: same-origin` — a tenant window can't get a
 *    handle to (or be opened with a usable opener of) a cross-origin window,
 *    closing window.opener tab-nabbing and cross-origin popup channels.
 *  - `Cross-Origin-Resource-Policy: same-site` — another ORIGIN can't embed this
 *    resource as a subresource. `same-site` (not `same-origin`) is deliberate:
 *    sibling tenant hosts share the registrable site, and a site legitimately
 *    loads its OWN subdomain assets; cross-SITE hotlinking/embedding is blocked.
 *  - Service-worker block: see serviceWorkerBlockHeaders().
 */
function baseSecurityHeaders(): Record<string, string> {
  return {
    "X-Content-Type-Options": "nosniff",
    "Referrer-Policy": "no-referrer",
    "X-Frame-Options": "DENY",
    "Cross-Origin-Opener-Policy": "same-origin",
    "Cross-Origin-Resource-Policy": "same-site",
    ...serviceWorkerBlockHeaders(),
  };
}

/**
 * Block service-worker registration on the content origin (§10 MEDIUM).
 *
 * A registered SW on a tenant host could persist arbitrary tenant JS across
 * navigations, intercept fetches, and survive a takedown — so we deny it in two
 * complementary ways:
 *  1. `Service-Worker-Allowed` is NOT widened — the platform never serves a SW
 *     script from a privileged scope, and (see index.ts) a request for the
 *     conventional SW path is refused outright.
 *  2. The conventional registration entrypoints are denied at the router: a
 *     request whose path looks like a service-worker script is 404'd with these
 *     headers, so `navigator.serviceWorker.register('/sw.js')` cannot fetch a
 *     scriptable body from us.
 *
 * (`Service-Worker-Allowed: ""` would scope any SW to nothing; we omit it on
 * content so we never even hint a SW is registrable, and rely on the router
 * refusal — see isServiceWorkerScript.)
 */
export function serviceWorkerBlockHeaders(): Record<string, string> {
  // No `Service-Worker-Allowed` widening header is emitted on content. We expose
  // this seam so a future per-site policy could attach one if a site opts in.
  return {};
}

/**
 * The conventional file names a browser will accept as a service-worker script
 * registered at the site root. We refuse to serve a SCRIPTABLE body at these
 * paths so a tenant can never register a SW on its content host (§10). Matching
 * is on the final path segment, case-insensitively.
 */
const SERVICE_WORKER_SCRIPTS = new Set([
  "sw.js",
  "service-worker.js",
  "serviceworker.js",
  "service-worker.min.js",
  "sw.min.js",
  "firebase-messaging-sw.js",
  "ngsw-worker.js",
  "workbox-sw.js",
]);

/**
 * True when a (cleaned, prefix-relative) request path targets a conventional
 * service-worker script. The Worker refuses to serve these (returns the platform
 * 404 instead of a scriptable body), blocking SW registration on content origins.
 *
 * We match the well-known file names rather than every `.js` so ordinary site
 * scripts still serve; a SW registered at a non-root scope still cannot control
 * the page above its scope, and the common-case registrations all use these
 * names at the root.
 */
export function isServiceWorkerScript(cleanRelPath: string): boolean {
  const last = cleanRelPath.split("/").pop() ?? "";
  return SERVICE_WORKER_SCRIPTS.has(last.toLowerCase());
}

/**
 * Security headers for SERVED TENANT CONTENT (public + gated). The base headers
 * plus the permissive-but-safe content CSP. Returned as a plain record so callers
 * can spread it into an existing Headers object.
 */
export function contentSecurityHeaders(): Record<string, string> {
  return {
    ...baseSecurityHeaders(),
    "Content-Security-Policy": CONTENT_CSP,
  };
}

/**
 * Security headers for PLATFORM-OWNED pages (404/410/429/suspended). The base
 * headers plus the strict self-only CSP.
 */
export function platformSecurityHeaders(): Record<string, string> {
  return {
    ...baseSecurityHeaders(),
    "Content-Security-Policy": PLATFORM_CSP,
  };
}

/** Apply a header record to a Headers object (skipping empty values). */
export function applyHeaders(h: Headers, record: Record<string, string>): Headers {
  for (const [k, v] of Object.entries(record)) {
    if (v !== "") h.set(k, v);
  }
  return h;
}
