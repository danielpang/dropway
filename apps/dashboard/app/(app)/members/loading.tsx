import { Skeleton } from "@/components/ui/skeleton";
import { Card, CardContent, CardHeader } from "@/components/ui/card";

/**
 * Instant loading UI for Members. Next.js commits this the moment the nav link
 * is clicked, so the URL + active nav state update immediately and the page
 * structure paints while the (force-dynamic) server component loads the org's
 * members, invitations, and per-user storage over the API. The static heading
 * is real; everything data-dependent is skeletoned to mirror page.tsx.
 */
export default function MembersLoading() {
  return (
    <div className="mx-auto max-w-3xl space-y-8">
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">Members</h1>
        <Skeleton className="h-4 w-72 max-w-full" />
      </div>

      {/* Invite card (admins) */}
      <Card>
        <CardHeader className="space-y-2">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-4 w-64 max-w-full" />
        </CardHeader>
        <CardContent>
          <Skeleton className="h-9 w-full" />
        </CardContent>
      </Card>

      {/* Member list */}
      <Card>
        <CardContent className="divide-y divide-border p-0">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="flex items-center justify-between gap-3 px-5 py-4">
              <div className="flex min-w-0 items-center gap-3">
                <Skeleton className="size-9 shrink-0 rounded-full" />
                <div className="min-w-0 space-y-1.5">
                  <Skeleton className="h-4 w-40" />
                  <Skeleton className="h-3 w-28" />
                </div>
              </div>
              <Skeleton className="h-5 w-16 rounded-full" />
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
