// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Edge error tracking. Reports an unexpected exception from the serving Worker to
// PostHog Error Tracking as a `$exception` event. This is the Worker's analogue
// of the Go `errtrack` seam and the dashboard's `captureServerException`: a small,
// best-effort sink that never throws and no-ops when no POSTHOG_KEY is set.
//
// It mirrors analytics.ts (buildVisitPayload + a POST to /capture/): the only
// difference is the event shape (`$exception` with `$exception_list`). Swapping
// PostHog for another vendor is a change to this one file.

import type { AnalyticsEnv, CaptureFetch } from "./analytics";

const DEFAULT_HOST = "https://us.posthog.com";

/** Exceptions from the edge have no acting user; attribute to a stable system id
 * (matches the Go services' "system" fallback). */
const SYSTEM_DISTINCT_ID = "system";

/** Coerce an unknown thrown value to an Error so it always carries name + message. */
function toError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}

/** The PostHog `/capture` payload for a `$exception` event (pure → unit-tested). */
export function buildExceptionPayload(input: {
  apiKey: string;
  error: Error;
  props: Record<string, unknown>;
  environment: string;
  now: Date;
}): Record<string, unknown> {
  return {
    api_key: input.apiKey,
    event: "$exception",
    distinct_id: SYSTEM_DISTINCT_ID,
    timestamp: input.now.toISOString(),
    properties: {
      $exception_list: [
        {
          type: input.error.name || "Error",
          value: input.error.message,
          // The Worker cannot produce PostHog-format stack frames; the raw stack
          // rides along as the `stack` property below for debugging.
          mechanism: { handled: false, synthetic: false },
        },
      ],
      $exception_level: "error",
      stack: input.error.stack ?? null,
      environment: input.environment,
      $lib: "dropway-serving-worker",
      ...input.props,
    },
  };
}

/**
 * Best-effort exception capture. No-ops without a key; otherwise POSTs a
 * `$exception` event to PostHog. Never throws. Schedule it via `waitUntil` so it
 * runs after the response is sent.
 */
export async function captureException(
  env: AnalyticsEnv,
  error: unknown,
  props: Record<string, unknown> = {},
  fetchImpl: CaptureFetch = (input, init) => fetch(input, init as RequestInit),
): Promise<void> {
  const key = env.POSTHOG_KEY;
  if (!key) return;
  try {
    const payload = buildExceptionPayload({
      apiKey: key,
      error: toError(error),
      props,
      environment: env.ENVIRONMENT ?? "production",
      now: new Date(),
    });
    const host = (env.POSTHOG_HOST ?? DEFAULT_HOST).replace(/\/$/, "");
    await fetchImpl(`${host}/capture/`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  } catch {
    // Error reporting must never affect content serving.
  }
}
