// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Free-tier "Deployed with Dropway" attribution banner.
//
// When enabled (the ATTRIBUTION_BANNER Worker var) the serving Worker injects a
// slim, dismissible bar at the top of every HTML page served for a FREE-tier org,
// advertising Dropway (the Dropway word links to https://dropway.dev). Paid tiers
// and non-HTML assets are never touched.
//
// The free-tier signal rides on the KV route projection (RouteValue.plan_tier,
// contract v3) written by the Go API, so this decision needs no extra read.
//
// CSP note: the banner ships inline styles, an inline onclick, and a tiny inline
// <script>. The tenant content CSP (security.ts CONTENT_CSP) already allows
// 'unsafe-inline' for both script-src and style-src, so the banner renders and the
// dismiss handler runs. Dismissal is remembered in localStorage, which is
// per-origin — each tenant site has its own content host, so a dismissal on one
// site never affects another.

import type { RouteValue } from "./route";
import { isHtml } from "./http";

/** The plan tier that gets the attribution banner. */
const FREE_TIER = "free" as const;

/** localStorage key the dismiss button sets (per content origin). */
const DISMISS_KEY = "dropway-banner-dismissed";

/**
 * The injected markup: a slim sticky top bar + a dismiss (×) button + a tiny
 * bootstrap script that hides the bar on load if the visitor previously dismissed
 * it. All styling is inline so it never depends on the tenant's stylesheet. The
 * bar uses `position: sticky` so it sits in normal flow at the top (pushing the
 * page down a little) rather than overlapping fixed content.
 */
export const BANNER_MARKUP =
  '<div id="dropway-banner" role="complementary" aria-label="Deployed with Dropway" ' +
  'style="box-sizing:border-box;position:sticky;top:0;left:0;right:0;z-index:2147483647;' +
  "display:flex;align-items:center;justify-content:center;gap:8px;width:100%;" +
  "padding:6px 36px;margin:0;" +
  "font:13px/1.4 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;" +
  'color:#1f2937;background:#f3f4f6;border-bottom:1px solid #e5e7eb;text-align:center;">' +
  "<span>Deployed with " +
  '<a href="https://dropway.dev" target="_blank" rel="noopener noreferrer" ' +
  'style="color:#4f46e5;font-weight:600;text-decoration:none;">Dropway</a></span>' +
  '<button type="button" aria-label="Dismiss" ' +
  `onclick="try{localStorage.setItem('${DISMISS_KEY}','1')}catch(e){}this.parentNode.remove()" ` +
  'style="position:absolute;right:8px;top:50%;transform:translateY(-50%);background:none;' +
  'border:0;color:#6b7280;font-size:16px;line-height:1;cursor:pointer;padding:2px 6px;">&times;</button>' +
  "</div>" +
  "<script>(function(){try{if(localStorage.getItem('" +
  DISMISS_KEY +
  "')==='1'){var b=document.getElementById('dropway-banner');if(b)b.parentNode.removeChild(b);}}catch(e){}})();</script>";

/** Truthy parse for the ATTRIBUTION_BANNER var ("true"/"1"/"yes"/"on"). */
export function bannerEnabled(flag: string | undefined): boolean {
  if (!flag) return false;
  switch (flag.trim().toLowerCase()) {
    case "1":
    case "true":
    case "yes":
    case "on":
      return true;
    default:
      return false;
  }
}

/**
 * Whether to inject the attribution banner for this response: the feature is
 * enabled AND the owning org is on the free tier AND the served document is HTML.
 * Anything else (paid/unknown tier, non-HTML asset, flag off) → false.
 */
export function shouldInjectBanner(
  env: { ATTRIBUTION_BANNER?: string },
  route: RouteValue,
  servedPath: string,
): boolean {
  return (
    bannerEnabled(env.ATTRIBUTION_BANNER) &&
    route.plan_tier === FREE_TIER &&
    isHtml(servedPath)
  );
}

/**
 * Insert the banner markup immediately after the opening <body> tag. If the
 * document has no <body> (a fragment or a malformed page), prepend it so the
 * banner is still the first thing in the output. Pure + synchronous so it is
 * trivially unit-testable.
 */
export function injectBanner(html: string): string {
  const bodyOpen = /<body[^>]*>/i;
  const m = bodyOpen.exec(html);
  if (m) {
    const at = m.index + m[0].length;
    return html.slice(0, at) + BANNER_MARKUP + html.slice(at);
  }
  return BANNER_MARKUP + html;
}
