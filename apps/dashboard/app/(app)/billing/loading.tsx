import { Skeleton } from "@/components/ui/skeleton";
import { Card, CardContent, CardHeader } from "@/components/ui/card";

/**
 * Instant loading UI for Billing. Painted the moment the nav link is clicked so
 * the active nav state and page shell update immediately while the
 * (force-dynamic) server component reads the org's plan over the API. The static
 * heading is real; the plan card + matrix are skeletoned to mirror page.tsx.
 */
export default function BillingLoading() {
  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">Billing</h1>
        <Skeleton className="h-4 w-72 max-w-full" />
      </div>

      {/* Current plan */}
      <Card>
        <CardHeader className="space-y-2">
          <div className="flex items-center justify-between gap-3">
            <Skeleton className="h-5 w-32" />
            <Skeleton className="h-5 w-16 rounded-full" />
          </div>
          <Skeleton className="h-4 w-56 max-w-full" />
        </CardHeader>
        <CardContent>
          <Skeleton className="h-9 w-36" />
        </CardContent>
      </Card>

      {/* Plan matrix */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <Card key={i} className="p-5">
            <Skeleton className="h-5 w-24" />
            <Skeleton className="mt-3 h-7 w-20" />
            <div className="mt-4 space-y-2">
              {Array.from({ length: 4 }).map((_, j) => (
                <Skeleton key={j} className="h-3 w-full" />
              ))}
            </div>
          </Card>
        ))}
      </div>
    </div>
  );
}
