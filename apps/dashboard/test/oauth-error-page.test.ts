// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";

import {
  firstQueryValue,
  oauthErrorPresentation,
  safeOAuthErrorCode,
  safeOAuthErrorDescription,
} from "@/lib/oauth-error-page";

describe("oauthErrorPresentation", () => {
  it("explains invalid_redirect and tells the user to retry the connection", () => {
    const copy = oauthErrorPresentation("invalid_redirect");
    expect(copy.title).toMatch(/callback/i);
    expect(copy.body).toMatch(/callback URL/i);
    expect(copy.hint).toMatch(/ChatGPT/);
  });

  it("does not send CLI or Google sign-in failures to ChatGPT", () => {
    for (const code of ["invalid_client", "invalid_scope", "access_denied", "server_error"]) {
      expect(oauthErrorPresentation(code).hint, code).not.toMatch(/ChatGPT/);
    }
  });

  it("has a fallback for sign-in errors that share this page", () => {
    const copy = oauthErrorPresentation(undefined);
    expect(copy.title.length).toBeGreaterThan(0);
    expect(copy.body.length).toBeGreaterThan(0);
    expect(copy.hint.length).toBeGreaterThan(0);
  });
});

describe("firstQueryValue", () => {
  it("reads the first non-empty value", () => {
    expect(firstQueryValue(["", "invalid_redirect"])).toBe("invalid_redirect");
    expect(firstQueryValue("  invalid_request  ")).toBe("invalid_request");
    expect(firstQueryValue(undefined)).toBeUndefined();
  });
});

describe("reflected OAuth error params", () => {
  it("keeps provider error codes and short descriptions", () => {
    expect(safeOAuthErrorCode("invalid_redirect")).toBe("invalid_redirect");
    expect(safeOAuthErrorDescription("invalid redirect uri")).toBe(
      "invalid redirect uri",
    );
  });

  it("drops crafted codes and descriptions", () => {
    expect(safeOAuthErrorCode("Please call support")).toBeUndefined();
    expect(
      safeOAuthErrorDescription("Session expired. Sign in at https://evil.example/login"),
    ).toBeUndefined();
    expect(safeOAuthErrorDescription("<script>alert(1)</script>")).toBeUndefined();
    expect(safeOAuthErrorDescription("x".repeat(201))).toBeUndefined();
  });
});
