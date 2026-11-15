// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for createCheckoutAction (billing/actions.ts): one subscription per
// org. When the API refuses a second Checkout because the org already pays
// (409 subscription_exists), the action must hand back the Billing Portal URL so
// the plan change happens on the existing subscription instead of creating a
// second one.

import { afterEach, describe, expect, it, vi } from "vitest";

const { createCheckout, createPortal } = vi.hoisted(() => ({
  createCheckout: vi.fn(),
  createPortal: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  api: { createCheckout, createPortal },
  ApiError: class ApiError extends Error {
    status: number;
    body: unknown;
    constructor(status: number, message: string, body: unknown) {
      super(message);
      this.status = status;
      this.body = body;
    }
  },
}));

import { ApiError } from "@/lib/api";
import { createCheckoutAction } from "@/app/(app)/billing/actions";

afterEach(() => {
  vi.clearAllMocks();
});

describe("createCheckoutAction", () => {
  it("returns the Checkout URL for an org without a subscription", async () => {
    createCheckout.mockResolvedValue({ checkout_url: "https://checkout.stripe.test/s" });

    const result = await createCheckoutAction({ targetTier: "pro" });

    expect(result).toEqual({ ok: true, checkoutUrl: "https://checkout.stripe.test/s" });
    expect(createPortal).not.toHaveBeenCalled();
  });

  it("sends an org that already pays to the Billing Portal", async () => {
    createCheckout.mockRejectedValue(
      new ApiError(409, "conflict", {
        error: "subscription_exists",
        message: "This organization already has an active subscription.",
      }),
    );
    createPortal.mockResolvedValue({ portal_url: "https://portal.stripe.test/s" });

    const result = await createCheckoutAction({ targetTier: "business" });

    expect(result).toEqual({ ok: true, checkoutUrl: "https://portal.stripe.test/s" });
    expect(createPortal).toHaveBeenCalledTimes(1);
  });

  it("reports a portal failure instead of starting a second Checkout", async () => {
    createCheckout.mockRejectedValue(
      new ApiError(409, "conflict", { error: "subscription_exists" }),
    );
    createPortal.mockRejectedValue(new ApiError(500, "boom", null));

    const result = await createCheckoutAction({ targetTier: "business" });

    expect(result.ok).toBe(false);
    expect(createCheckout).toHaveBeenCalledTimes(1);
  });

  it("does not open the portal for other 409s", async () => {
    createCheckout.mockRejectedValue(
      new ApiError(409, "conflict", { error: "conflict", message: "Something else." }),
    );

    const result = await createCheckoutAction({ targetTier: "pro" });

    expect(result).toEqual({ ok: false, message: "Something else." });
    expect(createPortal).not.toHaveBeenCalled();
  });
});
