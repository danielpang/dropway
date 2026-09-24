// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it, vi } from "vitest";

import {
  loopbackRedirectToRegister,
  nextLoopbackRedirectUris,
  parseLoopbackRedirect,
  registerLoopbackAuthorizeRedirect,
} from "@/lib/oauth-loopback-redirect";

const LOCAL = "http://localhost:64774/callback";
const IP = "http://127.0.0.1:3118/callback";
const V6 = "http://[::1]:3118/callback";

describe("parseLoopbackRedirect", () => {
  it("accepts localhost, 127.0.0.1, and ::1 callbacks", () => {
    expect(parseLoopbackRedirect(LOCAL)?.path).toBe("/callback");
    expect(parseLoopbackRedirect(IP)?.path).toBe("/callback");
    expect(parseLoopbackRedirect(V6)?.path).toBe("/callback");
    expect(parseLoopbackRedirect("http://127.0.0.2:1/callback")?.path).toBe("/callback");
  });

  it("rejects remote hosts, https, query strings, and non-canonical forms", () => {
    const rejected = [
      "https://chatgpt.com/connector_platform_oauth_redirect",
      "https://localhost:64774/callback",
      "http://localhost.evil.com/callback",
      "http://evil.com/callback",
      "http://127.0.0.1.evil.com/callback",
      "http://user:pass@127.0.0.1:1/callback",
      `${LOCAL}?next=https://evil.com`,
      `${LOCAL}#fragment`,
      "http://localhost:64774/./callback",
      "http://localhost:64774/callback/../admin",
      "http://127.999.0.1/callback",
      "http://127.0.0.01/callback",
      "not a url",
    ];
    for (const uri of rejected) {
      expect(parseLoopbackRedirect(uri), uri).toBeNull();
    }
  });
});

describe("loopbackRedirectToRegister", () => {
  it("appends localhost when the client registered the same path on 127.0.0.1", () => {
    expect(loopbackRedirectToRegister(LOCAL, [IP])).toBe(LOCAL);
    expect(nextLoopbackRedirectUris(LOCAL, [IP])).toEqual([IP, LOCAL]);
  });

  it("appends a different localhost port and an IPv6 sibling", () => {
    expect(loopbackRedirectToRegister("http://localhost:9/callback", [LOCAL])).toBe(
      "http://localhost:9/callback",
    );
    expect(loopbackRedirectToRegister(V6, [LOCAL])).toBe(V6);
  });

  it("treats one trailing slash as the same path but stores the requested string", () => {
    expect(loopbackRedirectToRegister(`${LOCAL}/`, [IP])).toBe(`${LOCAL}/`);
  });

  it("leaves an exact match unchanged", () => {
    expect(loopbackRedirectToRegister(LOCAL, [LOCAL])).toBeNull();
    expect(nextLoopbackRedirectUris(LOCAL, [IP, LOCAL])).toBeNull();
  });

  it("refuses a different path, even on loopback", () => {
    expect(loopbackRedirectToRegister("http://localhost:1/oauth/callback", [IP])).toBeNull();
  });

  it("refuses a loopback redirect when the client never registered one", () => {
    expect(
      loopbackRedirectToRegister(LOCAL, [
        "https://chatgpt.com/connector_platform_oauth_redirect",
      ]),
    ).toBeNull();
    expect(loopbackRedirectToRegister("https://evil.com/callback", [IP])).toBeNull();
  });
});

describe("registerLoopbackAuthorizeRedirect", () => {
  it("updates the client with the requested loopback sibling", async () => {
    const updateRedirects = vi.fn(async () => undefined);
    const result = await registerLoopbackAuthorizeRedirect({
      clientId: "client",
      redirectUri: LOCAL,
      findClient: async () => ({ redirectUris: [IP] }),
      updateRedirects,
    });
    expect(result).toBe("updated");
    expect(updateRedirects).toHaveBeenCalledWith("client", [IP, LOCAL]);
  });

  it("does not read the database for a remote redirect", async () => {
    const findClient = vi.fn();
    const result = await registerLoopbackAuthorizeRedirect({
      clientId: "client",
      redirectUri: "https://chatgpt.com/connector_platform_oauth_redirect",
      findClient,
      updateRedirects: vi.fn(),
    });
    expect(result).toBe("skipped");
    expect(findClient).not.toHaveBeenCalled();
  });

  it("does not attach loopback to a ChatGPT-only client", async () => {
    const updateRedirects = vi.fn();
    const result = await registerLoopbackAuthorizeRedirect({
      clientId: "client",
      redirectUri: LOCAL,
      findClient: async () => ({
        redirectUris: ["https://chatgpt.com/connector/oauth/cb_123"],
      }),
      updateRedirects,
    });
    expect(result).toBe("unchanged");
    expect(updateRedirects).not.toHaveBeenCalled();
  });
});
