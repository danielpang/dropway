// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it, vi } from "vitest";

import {
  coerceRedirectUris,
  nextRedirectUris,
  parseChatgptRedirect,
  redirectToRegister,
  registerChatgptAuthorizeRedirect,
} from "@/lib/oauth-chatgpt-redirect";

const STABLE = "https://chatgpt.com/connector_platform_oauth_redirect";
const CALLBACK = "https://chatgpt.com/connector/oauth/cb_123";
const OTHER = "https://chatgpt.com/connector/oauth/cb_other";

describe("parseChatgptRedirect", () => {
  it("accepts the stable connector callback and a single callback id", () => {
    expect(parseChatgptRedirect(STABLE)).toEqual({
      kind: "stable",
      host: "chatgpt.com",
    });
    expect(parseChatgptRedirect(CALLBACK)).toEqual({
      kind: "callback",
      host: "chatgpt.com",
      id: "cb_123",
    });
    expect(parseChatgptRedirect(`${STABLE}/`)).toEqual({
      kind: "stable",
      host: "chatgpt.com",
    });
  });

  it("rejects everything that is not a documented ChatGPT connector callback", () => {
    const rejected = [
      "https://evil.com/connector_platform_oauth_redirect",
      "https://chatgpt.com.evil.com/connector_platform_oauth_redirect",
      "https://chatgpt.com/connector/oauth/../../evil",
      "https://chatgpt.com/connector/oauth/cb_123/extra",
      "https://chatgpt.com/connector/oauth/cb.123",
      "https://chatgpt.com/some/other/path",
      "http://chatgpt.com/connector_platform_oauth_redirect",
      "https://user:pass@chatgpt.com/connector_platform_oauth_redirect",
      "https://chatgpt.com:8443/connector_platform_oauth_redirect",
      `${STABLE}?next=https://evil.com`,
      `${STABLE}#fragment`,
      "https://platform.openai.com/apps-manage/oauth",
      "javascript:alert(1)",
      "https://chat.openai.com/aip/oauth/callback",
      "not a url",
    ];
    for (const uri of rejected) {
      expect(parseChatgptRedirect(uri), uri).toBeNull();
    }
  });
});

describe("redirectToRegister", () => {
  it("appends the stable URI when the client registered a same-host callback id", () => {
    expect(redirectToRegister(STABLE, [CALLBACK])).toBe(STABLE);
  });

  it("appends one callback id when the client registered the stable URI", () => {
    expect(redirectToRegister(CALLBACK, [STABLE])).toBe(CALLBACK);
  });

  it("appends the exact requested string when only a trailing slash differs", () => {
    expect(redirectToRegister(`${STABLE}/`, [STABLE])).toBe(`${STABLE}/`);
    expect(nextRedirectUris(`${STABLE}/`, [STABLE])).toEqual([STABLE, `${STABLE}/`]);
  });

  it("leaves an exact match unchanged", () => {
    expect(redirectToRegister(STABLE, [STABLE])).toBeNull();
    expect(redirectToRegister(CALLBACK, [CALLBACK, STABLE])).toBeNull();
    expect(nextRedirectUris(STABLE, [STABLE])).toBeNull();
  });

  it("refuses a second distinct callback id", () => {
    expect(redirectToRegister(OTHER, [CALLBACK])).toBeNull();
    expect(redirectToRegister(OTHER, [STABLE, CALLBACK])).toBeNull();
  });

  it("refuses a ChatGPT callback when the client never registered one on that host", () => {
    expect(redirectToRegister(STABLE, ["http://127.0.0.1:9999/callback"])).toBeNull();
    expect(
      redirectToRegister(STABLE, ["https://chat.openai.com/connector_platform_oauth_redirect"]),
    ).toBeNull();
    expect(redirectToRegister("https://evil.com/steal", [STABLE])).toBeNull();
  });

  it("accepts the same paths on chat.openai.com only against that host", () => {
    const legacy = "https://chat.openai.com/connector/oauth/cb_123";
    const legacyStable = "https://chat.openai.com/connector_platform_oauth_redirect";
    expect(redirectToRegister(legacy, [legacyStable])).toBe(legacy);
    expect(redirectToRegister(legacy, [STABLE])).toBeNull();
  });
});

describe("coerceRedirectUris", () => {
  it("reads arrays and one level of JSON text", () => {
    expect(coerceRedirectUris([STABLE])).toEqual([STABLE]);
    expect(coerceRedirectUris(JSON.stringify([STABLE, CALLBACK]))).toEqual([
      STABLE,
      CALLBACK,
    ]);
    expect(coerceRedirectUris(null)).toEqual([]);
    expect(coerceRedirectUris(JSON.stringify("nope"))).toBeNull();
    expect(coerceRedirectUris([1, STABLE])).toBeNull();
  });
});

describe("registerChatgptAuthorizeRedirect", () => {
  it("updates the client with the requested ChatGPT callback", async () => {
    const updateRedirects = vi.fn(async () => undefined);
    const findClient = vi.fn(async () => ({ redirectUris: [STABLE] }));
    const result = await registerChatgptAuthorizeRedirect({
      clientId: "client",
      redirectUri: CALLBACK,
      findClient,
      updateRedirects,
    });
    expect(result).toBe("updated");
    expect(updateRedirects).toHaveBeenCalledWith("client", [STABLE, CALLBACK]);
  });

  it("does not read the database for a non-ChatGPT redirect", async () => {
    const findClient = vi.fn();
    const updateRedirects = vi.fn();
    const result = await registerChatgptAuthorizeRedirect({
      clientId: "client",
      redirectUri: "http://127.0.0.1:9999/callback",
      findClient,
      updateRedirects,
    });
    expect(result).toBe("skipped");
    expect(findClient).not.toHaveBeenCalled();
    expect(updateRedirects).not.toHaveBeenCalled();
  });

  it("does not write when the callback is already registered or is a second id", async () => {
    const updateRedirects = vi.fn();
    expect(
      await registerChatgptAuthorizeRedirect({
        clientId: "client",
        redirectUri: STABLE,
        findClient: async () => ({ redirectUris: JSON.stringify([STABLE]) }),
        updateRedirects,
      }),
    ).toBe("unchanged");
    expect(
      await registerChatgptAuthorizeRedirect({
        clientId: "client",
        redirectUri: OTHER,
        findClient: async () => ({ redirectUris: [CALLBACK] }),
        updateRedirects,
      }),
    ).toBe("unchanged");
    expect(updateRedirects).not.toHaveBeenCalled();
  });

  it("skips a missing client without writing", async () => {
    const updateRedirects = vi.fn();
    const result = await registerChatgptAuthorizeRedirect({
      clientId: "missing",
      redirectUri: STABLE,
      findClient: async () => null,
      updateRedirects,
    });
    expect(result).toBe("skipped");
    expect(updateRedirects).not.toHaveBeenCalled();
  });
});
