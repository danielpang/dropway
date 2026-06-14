import Link from "next/link";
import { AlertTriangle } from "lucide-react";

import type { OrgStatus } from "@/lib/api";

/**
 * Non-dismissible account banner shown org-wide when billing has restricted the
 * account (architecture §9):
 *
 *  - over_limit — a downgrade (e.g. canceled to Free) left the org above its
 *    caps. The account is read-only / no-new-resources until they upgrade or
 *    reduce usage. Data is NEVER deleted.
 *  - past_due — a payment failed and dunning lapsed; new actions are restricted
 *    until billing is fixed.
 *
 * It is intentionally NOT dismissible: the restriction persists until the org
 * resolves billing, and it links straight to /billing. The matching write
 * restrictions are enforced server-side by the Go API / cloud quota gate — this
 * banner (and the disabled "New site" control) is the honest UI mirror.
 */
export function OverLimitBanner({ status }: { status: OrgStatus }) {
  if (status !== "over_limit" && status !== "past_due") return null;

  const isPastDue = status === "past_due";

  return (
    <div
      role="alert"
      className="border-b border-amber-500/30 bg-amber-500/10 dark:bg-amber-500/[0.07]"
    >
      <div className="container flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2.5 text-sm">
        <AlertTriangle
          className="size-4 shrink-0 text-amber-600 dark:text-amber-400"
          aria-hidden
        />
        <span className="font-medium text-foreground">
          {isPastDue ? "Payment past due." : "Your organization is over its plan limit."}
        </span>
        <span className="text-muted-foreground">
          {isPastDue
            ? "Update your payment method to restore full access — your sites stay online."
            : "Creating new sites and members is paused until you upgrade or reduce usage. Your data is safe."}
        </span>
        <Link
          href="/billing"
          className="ml-auto font-medium text-amber-700 underline-offset-4 hover:underline dark:text-amber-400"
        >
          {isPastDue ? "Fix billing" : "Review plan"} →
        </Link>
      </div>
    </div>
  );
}
