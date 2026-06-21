// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the edge error sink (src/errtrack.ts):
// - buildExceptionPayload: the PostHog /capture shape carries a $exception_list
//   with the error type/value, env, and custom props
// - captureException:      no-ops without a key; POSTs otherwise; never throws
//
// Runs on the node pool (no Miniflare/Wrangler needed).

import { describe, expect, it, vi } from "vitest";

import {
  buildExceptionPayload,
  captureException,
} from "../src/errtrack";
import type { AnalyticsEnv, CaptureFetch } from "../src/analytics";

describe("buildExceptionPayload", () => {
  it("builds a $exception event from an Error with type, value, env, and props", () => {
    const err = new TypeError("boom");
    const payload = buildExceptionPayload({
      apiKey: "phc_test",
      error: err,
      props: { path: "/x", method: "GET" },
      environment: "production",
      now: new Date("2026-06-21T10:00:00Z"),
    });

    expect(payload["api_key"]).toBe("phc_test");
    expect(payload["event"]).toBe("$exception");
    expect(payload["distinct_id"]).toBe("system");
    expect(payload["timestamp"]).toBe("2026-06-21T10:00:00.000Z");

    const props = payload["properties"] as Record<string, unknown>;
    const list = props["$exception_list"] as Array<Record<string, unknown>>;
    expect(list[0]?.["type"]).toBe("TypeError");
    expect(list[0]?.["value"]).toBe("boom");
    expect(props["environment"]).toBe("production");
    expect(props["$lib"]).toBe("dropway-serving-worker");
    expect(props["path"]).toBe("/x");
    expect(props["method"]).toBe("GET");
  });
});

describe("captureException", () => {
  const env: AnalyticsEnv = { POSTHOG_KEY: "phc_test", ENVIRONMENT: "production" };

  it("no-ops when no key is configured", async () => {
    const fetchImpl = vi.fn<CaptureFetch>();
    await captureException({}, new Error("x"), {}, fetchImpl);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("POSTs a $exception to the capture endpoint", async () => {
    const fetchImpl = vi.fn<CaptureFetch>().mockResolvedValue(undefined);
    await captureException(env, new Error("kaboom"), { path: "/p" }, fetchImpl);

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    const [url, init] = fetchImpl.mock.calls[0]!;
    expect(url).toBe("https://us.posthog.com/capture/");
    expect(init.method).toBe("POST");
    const body = JSON.parse(init.body) as Record<string, unknown>;
    expect(body["event"]).toBe("$exception");
  });

  it("coerces a non-Error throw and never rejects, even if fetch fails", async () => {
    const fetchImpl = vi.fn<CaptureFetch>().mockRejectedValue(new Error("network"));
    await expect(
      captureException(env, "string failure", {}, fetchImpl),
    ).resolves.toBeUndefined();
    const body = JSON.parse(fetchImpl.mock.calls[0]![1].body) as Record<string, unknown>;
    const list = (body["properties"] as Record<string, unknown>)[
      "$exception_list"
    ] as Array<Record<string, unknown>>;
    expect(list[0]?.["value"]).toBe("string failure");
  });
});
