"use client";

import { Suspense, useEffect } from "react";
import { usePathname, useSearchParams } from "next/navigation";
import posthog from "posthog-js";
import { PostHogProvider as PHProvider, usePostHog } from "posthog-js/react";

import { appEnvironment, posthogClientKey, posthogHost } from "@/lib/env";

/**
 * Initializes the PostHog browser SDK and tracks pageviews + page-load
 * performance for the whole app. Mounted once at the root so every route — auth,
 * onboarding, and the app shell — is covered.
 *
 * - Manual `$pageview` capture (capture_pageview:false) because the App Router
 *   does client-side navigation; we re-capture on every path/query change.
 * - `capture_performance:true` turns on Web Vitals (LCP/FCP/CLS/INP) + page-load
 *   timing autocapture — the "page load" metric.
 * - `environment` is registered as a super property so it rides on every event.
 *
 * When NEXT_PUBLIC_POSTHOG_KEY is unset (e.g. a self-host with no PostHog) this
 * renders children untouched and initializes nothing.
 */
export function PostHogProvider({ children }: { children: React.ReactNode }) {
  const key = posthogClientKey();

  useEffect(() => {
    if (!key) return;
    if (posthog.__loaded) return;
    posthog.init(key, {
      api_host: posthogHost(),
      capture_pageview: false, // captured manually on App Router navigation
      capture_pageleave: true,
      capture_performance: true, // Web Vitals + page-load timing
      person_profiles: "identified_only",
      defaults: "2025-05-24",
    });
    posthog.register({ environment: appEnvironment() });
  }, [key]);

  if (!key) return <>{children}</>;

  return (
    <PHProvider client={posthog}>
      {/* useSearchParams must sit under Suspense so it doesn't force the whole
          tree into client-side rendering. */}
      <Suspense fallback={null}>
        <PageViewTracker />
      </Suspense>
      {children}
    </PHProvider>
  );
}

/** Captures a `$pageview` on every App Router navigation (path or query change). */
function PageViewTracker() {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const client = usePostHog();

  useEffect(() => {
    if (!pathname || !client) return;
    let url = window.location.origin + pathname;
    const qs = searchParams?.toString();
    if (qs) url += `?${qs}`;
    client.capture("$pageview", { $current_url: url });
  }, [pathname, searchParams, client]);

  return null;
}
