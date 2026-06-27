// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for lib/analytics-server.ts — the server-side PostHog capture path.
// These pin the SERVERLESS-SAFETY contract: every event/error must be sent with
// the *Immediate methods (captureImmediate / captureExceptionImmediate), which
// build-enqueue-and-send in a single awaited call. The old capture()+flush()
// pattern was racy on Vercel — capture() enqueues asynchronously, so a following
// flush() could run before the event was queued and send nothing
// (PostHog/posthog-js#2220), silently dropping every dashboard event/error once
// the function froze. We assert the Immediate methods are used (and bare
// capture/captureException are NOT) so that regression can't sneak back.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Spies shared with the mocked PostHog client below. vi.hoisted lets the
// (hoisted) vi.mock factory reference them without a TDZ error.
const { captureImmediate, captureExceptionImmediate, capture, captureException, flush } =
  vi.hoisted(() => ({
    captureImmediate: vi.fn().mockResolvedValue(undefined),
    captureExceptionImmediate: vi.fn().mockResolvedValue(undefined),
    capture: vi.fn(),
    captureException: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
  }));

vi.mock("posthog-node", () => ({
  // A class (not an arrow fn) so `new PostHog(...)` constructs cleanly.
  PostHog: class {
    captureImmediate = captureImmediate;
    captureExceptionImmediate = captureExceptionImmediate;
    capture = capture;
    captureException = captureException;
    flush = flush;
  },
}));

// Env: a configured ingest key so getClient() builds a (mocked) client.
vi.mock("@/lib/env", () => ({
  appEnvironment: () => "production",
  posthogHost: () => "https://us.i.posthog.com",
  posthogServerKey: () => "phc_test_key",
}));

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.resetModules();
});

describe("analytics-server captures (serverless-safe)", () => {
  it("captureSiteCreated sends via captureImmediate, not capture()+flush()", async () => {
    const { captureSiteCreated } = await import("@/lib/analytics-server");
    await captureSiteCreated({
      userId: "user_1",
      organization: "org_1",
      siteId: "site_1",
      slug: "docs",
    });

    expect(captureImmediate).toHaveBeenCalledTimes(1);
    expect(capture).not.toHaveBeenCalled();
    expect(flush).not.toHaveBeenCalled();

    const sent = captureImmediate.mock.calls[0]![0];
    expect(sent).toMatchObject({
      distinctId: "user_1",
      event: "site_created",
      groups: { organization: "org_1" },
    });
    expect(sent.properties).toMatchObject({
      environment: "production",
      organization: "org_1",
      site_id: "site_1",
      site_slug: "docs",
    });
  });

  it("captureSignup defaults the org group off and stamps environment", async () => {
    const { captureSignup } = await import("@/lib/analytics-server");
    await captureSignup({ userId: "user_2", email: "a@b.co", method: "google" });

    expect(captureImmediate).toHaveBeenCalledTimes(1);
    const sent = captureImmediate.mock.calls[0]![0];
    expect(sent.event).toBe("user_signed_up");
    // No organization → no group attached.
    expect(sent.groups).toBeUndefined();
    expect(sent.properties).toMatchObject({ environment: "production", method: "google" });
  });

  it("captureSignInSucceeded sends sign_in_succeeded, attaching the org group when known", async () => {
    const { captureSignInSucceeded } = await import("@/lib/analytics-server");
    await captureSignInSucceeded({ userId: "user_3", organization: "org_3", method: "email" });

    expect(captureImmediate).toHaveBeenCalledTimes(1);
    expect(capture).not.toHaveBeenCalled();
    expect(flush).not.toHaveBeenCalled();

    const sent = captureImmediate.mock.calls[0]![0];
    expect(sent).toMatchObject({
      distinctId: "user_3",
      event: "sign_in_succeeded",
      groups: { organization: "org_3" },
    });
    expect(sent.properties).toMatchObject({ environment: "production", method: "email" });
  });

  it("captureSignInFailed attributes to the system id (not the typed email) and records status/code/email props", async () => {
    const { captureSignInFailed } = await import("@/lib/analytics-server");
    await captureSignInFailed({
      status: 401,
      code: "INVALID_EMAIL_OR_PASSWORD",
      method: "email",
      email: "typo@b.co",
    });

    expect(captureImmediate).toHaveBeenCalledTimes(1);
    const sent = captureImmediate.mock.calls[0]![0];
    expect(sent.event).toBe("sign_in_failed");
    // No authenticated user: attributed to the system id so a typed email never
    // mints a person profile. No org → no group.
    expect(sent.distinctId).toBe("system");
    expect(sent.groups).toBeUndefined();
    expect(sent.properties).toMatchObject({
      environment: "production",
      status: 401,
      code: "INVALID_EMAIL_OR_PASSWORD",
      method: "email",
      email: "typo@b.co",
    });
  });

  it("captureSignUpFailed sends sign_up_failed attributed to the system id with status/code/email props", async () => {
    const { captureSignUpFailed } = await import("@/lib/analytics-server");
    await captureSignUpFailed({
      status: 422,
      code: "USER_ALREADY_EXISTS",
      method: "email",
      email: "taken@b.co",
    });

    expect(captureImmediate).toHaveBeenCalledTimes(1);
    const sent = captureImmediate.mock.calls[0]![0];
    expect(sent.event).toBe("sign_up_failed");
    expect(sent.distinctId).toBe("system");
    expect(sent.groups).toBeUndefined();
    expect(sent.properties).toMatchObject({
      environment: "production",
      status: 422,
      code: "USER_ALREADY_EXISTS",
      method: "email",
      email: "taken@b.co",
    });
  });

  it("captureDbCapacityIssue reports via captureExceptionImmediate, not captureException()+flush()", async () => {
    const { captureDbCapacityIssue } = await import("@/lib/analytics-server");
    const error = new Error("max clients reached in session mode");
    await captureDbCapacityIssue({
      reason: "pooler_session_exhausted",
      source: "better-auth",
      error,
    });

    expect(captureExceptionImmediate).toHaveBeenCalledTimes(1);
    expect(captureException).not.toHaveBeenCalled();
    expect(flush).not.toHaveBeenCalled();

    const [sentError, distinctId, props] = captureExceptionImmediate.mock.calls[0]!;
    expect(sentError).toBe(error);
    expect(distinctId).toBe("system"); // SYSTEM_DISTINCT_ID when no acting user
    expect(props).toMatchObject({
      issue: "db_connection_capacity",
      db_capacity_reason: "pooler_session_exhausted",
      source: "better-auth",
      environment: "production",
    });
  });

  it("coerces a non-Error into an Error so it carries a stack into Error Tracking", async () => {
    const { captureDbCapacityIssue } = await import("@/lib/analytics-server");
    await captureDbCapacityIssue({
      reason: "too_many_connections",
      source: "firstOrgId",
      error: "53300: too many clients already",
      distinctId: "user_9",
    });

    const [sentError, distinctId] = captureExceptionImmediate.mock.calls[0]!;
    expect(sentError).toBeInstanceOf(Error);
    expect((sentError as Error).message).toBe("53300: too many clients already");
    expect(distinctId).toBe("user_9");
  });
});
