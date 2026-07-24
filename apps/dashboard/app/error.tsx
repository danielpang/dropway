"use client";

import { useEffect } from "react";
import Link from "next/link";
import { RefreshCw } from "lucide-react";

import { captureClientException } from "@/lib/analytics-client";
import { ErrorPageMetric } from "@/components/error/error-page-metric";
import { BurstPipe } from "@/components/error/burst-pipe";
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
  useEffect(() => {
    // Surface to logs + the vendor-neutral error sink (best-effort; never re-throws).
    console.error(error);
    captureClientException(error);
  }, [error]);

  return (
    <main className="grid min-h-dvh place-items-center px-4">
      <ErrorPageMetric status={500} />
      <div className="w-full max-w-md space-y-6 text-center">
        <div className="flex justify-center">
          <BurstPipe />
        </div>
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
