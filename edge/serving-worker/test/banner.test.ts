// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the pure attribution-banner helpers (no edge/KV/R2). The
// end-to-end serve() behavior (injection, caching, tier/flag gating) is covered
// in serve.test.ts.

import { describe, expect, it } from "vitest";

import {
  BANNER_MARKUP,
  bannerEnabled,
  injectBanner,
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

  it("false for non-HTML assets", () => {
    expect(shouldInjectBanner(enabled, FREE_ROUTE, "style.css")).toBe(false);
    expect(shouldInjectBanner(enabled, FREE_ROUTE, "app.js")).toBe(false);
    expect(shouldInjectBanner(enabled, FREE_ROUTE, "logo.png")).toBe(false);
  });
});
