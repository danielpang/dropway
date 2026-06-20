import { Check } from "lucide-react";

import {
  ContactSalesButton,
  UpgradeButton,
} from "@/components/billing/upgrade-button";
import { ManageBillingButton } from "@/components/billing/manage-billing-button";
import { Badge } from "@/components/ui/badge";
import type { CheckoutTier, PlanTier } from "@/lib/api";
import {
  MATRIX_TIERS,
  PLAN_MATRIX,
  planAction,
  SALES_URL,
  TIER_LABEL,
} from "@/lib/billing";
import { cn } from "@/lib/utils";

/**
 * The plan/limits matrix, highlighting the org's current tier. Display-only — the
 * real caps are enforced server-side in cloud/quota, and paying raises them
 * automatically once the webhook syncs plan_tier. When `canManage`, a CTA row is
 * appended under each column (Upgrade to Pro/Business, Contact Sales, or the
 * current-plan marker) so owners/admins can act straight from the comparison.
 */
export function PlanMatrix({
  currentTier,
  canManage = false,
}: {
  currentTier: PlanTier;
  canManage?: boolean;
}) {
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
        {canManage && (
          <tfoot>
            <tr className="border-t border-border">
              <td className="px-4 py-3" />
              {MATRIX_TIERS.map((tier) => (
                <td
                  key={tier}
                  className={cn(
                    "px-4 py-3 align-top",
                    tier === currentTier && "bg-secondary/30",
                  )}
                >
                  <MatrixCta tier={tier} currentTier={currentTier} />
                </td>
              ))}
            </tr>
          </tfoot>
        )}
      </table>
    </div>
  );
}

/**
 * The per-column action: upgrade (Checkout), contact sales, downgrade (portal),
 * or the current-plan marker.
 */
function MatrixCta({
  tier,
  currentTier,
}: {
  tier: PlanTier;
  currentTier: PlanTier;
}) {
  switch (planAction(tier, currentTier)) {
    case "current":
      return (
        <span className="inline-flex items-center text-xs font-medium text-muted-foreground">
          Current plan
        </span>
      );
    case "upgrade":
      return <UpgradeButton targetTier={tier as CheckoutTier} block />;
    case "contact":
      return <ContactSalesButton salesUrl={SALES_URL} block />;
    case "downgrade":
      return (
        <ManageBillingButton
          block
          hideIcon
          label={`Downgrade to ${TIER_LABEL[tier]}`}
        />
      );
  }
}
