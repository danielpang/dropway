"use client";

import { useEffect } from "react";
import Link from "next/link";
import { RefreshCw, ServerCrash } from "lucide-react";
import { usePostHog } from "posthog-js/react";

import { Button } from "@/components/ui/button";

/**
 * Route-level error boundary for the whole app (auth pages, onboarding, and the
 * (app) shell's own layout). Next.js renders this in place of the failed segment
 * — INSIDE the root layout, so theme + fonts are intact — whenever a Server/Client
 * Component throws during render (e.g. the control-plane API is unreachable, or a
 * loader hits an unexpected 500). `reset()` re-renders the segment to retry.
 *
 * Errors below the (app) shell are caught one level deeper by app/(app)/error.tsx
 * (which keeps the nav); this is the broader fallback, and app/global-error.tsx
 * is the last resort if the root layout itself fails.
 */
export default function GlobalRouteError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const posthog = usePostHog();

  useEffect(() => {
    // Surface to logs + PostHog error tracking (best-effort; never re-throws).
    console.error(error);
    try {
      posthog?.captureException?.(error);
    } catch {
      /* analytics must never mask the original error */
    }
  }, [error, posthog]);

  return (
    <main className="grid min-h-dvh place-items-center px-4">
      <div className="w-full max-w-md space-y-6 text-center">
        <span className="mx-auto grid size-12 place-items-center rounded-xl bg-secondary text-secondary-foreground">
          <ServerCrash className="size-6" aria-hidden />
        </span>
        <div className="space-y-2">
          <h1 className="text-2xl font-semibold tracking-tight">
            Something went wrong
          </h1>
          <p className="text-sm text-muted-foreground">
            We hit an unexpected error loading this page. This is usually
            temporary — try again, and if it keeps happening, come back in a few
            minutes.
          </p>
          {error.digest ? (
            <p className="pt-1 font-mono text-xs text-muted-foreground/70">
              Reference: {error.digest}
            </p>
          ) : null}
        </div>
        <div className="flex flex-wrap items-center justify-center gap-2">
          <Button onClick={reset}>
            <RefreshCw aria-hidden />
            Try again
          </Button>
          <Button asChild variant="outline">
            <Link href="/dashboard">Back to dashboard</Link>
          </Button>
        </div>
      </div>
    </main>
  );
}
