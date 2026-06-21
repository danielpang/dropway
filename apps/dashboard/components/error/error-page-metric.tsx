"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";
import { usePostHog } from "posthog-js/react";

/**
 * Emits a single `error_page_viewed` analytics event when an error/not-found
 * page mounts, so we can track how often users land on a 404 or 500 (and on
 * which paths — broken links, dead deploys, flaky APIs). `environment` rides
 * along automatically as a registered super property; no-ops without PostHog.
 */
export function ErrorPageMetric({ status }: { status: 404 | 500 }) {
  const posthog = usePostHog();
  const pathname = usePathname();

  useEffect(() => {
    if (!posthog) return;
    try {
      posthog.capture("error_page_viewed", { status, path: pathname });
    } catch {
      /* analytics must never affect the page */
    }
  }, [posthog, status, pathname]);

  return null;
}
