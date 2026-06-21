import type { Metadata } from "next";
import Link from "next/link";

import { ErrorPageMetric } from "@/components/error/error-page-metric";
import { BurstPipe } from "@/components/error/burst-pipe";
import { Button } from "@/components/ui/button";

export const metadata: Metadata = { title: "Page not found" };

/**
 * Global 404. Next.js renders this for any URL that doesn't match a route (a
 * random/mistyped address) and wherever the app calls `notFound()`. It lives at
 * the root so it shows for signed-out visitors too, inside the root layout
 * (theme + analytics intact). Shares the prism motif with the 500 boundaries and
 * emits an `error_page_viewed` (status 404) metric on mount.
 */
export default function NotFound() {
  return (
    <main className="grid min-h-dvh place-items-center px-4">
      <ErrorPageMetric status={404} />
      <div className="w-full max-w-md space-y-6 text-center">
        <div className="flex justify-center">
          <BurstPipe />
        </div>
        <div className="space-y-2">
          <p className="font-mono text-sm font-medium text-muted-foreground">
            404
          </p>
          <h1 className="text-2xl font-semibold tracking-tight">
            This page slipped through
          </h1>
          <p className="text-sm text-muted-foreground">
            We couldn&rsquo;t find anything at this address. The link may be
            broken, or the page may have moved.
          </p>
        </div>
        <div className="flex flex-wrap items-center justify-center gap-2">
          <Button asChild>
            <Link href="/dashboard">Back to dashboard</Link>
          </Button>
        </div>
      </div>
    </main>
  );
}
