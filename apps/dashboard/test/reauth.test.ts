// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The re-auth URL must stay same-site and must force the sign-in form. Without
// reauth=1, a session cookie that outlived the rejected JWT bounces straight
// back to the page that 401'd.

import { describe, expect, it } from "vitest";

import {
  authScreenSwitchHref,
  isReauthRequest,
  signInPathForReauth,
} from "@/lib/reauth";

describe("signInPathForReauth", () => {
  it("sends the viewer to sign-in with a same-site callback and reauth=1", () => {
    expect(signInPathForReauth("/sites/cb04375f-f22b-4e0b-bb27-a17e42b3c850")).toBe(
      "/sign-in?callbackURL=%2Fsites%2Fcb04375f-f22b-4e0b-bb27-a17e42b3c850&reauth=1",
    );
  });

  it("rejects an off-site return path", () => {
    expect(signInPathForReauth("https://evil.example/phish")).toBe(
      "/sign-in?callbackURL=%2F&reauth=1",
    );
    expect(signInPathForReauth("//evil.example")).toBe(
      "/sign-in?callbackURL=%2F&reauth=1",
    );
  });
});

describe("isReauthRequest", () => {
  it("accepts only the exact flag the redirect sets", () => {
    expect(isReauthRequest("1")).toBe(true);
    expect(isReauthRequest("true")).toBe(false);
    expect(isReauthRequest(undefined)).toBe(false);
    expect(isReauthRequest(["1"])).toBe(false);
  });
});

describe("authScreenSwitchHref", () => {
  it("keeps reauth on the sign-up link when a callback is set", () => {
    expect(authScreenSwitchHref("sign-up", "/sites/abc", true)).toBe(
      "/sign-up?callbackURL=%2Fsites%2Fabc&reauth=1",
    );
  });

  it("omits the default callback but still carries reauth", () => {
    expect(authScreenSwitchHref("sign-in", "/dashboard", true)).toBe("/sign-in?reauth=1");
  });

  it("is a bare path when there is nothing to preserve", () => {
    expect(authScreenSwitchHref("sign-up", "/dashboard", false)).toBe("/sign-up");
  });
});
