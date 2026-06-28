import posthog from "posthog-js";

/**
 * The vendor-neutral CLIENT-side error sink: report a caught exception from the
 * browser to error tracking. This is the browser analogue of the Go
 * `errtrack.Reporter` seam and the server's `captureServerException`
 * (lib/analytics-server.ts) — the React error boundaries (app/error.tsx,
 * app/(app)/error.tsx, app/global-error.tsx) funnel through here instead of
 * touching the PostHog SDK directly, so swapping PostHog for another vendor is a
 * one-function change confined to this file.
 *
 * Scope: this is the *manual* capture path for errors React hands to an error
 * boundary (which it swallows before they reach window error handlers).
 * Unhandled errors + promise rejections that escape to the window are already
 * autocaptured by the browser SDK (`capture_exceptions` in
 * components/analytics/posthog-provider.tsx) — that init lives with the provider
 * because it is inherently SDK-specific; a different vendor would configure its
 * own autocapture there.
 *
 * `environment` is not stamped here: the provider registers it as a super
 * property, so it already rides on every captured event.
 *
 * Best-effort: no-ops when PostHog is unconfigured (self-host without a key) and
 * never throws into the caller, so error reporting can never mask the original
 * error that triggered the boundary.
 */
export function captureClientException(
  error: unknown,
  properties?: Record<string, unknown>,
): void {
  try {
    // Coerce to an Error so it always carries a name + message + stack into
    // Error Tracking, mirroring the server/edge seams.
    const err = error instanceof Error ? error : new Error(String(error));
    // Optional-chained: the singleton may be uninitialized (no key, or the root
    // layout failed before the provider mounted — see app/global-error.tsx).
    posthog?.captureException?.(err, properties);
  } catch {
    // Telemetry must never mask the original error.
  }
}
