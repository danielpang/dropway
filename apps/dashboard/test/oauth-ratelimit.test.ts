// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// M4: the unauthenticated OAuth surface (Dynamic Client Registration + the
// authorize/consent/token endpoints) must carry rate-limit rules so an anonymous
// caller can't flood them. These are the rules wired into Better Auth's
// rateLimit.customRules in lib/auth.ts.

import { describe, expect, it } from "vitest";

import { oauthRateLimitRules } from "@/lib/oauth-ratelimit";

describe("oauthRateLimitRules", () => {
  it("covers every unauthenticated OAuth endpoint that can be flooded", () => {
    for (const path of [
      "/oauth2/register",
      "/oauth2/authorize",
      "/oauth2/consent",
      "/oauth2/token",
    ]) {
      expect(oauthRateLimitRules[path]).toBeDefined();
    }
  });

  it("bounds unauthenticated DCR the most tightly", () => {
    const reg = oauthRateLimitRules["/oauth2/register"];
    if (!reg) throw new Error("missing /oauth2/register rule");
    // A real user registers a client once; allow only a small burst per hour, and
    // keep max no looser than the oauth-provider plugin's own short-window default
    // (5) so this rule never relaxes the burst it overrides.
    expect(reg.window).toBeGreaterThanOrEqual(600);
    expect(reg.max).toBeLessThanOrEqual(5);
  });

  it("uses positive, finite windows and maxes for every rule", () => {
    for (const [path, rule] of Object.entries(oauthRateLimitRules)) {
      expect(rule.window, path).toBeGreaterThan(0);
      expect(rule.max, path).toBeGreaterThan(0);
      expect(Number.isFinite(rule.window), path).toBe(true);
      expect(Number.isFinite(rule.max), path).toBe(true);
    }
  });
});
