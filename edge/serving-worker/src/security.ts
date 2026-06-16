// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Content-security response headers for EVERY response the Worker emits — served
// tenant content (public + gated) AND the platform pages (404, link-expired,
// 429, account-suspended). This is the Phase-4 edge hardening surface
// (docs/ARCHITECTURE.md §10, "[MEDIUM] CSP / block service-worker registration
// on content origins").
//
// IMPORTANT framing (§10): CSP is NOT the tenant-isolation control here — the
// separate PSL content domain (`*.dropwaycontent.com`) is. Hostile tenant JS
// is already firewalled from the `app.dropway.dev` session by domain separation;
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
 *  - `script-src 'self' 'unsafe-inline' 'unsafe-eval' blob: https:` — static sites
 *    and client-side React bundles frequently inline a bootstrap script, use
 *    eval/blob workers, AND pull libraries from a CDN (Three.js, htmx, etc.). We
 *    ALLOW these: CSP is not the isolation boundary, so we optimize for "static
 *    sites just work" over a strict script policy. A per-site stricter CSP can be
 *    layered in later. (`https:` makes blocking only external <script src> moot,
 *    given 'unsafe-inline' + connect-src https: are already permitted.)
 *  - `style-src 'self' 'unsafe-inline' https:` — inline styles are ubiquitous and
 *    web fonts / CDN stylesheets (Google Fonts, etc.) are the common case.
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
  "script-src 'self' 'unsafe-inline' 'unsafe-eval' blob: https:; " +
  "style-src 'self' 'unsafe-inline' https:; " +
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
 *
 * Service-worker registration is blocked at the ROUTER (isServiceWorkerRequest +
 * isServiceWorkerScript), not by a response header — there is no header that
 * reliably does it (see those functions).
 */
function baseSecurityHeaders(): Record<string, string> {
  return {
    "X-Content-Type-Options": "nosniff",
    "Referrer-Policy": "no-referrer",
    "X-Frame-Options": "DENY",
    "Cross-Origin-Opener-Policy": "same-origin",
    "Cross-Origin-Resource-Policy": "same-site",
  };
}

/**
 * Block service-worker registration on the content origin (§10 MEDIUM).
 *
 * A registered SW on a tenant host could persist arbitrary tenant JS across
 * navigations, intercept fetches, and survive a takedown — so we deny it in two
 * complementary, PATH-INDEPENDENT ways (the previous header-only seam was a no-op):
 *  1. isServiceWorkerRequest() — every SW-script fetch (registration AND update)
 *     carries the request header `Service-Worker: script`. The router refuses ANY
 *     such request (platform 404), so a tenant cannot register a SW under ANY path
 *     — including non-conventional names like `/assets/app-worker.js` that the
 *     filename list below would miss.
 *  2. isServiceWorkerScript() — the conventional SW file names (sw.js, …) are
 *     additionally 404'd by name, as belt-and-suspenders for any client/runtime
 *     that omits the request header.
 *
 * There is no response header that reliably blocks registration (`Service-Worker-
 * Allowed` only WIDENS scope; CSP `worker-src` would also break legitimate Web
 * Workers), so the request-header + filename refusals are the real controls.
 */

/**
 * True when the request is a service-worker SCRIPT fetch (registration/update).
 * The browser sets `Service-Worker: script` on exactly these; refusing them blocks
 * SW registration on the content origin regardless of the request path (§10).
 */
export function isServiceWorkerRequest(request: Request): boolean {
  return request.headers.get("Service-Worker") === "script";
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
