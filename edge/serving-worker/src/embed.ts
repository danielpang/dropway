// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Embed surface — the framable, chrome-stripped rendering of a site's top document
// requested with `?embed=1`. It exists so a Dropway site can be pasted into an
// <iframe> inside Notion, Linear, Confluence, etc. instead of only living at a URL.
//
// What the embed changes vs a normal serve (see index.ts serveEmbed):
//   1. FRAMING: the response drops X-Frame-Options and widens the CSP to
//      `frame-ancestors *` (security.ts framableContentSecurityHeaders) so any parent
//      origin may frame it. Normal serving stays unframable (clickjacking defense).
//   2. ACCESS CONTROL is fully preserved: a gated site (password/allowlist/org_only)
//      NEVER serves its bytes into an embed. Instead the embed shows a framable
//      "Sign in to view" placeholder that links out to the real (gated) site. We do
//      NOT run the /authz redirect in-frame — a login bounced through a tiny
//      cross-origin iframe is broken UX and the dashboard forbids being framed.
//   3. CHROME: the free-tier attribution banner and the "How this was made" chat pill
//      are suppressed; the embed shows only a slim "Powered by Dropway" badge, which
//      Pro+ orgs may remove with `?badge=0` (entitlement is server-authoritative — it
//      rides the KV route projection's plan_tier, so a free org can't fake it).
//
// Only the TOP document carries `?embed=1`; its subresources (CSS/JS/images) load
// from the same content origin WITHOUT the param and are served normally — framing
// headers and CORP apply to the document/subresource boundary, not to same-origin
// asset loads inside the framed document.

import type { RouteValue } from "./route";
import { isHtml } from "./http";
import { injectAfterBodyOpen, isInjectableContentType } from "./banner";
import { framablePlatformSecurityHeaders } from "./security";

/** The query parameter that opts the top document into the framable embed surface. */
export const EMBED_QUERY_PARAM = "embed";

/** The query parameter a Pro+ org uses to suppress the attribution badge. */
export const EMBED_BADGE_PARAM = "badge";

/** Plan tiers entitled to remove the embed badge ("Pro+"). Compared normalized. */
const BADGE_REMOVABLE_TIERS = new Set(["pro", "business", "enterprise"]);

/**
 * True when the request opts into embed mode — the presence of `?embed` (any value,
 * including empty: `?embed`, `?embed=1`). Mirrors the `?raw` opt-in convention.
 */
export function isEmbedRequested(url: URL): boolean {
  return url.searchParams.has(EMBED_QUERY_PARAM);
}

/** True when a plan tier is entitled to remove the embed badge (Pro / Business / Enterprise). */
export function badgeRemovable(planTier: string | undefined): boolean {
  const t = planTier?.trim().toLowerCase();
  return t !== undefined && BADGE_REMOVABLE_TIERS.has(t);
}

/** True when the request explicitly asks to suppress the badge (`?badge=0`). */
function badgeSuppressed(url: URL): boolean {
  const v = url.searchParams.get(EMBED_BADGE_PARAM);
  if (v === null) return false;
  switch (v.trim().toLowerCase()) {
    case "0":
    case "false":
    case "off":
    case "no":
      return true;
    default:
      return false;
  }
}

/**
 * Whether to inject the "Powered by Dropway" badge into this embed response.
 *
 *  - No plan_tier on the route (a self-host / legacy projection) → NO badge, matching
 *    the OSS "no attribution" posture (self-host serves via the Go service anyway).
 *  - A Pro+ org that passed `?badge=0` → suppressed (server-authoritative entitlement).
 *  - Everyone else (incl. free tier, which cannot remove it) → badge shown.
 */
export function shouldShowEmbedBadge(route: RouteValue, url: URL): boolean {
  const tier = route.plan_tier?.trim().toLowerCase();
  if (!tier) return false;
  if (badgeRemovable(tier) && badgeSuppressed(url)) return false;
  return true;
}

