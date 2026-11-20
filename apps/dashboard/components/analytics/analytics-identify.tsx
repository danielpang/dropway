"use client";

import { useEffect } from "react";
import posthog from "posthog-js";

import { initPostHog } from "@/components/analytics/posthog-provider";

/**
 * Associates the browser's PostHog events with the signed-in user and their
 * active organization. Rendered inside the authenticated app shell (the (app)
 * layout already resolved the session), so every dashboard pageview/event is:
 *  - attributed to the person (identify),
 *  - stamped with `org_id` (a super property, the same property every server
 *    event uses), and
 *  - tied to the `organization` group, whose `name` is set here too.
 *
 * initPostHog() runs first because this effect fires before the provider's (a
 * child's effects run before its parent's), and posthog-js drops calls made
 * before init. Idempotent: PostHog de-dupes repeat identify/group calls.
 */
export function AnalyticsIdentify({
  userId,
  email,
  organization,
  organizationName,
}: {
  userId: string;
  email?: string | null;
  /** The active org id. */
  organization?: string | null;
  organizationName?: string | null;
}) {
  useEffect(() => {
    if (!userId || !initPostHog()) return;
    applyIdentity(posthog, { userId, email, organization, organizationName });
  }, [userId, email, organization, organizationName]);

  return null;
}

/** The PostHog calls AnalyticsIdentify makes, split out so they can be tested. */
export function applyIdentity(
  ph: Pick<typeof posthog, "identify" | "register" | "unregister" | "group" | "resetGroups">,
  input: {
    userId: string;
    email?: string | null;
    organization?: string | null;
    organizationName?: string | null;
  },
): void {
  ph.identify(input.userId, {
    email: input.email ?? undefined,
    org_id: input.organization ?? undefined,
  });
  if (input.organization) {
    ph.register({ org_id: input.organization });
    ph.group("organization", input.organization, {
      name: input.organizationName ?? undefined,
    });
  } else {
    ph.unregister("org_id");
    ph.resetGroups();
  }
}
