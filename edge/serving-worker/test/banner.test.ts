// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the pure attribution-banner helpers (no edge/KV/R2). The
// end-to-end serve() behavior (injection, caching, tier/flag gating) is covered
// in serve.test.ts.

import { describe, expect, it } from "vitest";

import {
  BANNER_BYTE_LENGTH,
  BANNER_MARKUP,
  bannerEnabled,
  injectBanner,
  isInjectableContentType,
  shouldInjectBanner,
} from "../src/banner";
import type { RouteValue } from "../src/route";

const FREE_ROUTE: RouteValue = {
  org_id: "11111111-1111-1111-1111-111111111111",
  site_id: "22222222-2222-2222-2222-222222222222",
  version_id: "33333333-3333-3333-3333-333333333333",
  access_mode: "public",
  schema_version: 3,
  plan_tier: "free",
};

describe("bannerEnabled", () => {
  it("is true for the truthy spellings only", () => {
    for (const v of ["true", "TRUE", "1", "yes", "on", " true "]) {
      expect(bannerEnabled(v)).toBe(true);
    }
  });

  it("is false for unset / false-ish values", () => {
    for (const v of [undefined, "", "false", "0", "no", "off", "nope"]) {
      expect(bannerEnabled(v)).toBe(false);
    }
  });
});

describe("injectBanner", () => {
  it("inserts right after the opening <body> tag", () => {
    const out = injectBanner("<html><body class='x'><h1>hi</h1></body></html>");
    expect(out).toContain(BANNER_MARKUP);
    expect(out.indexOf("<body class='x'>")).toBeLessThan(out.indexOf("dropway-banner"));
    expect(out.indexOf("dropway-banner")).toBeLessThan(out.indexOf("<h1>hi</h1>"));
  });

  it("matches <body> case-insensitively", () => {
    const out = injectBanner("<BODY><h1>hi</h1></BODY>");
    expect(out.indexOf("dropway-banner")).toBeLessThan(out.indexOf("<h1>hi</h1>"));
    expect(out.indexOf("<BODY>")).toBeLessThan(out.indexOf("dropway-banner"));
  });

  it("prepends when there is no <body>", () => {
    const out = injectBanner("<h1>fragment</h1>");
    expect(out.startsWith(BANNER_MARKUP)).toBe(true);
    expect(out.endsWith("<h1>fragment</h1>")).toBe(true);
  });

  it("only injects once (first <body>)", () => {
    const out = injectBanner("<body></body><body></body>");
    expect(out.split("dropway-banner").length - 1).toBe(
      BANNER_MARKUP.split("dropway-banner").length - 1,
    );
  });

  it("handles a '>' inside a <body> attribute value (quote-aware)", () => {
    // The naive /<body[^>]*>/ regex would split at the '>' inside the onload
    // attribute and corrupt the tag; the quote-aware scan must find the real end.
    const html = `<body onload="if(a>b){go()}" class="x"><h1>hi</h1></body>`;
    const out = injectBanner(html);
    // The whole opening body tag is preserved intact, banner sits right after it,
    // and the tenant content is untouched and still after the banner.
    expect(out).toContain(`<body onload="if(a>b){go()}" class="x">`);
    expect(out.indexOf(`class="x">`)).toBeLessThan(out.indexOf("dropway-banner"));
    expect(out.indexOf("dropway-banner")).toBeLessThan(out.indexOf("<h1>hi</h1>"));
    // The onload handler body must not have leaked out as page text.
    expect(out).not.toMatch(/dropway-banner[\s\S]*b\)\{go\(\)\}"/);
  });

  it("handles single-quoted attributes containing '>'", () => {
    const out = injectBanner(`<body data-tpl='a>b'><p>x</p></body>`);
    expect(out).toContain(`<body data-tpl='a>b'>`);
    expect(out.indexOf(`data-tpl='a>b'>`)).toBeLessThan(out.indexOf("dropway-banner"));
  });

  it("skips a literal <body> inside an HTML comment, injecting after the real one", () => {
    const html = `<!-- example: <body class="demo"> --><body id="real"><h1>hi</h1></body>`;
    const out = injectBanner(html);
    // Banner goes after the REAL body, not inside the comment.
    expect(out.indexOf(`<body id="real">`)).toBeLessThan(out.indexOf("dropway-banner"));
    expect(out.indexOf("-->")).toBeLessThan(out.indexOf("dropway-banner"));
  });

  it("does not match <bodyfoo> (requires a real tag boundary)", () => {
    const out = injectBanner("<bodyguard>x</bodyguard>");
    // No real <body> → prepend (banner before the content).
    expect(out.startsWith(BANNER_MARKUP)).toBe(true);
  });

  it("injected length is exactly original + BANNER_BYTE_LENGTH for UTF-8 content", () => {
    const html = "<body>héllo 日本語</body>";
    const out = injectBanner(html);
    const enc = new TextEncoder();
    expect(enc.encode(out).length).toBe(enc.encode(html).length + BANNER_BYTE_LENGTH);
  });
});

describe("isInjectableContentType", () => {
  it("injects for UTF-8 / ascii / absent charset", () => {
    expect(isInjectableContentType("text/html; charset=utf-8")).toBe(true);
    expect(isInjectableContentType("text/html;charset=UTF-8")).toBe(true);
    expect(isInjectableContentType("text/html; charset=us-ascii")).toBe(true);
    expect(isInjectableContentType("text/html")).toBe(true);
    expect(isInjectableContentType(undefined)).toBe(true);
  });

  it("skips non-UTF-8 charsets (would corrupt on decode/re-encode)", () => {
    expect(isInjectableContentType("text/html; charset=iso-8859-1")).toBe(false);
    expect(isInjectableContentType("text/html; charset=shift_jis")).toBe(false);
    expect(isInjectableContentType('text/html; charset="windows-1252"')).toBe(false);
  });
});

describe("shouldInjectBanner", () => {
  const enabled = { ATTRIBUTION_BANNER: "true" };

  it("true for enabled + free + html", () => {
    expect(shouldInjectBanner(enabled, FREE_ROUTE, "index.html")).toBe(true);
    expect(shouldInjectBanner(enabled, FREE_ROUTE, "docs/page.htm")).toBe(true);
  });

  it("false when the flag is off", () => {
    expect(shouldInjectBanner({}, FREE_ROUTE, "index.html")).toBe(false);
    expect(shouldInjectBanner({ ATTRIBUTION_BANNER: "false" }, FREE_ROUTE, "index.html")).toBe(false);
  });

  it("false for a non-free / unknown tier", () => {
    expect(shouldInjectBanner(enabled, { ...FREE_ROUTE, plan_tier: "pro" }, "index.html")).toBe(false);
    expect(shouldInjectBanner(enabled, { ...FREE_ROUTE, plan_tier: undefined }, "index.html")).toBe(false);
  });

  it("normalizes the tier (case/whitespace-insensitive 'free')", () => {
    for (const tier of ["FREE", " free ", "Free"]) {
      expect(shouldInjectBanner(enabled, { ...FREE_ROUTE, plan_tier: tier }, "index.html")).toBe(true);
    }
  });

  it("false for non-HTML assets", () => {
    expect(shouldInjectBanner(enabled, FREE_ROUTE, "style.css")).toBe(false);
    expect(shouldInjectBanner(enabled, FREE_ROUTE, "app.js")).toBe(false);
    expect(shouldInjectBanner(enabled, FREE_ROUTE, "logo.png")).toBe(false);
  });
});