/**
 * The injected badge: a slim fixed pill in the bottom-right corner linking to
 * dropway.dev. All styling is inline so it never depends on the tenant stylesheet,
 * and there is no script (unlike the dismissible banner) — the badge is only removed
 * by an entitled org via `?badge=0`, never client-side. Its id is prefixed
 * `dropway-` to avoid colliding with tenant DOM.
 */
export const EMBED_BADGE_MARKUP =
  '<a id="dropway-embed-badge" href="https://dropway.dev" target="_blank" rel="noopener noreferrer" ' +
  'aria-label="Powered by Dropway" ' +
  "style=\"position:fixed;bottom:8px;right:8px;z-index:2147483647;box-sizing:border-box;" +
  "display:inline-flex;align-items:center;gap:5px;padding:4px 9px;border-radius:6px;" +
  "font:11px/1 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;" +
  "font-weight:600;color:#4f46e5;background:rgba(255,255,255,0.92);" +
  'border:1px solid #e5e7eb;text-decoration:none;box-shadow:0 1px 2px rgba(0,0,0,0.08);">' +
  "Powered by Dropway</a>";

/**
 * UTF-8 byte length of the badge markup. injectEmbedBadge only INSERTS this markup
 * on the UTF-8-only inject path, so the injected body's Content-Length is exactly
 * `original + EMBED_BADGE_BYTE_LENGTH` — letting a HEAD report the right length
 * without buffering the body (same trick as banner.ts BANNER_BYTE_LENGTH).
 */
export const EMBED_BADGE_BYTE_LENGTH = new TextEncoder().encode(EMBED_BADGE_MARKUP).length;

/** Insert the badge markup immediately after the opening <body> tag (or prepend). */
export function injectEmbedBadge(html: string): string {
  return injectAfterBodyOpen(html, EMBED_BADGE_MARKUP);
}

/**
 * Whether the badge can be injected into this response: it must be an HTML document
 * with an injectable (UTF-8) content type. Mirrors banner.ts's guards.
 */
export function isInjectableEmbedBadge(servedPath: string, contentType: string | undefined): boolean {
  return isHtml(servedPath) && isInjectableContentType(contentType);
}

/**
 * The framable "Sign in to view" placeholder served when a GATED site is requested
 * in embed mode. It NEVER contains tenant content — it is a platform page that fully
 * substitutes for the private site inside the frame, with a link out to the real site
 * (new tab) where the viewer can authenticate. `no-store` so a later access change is
 * visible immediately; framable platform headers so it renders inside the iframe.
 *
 * `siteUrl` is the site's own origin root (no `?embed=1`) — clicking it opens the
 * gated site in a new tab, which runs the normal /authz sign-in flow.
 */
export function embedGatePlaceholder(siteUrl: string): Response {
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "no-store",
    ...framablePlatformSecurityHeaders(),
  });
  return new Response(renderEmbedGateHtml(siteUrl), { status: 200, headers });
}

/** Escape the few HTML-significant chars in the site URL before interpolating it. */
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function renderEmbedGateHtml(siteUrl: string): string {
  const href = escapeHtml(siteUrl);
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Private site</title>
<style>
  :root { color-scheme: light dark; }
  html, body { height: 100%; }
  body { margin: 0; font: 15px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI",
         Roboto, Helvetica, Arial, sans-serif;
         display: grid; place-items: center; padding: 1.5rem; }
  main { text-align: center; max-width: 22rem; }
  .lock { font-size: 1.75rem; line-height: 1; margin-bottom: .5rem; }
  h1 { font-size: 1.05rem; margin: 0 0 .35rem; }
  p { opacity: .7; margin: 0 0 1rem; font-size: .9rem; }
  a.cta { display: inline-block; padding: .5rem 1rem; border-radius: .5rem;
          background: #4f46e5; color: #fff; text-decoration: none;
          font-weight: 600; font-size: .9rem; }
</style>
</head>
<body>
  <main>
    <div class="lock" aria-hidden="true">🔒</div>
    <h1>This site is private</h1>
    <p>You need to sign in to view this content.</p>
    <a class="cta" href="${href}" target="_blank" rel="noopener noreferrer">Sign in to view</a>
  </main>
</body>
</html>
`;
}
