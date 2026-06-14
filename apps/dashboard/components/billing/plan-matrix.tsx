import { Check } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { PlanTier } from "@/lib/api";
import { MATRIX_TIERS, PLAN_MATRIX, TIER_LABEL } from "@/lib/billing";

/**
 * The plan/limits matrix (architecture §9 bands), highlighting the org's current
 * tier. Display-only — the real caps are enforced server-side in cloud/quota,
 * and paying raises them automatically once the webhook syncs plan_tier. The
 * matrix simply makes "what does upgrading get me" legible.
 */
export function PlanMatrix({ currentTier }: { currentTier: PlanTier }) {
  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full border-collapse text-sm">
        <caption className="sr-only">Plan limits by tier</caption>
        <thead>
          <tr className="border-b border-border bg-muted/40">
            <th
              scope="col"
              className="px-4 py-3 text-left font-medium text-muted-foreground"
            >
              Feature
            </th>
            {MATRIX_TIERS.map((tier) => {
              const isCurrent = tier === currentTier;
              return (
                <th
                  scope="col"
                  key={tier}
                  className={cn(
                    "px-4 py-3 text-left font-semibold text-foreground",
                    isCurrent && "bg-secondary/60",
                  )}
                >
                  <span className="flex items-center gap-2">
                    {TIER_LABEL[tier]}
                    {isCurrent && (
                      <Badge variant="success" className="gap-1">
                        <Check className="size-3" aria-hidden />
                        Current
                      </Badge>
                    )}
                  </span>
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {PLAN_MATRIX.map((row) => (
            <tr key={row.label} className="border-b border-border last:border-0">
              <th
                scope="row"
                className="px-4 py-2.5 text-left font-normal text-muted-foreground"
              >
                {row.label}
              </th>
              {MATRIX_TIERS.map((tier) => (
                <td
                  key={tier}
                  className={cn(
                    "px-4 py-2.5 tabular-nums text-foreground",
                    tier === currentTier && "bg-secondary/30",
                  )}
                >
                  {row.values[tier]}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
