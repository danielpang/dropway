import { Skeleton } from "@/components/ui/skeleton";
import { Card, CardContent, CardHeader } from "@/components/ui/card";

/**
 * Instant loading UI for Organization settings. Painted the moment the nav link
 * is clicked so the active nav state and page shell update immediately while the
 * (force-dynamic) server component reads the org's sharing/MCP policy over the
 * API. The static heading is real; the policy cards are skeletoned to mirror
 * page.tsx.
 */
export default function SettingsLoading() {
  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">
          Organization settings
        </h1>
        <Skeleton className="h-4 w-64 max-w-full" />
      </div>

      {/* Policy toggle cards */}
      {Array.from({ length: 2 }).map((_, i) => (
        <Card key={i}>
          <CardHeader className="space-y-2">
            <Skeleton className="h-5 w-44" />
            <Skeleton className="h-4 w-72 max-w-full" />
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Skeleton className="h-4 w-56 max-w-full" />
            <Skeleton className="h-6 w-11 shrink-0 rounded-full" />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}
