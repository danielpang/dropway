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
//
// KNOWN LIMITATION (dismiss only): if a tenant page ships its OWN stricter CSP via a
// <meta http-equiv="Content-Security-Policy"> that forbids inline scripts, the
// browser intersects it with our response CSP and blocks the inline onclick + the
// bootstrap <script>. The banner still RENDERS (the ad is intact); only the dismiss
// button and the remembered-dismissal stop working on those pages. We deliberately
// do NOT parse/rewrite a tenant's meta CSP, so this is accepted, not worked around.

import type { RouteValue } from "./route";
import { isHtml } from "./http";

/** The plan tier that gets the attribution banner (compared case/space-insensitively). */
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

/**
 * UTF-8 byte length of the banner markup. injectBanner only ever INSERTS this markup
 * (it never removes bytes), and the inject path runs only on UTF-8 content (see
 * isInjectableContentType), where a decode→re-encode round-trip is byte-stable. So
 * the injected body's Content-Length is exactly `originalLength + BANNER_BYTE_LENGTH`
 * — which lets a HEAD request report the right length WITHOUT buffering the body.
 * BANNER_MARKUP is pure ASCII, so its byte length equals its string length, but we
 * encode to be exact and future-proof.
 */
export const BANNER_BYTE_LENGTH = new TextEncoder().encode(BANNER_MARKUP).length;

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
 *
 * The tier compare is case/whitespace-insensitive: plan_tier is a free-form contract
 * string written by the Go API, so we normalize ("FREE", " free " → "free") rather
 * than relying on an exact byte match that a casing/whitespace drift could silently
 * break (the banner is a revenue lever — fail toward the documented "free" intent).
 */
export function shouldInjectBanner(
  env: { ATTRIBUTION_BANNER?: string },
  route: RouteValue,
  servedPath: string,
): boolean {
  return (
    bannerEnabled(env.ATTRIBUTION_BANNER) &&
    route.plan_tier?.trim().toLowerCase() === FREE_TIER &&
    isHtml(servedPath)
  );
}

/**
 * Whether a response body can be safely buffered, banner-injected, and re-encoded.
 * Injection decodes the bytes as UTF-8 (`Response.text()`) and re-encodes them, which
 * would corrupt a non-UTF-8 document (e.g. `charset=shift_jis` / `iso-8859-1`) into
 * U+FFFD mojibake. So we inject ONLY when the Content-Type declares UTF-8/ASCII or
 * declares no charset at all (our served HTML defaults to UTF-8). Any other explicit
 * charset → skip injection and stream the original bytes untouched.
 */
export function isInjectableContentType(contentType: string | undefined): boolean {
  if (!contentType) return true;
  const m = /charset=([^;]+)/i.exec(contentType);
  if (!m || m[1] === undefined) return true; // no explicit charset → treat as UTF-8
  const cs = m[1].trim().toLowerCase().replace(/^["']|["']$/g, "");
  return cs === "utf-8" || cs === "utf8" || cs === "us-ascii" || cs === "ascii";
}

/**
 * Insert the banner markup immediately after the opening <body> tag. Robust to:
 *   - a '>' inside an attribute value (e.g. <body onload="if(a>b)f()"> ) — the tag
 *     end is found by honoring single/double quotes, not the first raw '>';
 *   - a literal "<body…>" inside an HTML comment — comments are skipped, so the
 *     banner lands after the REAL body tag, never inside a comment;
 *   - no <body> at all (a fragment / malformed page) → prepend the banner.
 * Pure + synchronous so it is trivially unit-testable.
 */
export function injectBanner(html: string): string {
  const at = bodyOpenEnd(html);
  if (at === -1) return BANNER_MARKUP + html;
  return html.slice(0, at) + BANNER_MARKUP + html.slice(at);
}

/**
 * Index just past the '>' that closes the first real <body …> opening tag, or -1 if
 * there is none (or the document is truncated mid-tag/comment). Skips HTML comments
 * and honors quoted attribute values so a '>' inside an attribute doesn't end the tag
 * early.
 */
function bodyOpenEnd(html: string): number {
  const lower = html.toLowerCase();
  let i = 0;
  while (i < lower.length) {
    if (lower.startsWith("<!--", i)) {
      const end = lower.indexOf("-->", i + 4);
      if (end === -1) return -1; // unterminated comment → no safe insertion point
      i = end + 3;
      continue;
    }
    if (lower.startsWith("<body", i)) {
      // Confirm a real tag start: next char ends the name (`>`, `/`, whitespace) or
      // the tag is truncated at EOF. Guards against <bodyfoo> / <body-x>.
      const after = lower[i + 5];
      if (after === undefined || after === ">" || after === "/" || /\s/.test(after)) {
        return tagEnd(html, i);
      }
    }
    i++;
  }
  return -1;
}

/**
 * Index just past the '>' that closes the tag starting at `start`, treating any '>'
 * inside a single- or double-quoted attribute value as ordinary text. Returns -1 for
 * a tag truncated before its closing '>'.
 */
function tagEnd(html: string, start: number): number {
  let quote: string | null = null;
  for (let j = start; j < html.length; j++) {
    const c = html[j];
    if (quote !== null) {
      if (c === quote) quote = null;
    } else if (c === '"' || c === "'") {
      quote = c;
    } else if (c === ">") {
      return j + 1;
    }
  }
  return -1; // unterminated tag
}
