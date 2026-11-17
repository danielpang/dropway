// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Browser analytics identity. Two regressions this pins:
//  - initPostHog() starts the SDK on first use, once. React runs a child's effects
//    before its parent's, so AnalyticsIdentify / PageViewTracker used to call
//    posthog-js before the provider's effect ran init(), and those calls were
//    dropped: no browser identify, org or first pageview was ever recorded.
//  - Browser events carry org_id (a super property, the same name every server
//    event uses) and the org group with its name; a session with no org clears both.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const ph = vi.hoisted(() => ({
  __loaded: false,
  init: vi.fn(),
  register: vi.fn(),
  reset: vi.fn(),
}));
vi.mock("posthog-js", () => ({
  default: Object.assign(ph, {
    init: vi.fn(() => {
      ph.__loaded = true;
    }),
  }),
}));
vi.mock("posthog-js/react", () => ({ PostHogProvider: () => null, usePostHog: () => undefined }));
vi.mock("next/navigation", () => ({ usePathname: () => "/", useSearchParams: () => null }));
const env = vi.hoisted(() => ({ key: "phc_test" as string | undefined }));
vi.mock("@/lib/env", () => ({
  appEnvironment: () => "production",
  posthogClientKey: () => env.key,
  posthogHost: () => "https://us.i.posthog.com",
}));

beforeEach(() => {
  ph.__loaded = false;
  env.key = "phc_test";
  vi.stubGlobal("window", { location: { pathname: "/dashboard" } });
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("initPostHog", () => {
  it("starts PostHog on first use, once", async () => {
    const posthog = (await import("posthog-js")).default;
    const { initPostHog } = await import("@/components/analytics/posthog-provider");
    expect(initPostHog()).toBe(true);
    expect(initPostHog()).toBe(true);
    expect(posthog.init).toHaveBeenCalledTimes(1);
    expect(posthog.register).toHaveBeenCalledWith({ environment: "production" });
  });

  it("does nothing without a key or on the invitation landing", async () => {
    const posthog = (await import("posthog-js")).default;
    const { initPostHog } = await import("@/components/analytics/posthog-provider");
    env.key = undefined;
    expect(initPostHog()).toBe(false);
    env.key = "phc_test";
    vi.stubGlobal("window", { location: { pathname: "/shared-projects/accept" } });
    expect(initPostHog()).toBe(false);
    expect(posthog.init).not.toHaveBeenCalled();
  });

  it("resetAnalytics clears identity only once PostHog is running", async () => {
    const posthog = (await import("posthog-js")).default;
    const { initPostHog, resetAnalytics } = await import("@/components/analytics/posthog-provider");
    resetAnalytics();
    expect(posthog.reset).not.toHaveBeenCalled();
    initPostHog();
    resetAnalytics();
    expect(posthog.reset).toHaveBeenCalledTimes(1);
  });
});

describe("applyIdentity", () => {
  function fakePostHog() {
    return {
      identify: vi.fn(),
      register: vi.fn(),
      unregister: vi.fn(),
      group: vi.fn(),
      resetGroups: vi.fn(),
    };
  }

  it("identifies the user and stamps org_id + the named org group", async () => {
    const { applyIdentity } = await import("@/components/analytics/analytics-identify");
    const fake = fakePostHog();
    applyIdentity(fake as never, {
      userId: "user_1",
      email: "a@b.co",
      organization: "org_1",
      organizationName: "Acme",
    });
    expect(fake.identify).toHaveBeenCalledWith("user_1", { email: "a@b.co", org_id: "org_1" });
    expect(fake.register).toHaveBeenCalledWith({ org_id: "org_1" });
    expect(fake.group).toHaveBeenCalledWith("organization", "org_1", { name: "Acme" });
  });

  it("clears org_id and the group when there is no active org", async () => {
    const { applyIdentity } = await import("@/components/analytics/analytics-identify");
    const fake = fakePostHog();
    applyIdentity(fake as never, { userId: "user_1" });
    expect(fake.unregister).toHaveBeenCalledWith("org_id");
    expect(fake.resetGroups).toHaveBeenCalled();
    expect(fake.register).not.toHaveBeenCalled();
    expect(fake.group).not.toHaveBeenCalled();
  });
});
