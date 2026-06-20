import { Skeleton } from "@/components/ui/skeleton";
import { Card } from "@/components/ui/card";

/**
 * Instant loading UI for the Audit log. Painted the moment the nav link is
 * clicked so the active nav state and page shell update immediately while the
 * (force-dynamic) server component loads the org's audit page over the API. The
 * static heading is real; the table rows are skeletoned to mirror page.tsx.
 */
export default function AuditLoading() {
  return (
    <div className="mx-auto max-w-5xl space-y-8">
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">Audit log</h1>
        <Skeleton className="h-4 w-80 max-w-full" />
      </div>

      <Card className="p-0">
        <div className="divide-y divide-border">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="flex items-center gap-4 px-5 py-3.5">
              <Skeleton className="h-4 w-32 shrink-0" />
              <Skeleton className="h-4 w-40" />
              <Skeleton className="ml-auto h-4 w-24 shrink-0" />
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}
