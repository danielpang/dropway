// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { createHash } from "node:crypto";
import { describe, expect, it, vi } from "vitest";

import {
  authorizedCodexLoopbackRedirect,
  equivalentCodexLoopbackCallbacks,
} from "@/lib/oauth-token-loopback-redirect";

const authorized = "http://127.0.0.1:63478/callback";
const tokenRequest = "http://localhost:63478/callback";
const code = "abcdefghijklmnopqrstuvwxyzABCDEF";

function lookup(clientId = "codex", redirectUri = authorized) {
  return vi.fn(async (_identifier: string) => ({
    value: JSON.stringify({
      type: "authorization_code",
      query: { client_id: clientId, redirect_uri: redirectUri },
    }),
  }));
}

describe("equivalentCodexLoopbackCallbacks", () => {
  it("accepts only a host spelling change on the same local listener", () => {
    expect(equivalentCodexLoopbackCallbacks(authorized, tokenRequest)).toBe(true);
    expect(equivalentCodexLoopbackCallbacks(tokenRequest, authorized)).toBe(true);
    expect(equivalentCodexLoopbackCallbacks(
      `${authorized}/server_id`, `${tokenRequest}/server_id`,
    )).toBe(true);

    const rejected = [
      "http://localhost:63479/callback",
      "http://localhost:63478/other",
      "http://localhost:63478/callback/other/path",
      "http://localhost:63478/callback?next=/",
      "http://localhost:63478/callback#fragment",
      "http://localhost.evil.com:63478/callback",
      "http://127.0.0.2:63478/callback",
      "https://localhost:63478/callback",
      "http://user@localhost:63478/callback",
      "http://localhost:63478/./callback",
      authorized,
    ];
    for (const uri of rejected) {
      expect(equivalentCodexLoopbackCallbacks(authorized, uri), uri).toBe(false);
    }
  });
});

describe("authorizedCodexLoopbackRedirect", () => {
  it("restores the URI bound to the code for the same client", async () => {
    const findVerification = lookup();
    expect(await authorizedCodexLoopbackRedirect({
      clientId: "codex", code, redirectUri: tokenRequest, findVerification,
    })).toBe(authorized);
    expect(findVerification).toHaveBeenCalledWith(
      createHash("sha256").update(code).digest("base64url"),
    );
  });

  it("keeps the provider's strict check for a different client or callback", async () => {
    const findVerification = lookup("other-client");
    expect(await authorizedCodexLoopbackRedirect({
      clientId: "codex", code, redirectUri: tokenRequest, findVerification,
    })).toBeNull();
    expect(await authorizedCodexLoopbackRedirect({
      clientId: "other-client", code,
      redirectUri: "http://localhost:63479/callback", findVerification,
    })).toBeNull();
  });

  it("does not look up malformed codes or remote redirects", async () => {
    const findVerification = lookup();
    expect(await authorizedCodexLoopbackRedirect({
      clientId: "codex", code, redirectUri: "https://example.com/callback", findVerification,
    })).toBeNull();
    expect(await authorizedCodexLoopbackRedirect({
      clientId: "codex", code: "bad-code", redirectUri: tokenRequest, findVerification,
    })).toBeNull();
    expect(findVerification).not.toHaveBeenCalled();
  });

  it("ignores malformed and missing verification values", async () => {
    for (const value of [undefined, "not json", JSON.stringify({ type: "refresh_token" })]) {
      expect(await authorizedCodexLoopbackRedirect({
        clientId: "codex", code, redirectUri: tokenRequest,
        findVerification: async () => ({ value }),
      })).toBeNull();
    }
  });
});
