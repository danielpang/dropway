// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it, vi } from "vitest";

import {
  nextRedirectUris,
  parseLoopbackRedirect,
  redirectToRegister,
  registerLoopbackAuthorizeRedirect,
} from "@/lib/oauth-loopback-redirect";

const LOCAL = "http://localhost:64774/callback";
const LOCAL_OTHER_PORT = "http://localhost:1111/callback";
const IP = "http://127.0.0.1:64774/callback";
const IP_OTHER_PORT = "http://127.0.0.1:2222/callback";
const CHATGPT = "https://chatgpt.com/connector/oauth/cb_123";

describe("parseLoopbackRedirect", () => {
  it("accepts localhost and 127.0.0.1 on /callback over http", () => {
    expect(parseLoopbackRedirect(LOCAL)).toEqual({
      host: "localhost",
      pathname: "/callback",
    });
    expect(parseLoopbackRedirect(IP)).toEqual({
      host: "127.0.0.1",
      pathname: "/callback",
    });
  });

  it("rejects non-loopback and non-callback paths", () => {
    for (const uri of [
      "https://localhost:1/callback",
      "http://localhost:1/other",
      "http://evil.com/callback",
      `${LOCAL}?x=1`,
      `${LOCAL}#frag`,
    ]) {
      expect(parseLoopbackRedirect(uri), uri).toBeNull();
    }
  });
});

describe("redirectToRegister", () => {
  it("adds a new localhost port when another loopback /callback is registered", () => {
    expect(redirectToRegister(LOCAL, [LOCAL_OTHER_PORT])).toBe(LOCAL);
    expect(redirectToRegister(LOCAL, [IP_OTHER_PORT])).toBe(LOCAL);
  });

  it("adds localhost when the client registered 127.0.0.1 (and vice versa)", () => {
    expect(redirectToRegister(LOCAL, [IP])).toBe(LOCAL);
    expect(redirectToRegister(IP, [LOCAL_OTHER_PORT])).toBe(IP);
  });

  it("leaves an exact match unchanged", () => {
    expect(redirectToRegister(LOCAL, [LOCAL])).toBeNull();
  });

  it("refuses loopback when the client never registered one", () => {
    expect(redirectToRegister(LOCAL, [CHATGPT])).toBeNull();
    expect(redirectToRegister(LOCAL, [])).toBeNull();
  });
});

describe("nextRedirectUris", () => {
  it("replaces prior loopback entries for the same path", () => {
    expect(nextRedirectUris(LOCAL, [LOCAL_OTHER_PORT, CHATGPT])).toEqual([
      CHATGPT,
      LOCAL,
    ]);
  });
});

describe("registerLoopbackAuthorizeRedirect", () => {
  it("updates redirectUris for a cached client_id on a new loopback port", async () => {
    const updateRedirects = vi.fn(async () => undefined);
    const result = await registerLoopbackAuthorizeRedirect({
      clientId: "client",
      redirectUri: LOCAL,
      findClient: async () => ({ redirectUris: [LOCAL_OTHER_PORT] }),
      updateRedirects,
    });
    expect(result).toBe("updated");
    expect(updateRedirects).toHaveBeenCalledWith("client", [LOCAL]);
  });

  it("does not touch ChatGPT-only clients", async () => {
    const updateRedirects = vi.fn();
    const result = await registerLoopbackAuthorizeRedirect({
      clientId: "client",
      redirectUri: LOCAL,
      findClient: async () => ({ redirectUris: [CHATGPT] }),
      updateRedirects,
    });
    expect(result).toBe("unchanged");
    expect(updateRedirects).not.toHaveBeenCalled();
  });
});
