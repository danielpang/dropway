"use client";

import { useEffect } from "react";
import { usePostHog } from "posthog-js/react";

/**
 * Associates the browser's PostHog events with the signed-in user and their
 * active organization. Rendered inside the authenticated app shell (the (app)
 * layout already resolved the session), so every dashboard pageview/event is
 * attributed to a person and an `organization` group.
 *
 * Idempotent: PostHog de-dupes repeat identify calls with the same id, so
 * re-rendering across navigations is cheap.
 */
export function AnalyticsIdentify({
  userId,
  email,
  organization,
  organizationName,
}: {
  userId: string;
  email?: string | null;
  organization?: string | null;
  organizationName?: string | null;
}) {
  const client = usePostHog();

  useEffect(() => {
    if (!client || !userId) return;
    client.identify(userId, {
      email: email ?? undefined,
      organization: organization ?? undefined,
    });
    if (organization) {
      client.group("organization", organization, {
        name: organizationName ?? undefined,
      });
    }
  }, [client, userId, email, organization, organizationName]);

  return null;
}
