// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the framework-free pieces of lib/api.ts — specifically
// ApiError + its 402 quota narrowing (asQuotaExceeded), which the upgrade modal
// depends on. The Go API signals a hard-cap hit PURELY by the 402 status (no
// `error` discriminator); the body is `quota.ExceededError`, recognized by its
// string `limit`. We assert every branch of that narrowing here.
//
// lib/api.ts is a "server-only" module that imports @/lib/auth and next/headers;
// the vitest config aliases `server-only` and `@/lib/auth` to stubs so the class
// loads under node. ApiError + asQuotaExceeded touch none of those at runtime.

import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/api";

describe("ApiError", () => {
  it("captures status, message, body, and the ApiError name", () => {
    const err = new ApiError(403, "API 403 on /v1/sites", { detail: "forbidden" });
    expect(err).toBeInstanceOf(Error);
    expect(err.name).toBe("ApiError");
    expect(err.status).toBe(403);
    expect(err.message).toBe("API 403 on /v1/sites");
    expect(err.body).toEqual({ detail: "forbidden" });
  });
});

describe("ApiError.asQuotaExceeded (402 quota narrowing)", () => {
  it("narrows a real 402 quota body (status 402 + string `limit`)", () => {
    const body = { limit: "sites", next_tier: "business", sales_url: null };
    const quota = new ApiError(402, "API 402 on /v1/sites", body).asQuotaExceeded();
    expect(quota).not.toBeNull();
    // The narrowed value is the body itself (the 402 status is the signal).
    expect(quota).toBe(body);
    expect(quota!.limit).toBe("sites");
  });

  it("narrows regardless of which resource string `limit` carries", () => {
    for (const limit of ["sites", "members", "deploys", "bandwidth", "domains"]) {
      const quota = new ApiError(402, "cap", { limit }).asQuotaExceeded();
      expect(quota?.limit).toBe(limit);
    }
  });

  it("returns null when the status is not 402 (even with a quota-shaped body)", () => {
    // A 403/429/500 with a `limit` body is NOT a quota cap — the status is the signal.
    for (const status of [200, 400, 403, 429, 500]) {
      expect(new ApiError(status, "x", { limit: "sites" }).asQuotaExceeded()).toBeNull();
    }
  });

  it("returns null for a 402 whose body lacks a string `limit`", () => {
    expect(new ApiError(402, "x", { limit: 5 }).asQuotaExceeded()).toBeNull(); // numeric
    expect(new ApiError(402, "x", { notLimit: "sites" }).asQuotaExceeded()).toBeNull();
    expect(new ApiError(402, "x", {}).asQuotaExceeded()).toBeNull();
  });

  it("returns null for a 402 with a non-object body (null / string / array)", () => {
    expect(new ApiError(402, "x", null).asQuotaExceeded()).toBeNull();
    expect(new ApiError(402, "x", "Payment Required").asQuotaExceeded()).toBeNull();
    // Arrays are objects but have no string `limit`.
    expect(new ApiError(402, "x", ["sites"]).asQuotaExceeded()).toBeNull();
  });
});
