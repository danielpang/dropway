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
  const pathname = usePathname();
  const invitationLanding = pathname?.startsWith("/shared-projects/accept") === true;

  useEffect(() => {
    if (!key || invitationLanding) {
      if (invitationLanding && posthog.__loaded) posthog.stopSessionRecording();
      return;
    }
    initPostHog();
  }, [key, invitationLanding]);

  if (!key || invitationLanding) return <>{children}</>;

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

/** The invitation landing page carries a bearer fragment that must never reach
 * analytics, so PostHog is never started there. */
function onInvitationLanding(): boolean {
  return window.location.pathname.startsWith("/shared-projects/accept");
}

/**
 * Start the browser SDK once; safe to call from any client component's effect.
 * Returns whether PostHog is running.
 *
 * Components that use PostHog in an effect must call this first. React runs a
 * child's effects BEFORE its parent's, so AnalyticsIdentify and PageViewTracker
 * (children of PostHogProvider) used to call identify()/capture() before the
 * provider's effect ran init(), and posthog-js drops calls made before init: no
 * user, org or first pageview was ever recorded from the browser.
 */
export function initPostHog(): boolean {
  const key = posthogClientKey();
  if (!key || typeof window === "undefined" || onInvitationLanding()) return false;
  if (!posthog.__loaded) {
    posthog.init(key, {
      api_host: posthogHost(),
      // Invitation bearer fragments must never enter analytics or error capture.
      before_send: (event) => (onInvitationLanding() ? null : event),
      capture_pageview: false, // captured manually on App Router navigation
      capture_pageleave: true,
      capture_performance: true, // Web Vitals + page-load timing
      capture_exceptions: true, // autocapture unhandled errors + promise rejections → Error Tracking
      person_profiles: "identified_only",
      defaults: "2025-05-24",
    });
    posthog.register({ environment: appEnvironment() });
  }
  return true;
}

/**
 * Forget the signed-in user and org in this browser: clears the identity, the
 * org group and the org_id super property, so the next account on a shared
 * browser starts clean. Call on sign-out, before redirecting.
 */
export function resetAnalytics(): void {
  if (posthog.__loaded) posthog.reset();
}

/** Captures a `$pageview` on every App Router navigation (path or query change). */
function PageViewTracker() {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const client = usePostHog();

  useEffect(() => {
    if (!pathname || !client || !initPostHog()) return;
    let url = window.location.origin + pathname;
    const qs = searchParams?.toString();
    if (qs) url += `?${qs}`;
    client.capture("$pageview", { $current_url: url });
  }, [pathname, searchParams, client]);

  return null;
}
