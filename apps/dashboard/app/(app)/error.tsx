"use client";

import { useEffect } from "react";
import { RefreshCw } from "lucide-react";
import { usePostHog } from "posthog-js/react";

import { ErrorPageMetric } from "@/components/error/error-page-metric";
import { PrismShatter } from "@/components/error/prism-shatter";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

/**
 * Error boundary for the authenticated app shell. Renders INSIDE the (app)
 * layout (header + nav stay put), so when a dashboard page throws — most often
 * the Go control-plane API failing or returning an unexpected 5xx — the user
 * keeps their navigation and sees a recoverable error in the content area
 * instead of a blank screen. `reset()` re-runs the failed page to retry.
 *
 * (Failures in the (app) layout itself — e.g. the session/auth read — bubble up
 * to app/error.tsx, since a segment's own error.tsx can't catch its layout.)
 */
export default function AppError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const posthog = usePostHog();

  useEffect(() => {
    console.error(error);
    try {
      posthog?.captureException?.(error);
    } catch {
      /* analytics must never mask the original error */
    }
  }, [error, posthog]);

  return (
    <div className="mx-auto max-w-2xl">
      <ErrorPageMetric status={500} />
      <Card className="flex flex-col items-center gap-5 border-dashed p-10 text-center">
        <PrismShatter size={200} />
        <div className="space-y-2">
          <h2 className="text-lg font-semibold tracking-tight">
            Couldn&rsquo;t load this page
          </h2>
          <p className="text-sm text-muted-foreground">
            We couldn&rsquo;t reach the service or it returned an error. This is
            usually temporary — try again in a moment.
          </p>
          {error.digest ? (
            <p className="pt-1 font-mono text-xs text-muted-foreground/70">
              Reference: {error.digest}
            </p>
          ) : null}
        </div>
        <Button onClick={reset}>
          <RefreshCw aria-hidden />
          Try again
        </Button>
      </Card>
    </div>
  );
}
