// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";

import {
  firstQueryValue,
  oauthErrorPresentation,
} from "@/lib/oauth-error-page";

describe("oauthErrorPresentation", () => {
  it("explains invalid_redirect and tells the user to retry from ChatGPT", () => {
    const copy = oauthErrorPresentation("invalid_redirect");
    expect(copy.title).toMatch(/callback/i);
    expect(copy.body).toContain("connector_platform_oauth_redirect");
    expect(copy.hint).toMatch(/ChatGPT/);
  });

  it("has a fallback for sign-in errors that share this page", () => {
    const copy = oauthErrorPresentation(undefined);
    expect(copy.title.length).toBeGreaterThan(0);
    expect(copy.body.length).toBeGreaterThan(0);
    expect(copy.hint.length).toBeGreaterThan(0);
  });
});

describe("firstQueryValue", () => {
  it("reads the first non-empty value and caps length", () => {
    expect(firstQueryValue(["", "invalid_redirect"])).toBe("invalid_redirect");
    expect(firstQueryValue("  invalid_request  ")).toBe("invalid_request");
    expect(firstQueryValue(undefined)).toBeUndefined();
    expect(firstQueryValue("x".repeat(500))).toHaveLength(300);
  });
});
