// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for lib/token-cache.ts — the short-TTL, per-instance reuse of
// minted bearer JWTs that keeps the dashboard from re-signing a token (a jwks
// read + decrypt + EdDSA sign in Better Auth) on every page load. The cache is
// pure and clock-injectable, so all of TTL expiry, per-key isolation, the
// tenant-scoped key, and bounded eviction are covered here without the Next
// runtime or a live mint.

import { describe, expect, it } from "vitest";

import { TokenCache, tokenCacheKey } from "@/lib/token-cache";

/** A controllable clock so TTL behavior is deterministic. */
function clock(start = 0) {
  let t = start;
  return {
    now: () => t,
    advance: (ms: number) => {
      t += ms;
    },
  };
}

describe("tokenCacheKey", () => {
  it("scopes the key by session AND active org", () => {
    expect(tokenCacheKey("sess1", "orgA")).toBe("sess1:orgA");
    // Same session, different org → different key (an org switch must re-mint).
    expect(tokenCacheKey("sess1", "orgA")).not.toBe(tokenCacheKey("sess1", "orgB"));
    // Same org, different session → different key (users never share a token).
    expect(tokenCacheKey("sess1", "orgA")).not.toBe(tokenCacheKey("sess2", "orgA"));
  });

  it("collapses a missing active org to the empty string", () => {
    expect(tokenCacheKey("sess1", null)).toBe("sess1:");
    expect(tokenCacheKey("sess1", undefined)).toBe("sess1:");
    expect(tokenCacheKey("sess1", null)).toBe(tokenCacheKey("sess1", undefined));
  });
});

describe("TokenCache", () => {
  it("returns null on a miss", () => {
    const cache = new TokenCache();
    expect(cache.get("nope")).toBeNull();
  });

  it("returns a cached token within the TTL", () => {
    const c = clock();
    const cache = new TokenCache({ ttlMs: 60_000, now: c.now });
    cache.set("k", "tok");

    expect(cache.get("k")).toBe("tok");
    c.advance(59_999); // still inside the window
    expect(cache.get("k")).toBe("tok");
  });

  it("expires a token once the TTL elapses", () => {
    const c = clock();
    const cache = new TokenCache({ ttlMs: 60_000, now: c.now });
    cache.set("k", "tok");

    c.advance(60_000); // expiresAt is exclusive (<= now → expired)
    expect(cache.get("k")).toBeNull();
  });

  it("drops the expired entry on read (no lingering past the window)", () => {
    const c = clock();
    const cache = new TokenCache({ ttlMs: 1_000, now: c.now });
    cache.set("k", "tok");
    expect(cache.size).toBe(1);

    c.advance(1_000);
    expect(cache.get("k")).toBeNull();
    expect(cache.size).toBe(0);
  });

  it("isolates entries across keys", () => {
    const cache = new TokenCache();
    cache.set("a", "tokA");
    cache.set("b", "tokB");
    expect(cache.get("a")).toBe("tokA");
    expect(cache.get("b")).toBe("tokB");
  });

  it("re-mints (returns null) after delete", () => {
    const cache = new TokenCache();
    cache.set("k", "tok");
    cache.delete("k");
    expect(cache.get("k")).toBeNull();
  });

  it("refreshes the TTL when a key is re-set", () => {
    const c = clock();
    const cache = new TokenCache({ ttlMs: 1_000, now: c.now });
    cache.set("k", "old");

    c.advance(900);
    cache.set("k", "new"); // re-mint resets the window
    c.advance(900); // 1800ms since first set, but only 900 since the refresh
    expect(cache.get("k")).toBe("new");
  });

  it("stays bounded at maxEntries, evicting expired entries first", () => {
    const c = clock();
    const cache = new TokenCache({ ttlMs: 1_000, maxEntries: 3, now: c.now });

    cache.set("a", "1");
    cache.set("b", "2");
    c.advance(1_000); // a and b are now expired
    cache.set("c", "3"); // fresh
    cache.set("d", "4"); // hitting the cap sweeps the two expired entries first

    expect(cache.size).toBeLessThanOrEqual(3);
    expect(cache.get("c")).toBe("3");
    expect(cache.get("d")).toBe("4");
    // The expired ones are gone rather than evicting the live tokens.
    expect(cache.get("a")).toBeNull();
    expect(cache.get("b")).toBeNull();
  });

  it("evicts oldest-first when every entry is still live and at the cap", () => {
    const cache = new TokenCache({ ttlMs: 60_000, maxEntries: 2 });
    cache.set("a", "1");
    cache.set("b", "2");
    cache.set("c", "3"); // at cap, all live → oldest ("a") is dropped

    expect(cache.size).toBeLessThanOrEqual(2);
    expect(cache.get("a")).toBeNull();
    expect(cache.get("b")).toBe("2");
    expect(cache.get("c")).toBe("3");
  });
});
