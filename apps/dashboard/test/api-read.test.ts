// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// settleApiRead is the boundary between an API read and Next control flow.
// 401 must redirect (so onRequestError never sees it); 404 must notFound();
// anything else must surface as the original error.

import { describe, expect, it, vi } from "vitest";

const { notFound, redirect } = vi.hoisted(() => ({
  notFound: vi.fn((): never => {
    throw new Error("NEXT_NOT_FOUND");
  }),
  redirect: vi.fn((url: string): never => {
    throw new Error(`NEXT_REDIRECT:${url}`);
  }),
}));

vi.mock("next/navigation", () => ({ notFound, redirect }));

import { ApiError } from "@/lib/api";
import { settleApiRead } from "@/lib/api-read";

describe("settleApiRead", () => {
  it("redirects a 401 to the re-auth sign-in URL", () => {
    const err = new ApiError(401, "API 401 on /v1/sites/abc", { error: "unauthorized" });
    expect(() => settleApiRead(err, "/sites/abc")).toThrow(
      "NEXT_REDIRECT:/sign-in?callbackURL=%2Fsites%2Fabc&reauth=1",
    );
    expect(redirect).toHaveBeenCalledWith(
      "/sign-in?callbackURL=%2Fsites%2Fabc&reauth=1",
    );
  });

  it("turns a 404 into notFound", () => {
    const err = new ApiError(404, "API 404 on /v1/sites/abc", null);
    expect(() => settleApiRead(err, "/sites/abc")).toThrow("NEXT_NOT_FOUND");
    expect(notFound).toHaveBeenCalledOnce();
  });

  it("rethrows a 500 and non-API failures", () => {
    const apiErr = new ApiError(500, "API 500 on /v1/sites/abc", null);
    expect(() => settleApiRead(apiErr, "/sites/abc")).toThrow(apiErr);
    const boom = new Error("network");
    expect(() => settleApiRead(boom, "/sites/abc")).toThrow(boom);
  });
});
