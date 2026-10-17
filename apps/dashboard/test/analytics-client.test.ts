// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for lib/analytics-client.ts — the browser-side error sink. These pin
// its contract: it funnels every error-boundary capture through one function (so
// swapping vendors is a one-file change), coerces non-Error throws so they carry
// a stack, forwards optional properties, and is strictly best-effort — it never
// throws into the caller, so a telemetry failure can never mask the original
// error that tripped the boundary.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Spy shared with the mocked posthog-js singleton below. vi.hoisted lets the
// (hoisted) vi.mock factory reference it without a TDZ error.
const { captureException } = vi.hoisted(() => ({
  captureException: vi.fn(),
}));

// posthog-js default-exports a singleton object; mock just the method we use.
vi.mock("posthog-js", () => ({
  default: { captureException },
}));

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.resetModules();
});

describe("captureClientException", () => {
  it("forwards an Error to the SDK as-is, with properties", async () => {
    const { captureClientException } = await import("@/lib/analytics-client");
    const error = new Error("boom");
    captureClientException(error, { route: "/dashboard" });

    expect(captureException).toHaveBeenCalledTimes(1);
    const [sentError, props] = captureException.mock.calls[0]!;
    expect(sentError).toBe(error);
    expect(props).toEqual({ route: "/dashboard" });
  });

  it("coerces a non-Error throw into an Error so it carries a stack", async () => {
    const { captureClientException } = await import("@/lib/analytics-client");
    captureClientException("plain string failure");

    const [sentError] = captureException.mock.calls[0]!;
    expect(sentError).toBeInstanceOf(Error);
    expect((sentError as Error).message).toBe("plain string failure");
  });

  it("never throws when the SDK capture throws", async () => {
    captureException.mockImplementationOnce(() => {
      throw new Error("sdk exploded");
    });
    const { captureClientException } = await import("@/lib/analytics-client");
    expect(() => captureClientException(new Error("boom"))).not.toThrow();
  });
});

describe("captureClientException when the SDK is unconfigured", () => {
  // No `captureException` method on the singleton (key unset / not initialized).
  it("no-ops without throwing", async () => {
    vi.resetModules();
    vi.doMock("posthog-js", () => ({ default: {} }));
    const { captureClientException } = await import("@/lib/analytics-client");
    expect(() => captureClientException(new Error("boom"))).not.toThrow();
  });
});
