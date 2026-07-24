import { Skeleton } from "@/components/ui/skeleton";
import { Card } from "@/components/ui/card";

/**
 * Instant loading UI for the sites list. Next.js renders this as the Suspense
 * fallback the moment /dashboard is requested, e.g. when the header logo is
 * clicked, so the page structure paints immediately while the (force-dynamic)
 * server component fetches the sites list, billing state, and active org over the
 * API. The static heading is real; everything that needs a network round-trip
 * (the "New site" button, which depends on billing + org slug, and each site
 * card) is skeletoned, mirroring page.tsx so the swap-in doesn't shift.
 */
export default function DashboardLoading() {
  return (
    <div className="mx-auto max-w-5xl space-y-8">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0 flex-1 space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Sites</h1>
          <p className="text-muted-foreground">
            Deploy a folder, get a live, access-controlled URL.
          </p>
        </div>
        {/* "New site" depends on billing/org state; ml-auto mirrors page.tsx wrap alignment. */}
        <Skeleton className="ml-auto h-9 w-28 shrink-0" />
      </div>

      <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {Array.from({ length: 4 }).map((_, i) => (
          <li key={i}>
            <SiteCardSkeleton />
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Mirrors the SiteRow card in page.tsx: icon + slug + URL, then status pills. */
function SiteCardSkeleton() {
  return (
    <Card className="p-5">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <Skeleton className="size-8 shrink-0 rounded-md" />
          <Skeleton className="h-4 w-28" />
        </div>
        <Skeleton className="size-4 shrink-0 rounded-sm" />
      </div>
      <Skeleton className="mt-2 h-3 w-44 max-w-full" />
      <div className="mt-4 flex items-center gap-2">
        <Skeleton className="h-5 w-14 rounded-full" />
        <Skeleton className="h-5 w-20 rounded-full" />
      </div>
    </Card>
  );
}
