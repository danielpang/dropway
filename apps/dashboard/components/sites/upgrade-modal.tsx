"use client";

import * as React from "react";
import { ArrowUpRight, Zap } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { QuotaExceeded } from "@/lib/api";

/** Human label for each capped resource the API can report. */
const RESOURCE_LABEL: Record<string, string> = {
  sites_per_user: "sites",
  members_per_org: "team members",
};

/**
 * Upgrade modal PLACEHOLDER (Phase 1: billing is not wired). It renders the 402
 * `quota.ExceededError` body returned by the Go API so the user understands the
 * cap they hit and where to go next. The `upgrade_url` / `sales_url` come from
 * the API; until billing ships they may be absent, in which case the CTA is a
 * soft "contact us" affordance.
 */
export function UpgradeModal({
  quota,
  open,
  onOpenChange,
}: {
  quota: QuotaExceeded | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  if (!quota) return null;

  const resource = quota.limit ? RESOURCE_LABEL[quota.limit] ?? quota.limit : "resources";
  const href = quota.upgrade_url ?? quota.sales_url;
  const ctaLabel = quota.upgrade_url
    ? "Upgrade plan"
    : quota.sales_url
      ? "Contact sales"
      : null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogHeader>
        <div className="mb-1 grid size-10 place-items-center rounded-lg bg-secondary text-secondary-foreground">
          <Zap className="size-5" aria-hidden />
        </div>
        <DialogTitle>You&rsquo;ve hit your plan limit</DialogTitle>
        <DialogDescription>
          {`Your ${quota.plan_tier ?? "current"} plan allows ${
            quota.max ?? "a limited number of"
          } ${resource}.`}
        </DialogDescription>
      </DialogHeader>

      <DialogBody>
        <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-border bg-border text-sm">
          <Stat label="In use" value={String(quota.current ?? "—")} />
          <Stat label="Included" value={String(quota.max ?? "—")} />
        </dl>
        {quota.next_tier && (
          <p className="mt-3 text-sm text-muted-foreground">
            Move to the{" "}
            <span className="font-medium text-foreground">{quota.next_tier}</span>{" "}
            plan to raise this limit.
          </p>
        )}
      </DialogBody>

      <DialogFooter>
        <Button variant="outline" onClick={() => onOpenChange(false)}>
          Not now
        </Button>
        {href && ctaLabel ? (
          <Button asChild>
            <a href={href} target="_blank" rel="noopener noreferrer">
              {ctaLabel}
              <ArrowUpRight aria-hidden />
            </a>
          </Button>
        ) : (
          // Billing not wired yet (Phase 1): keep a disabled, honest CTA.
          <Button disabled title="Billing is coming soon">
            Upgrade plan
          </Button>
        )}
      </DialogFooter>
    </Dialog>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-card p-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 text-lg font-semibold tabular-nums text-foreground">
        {value}
      </dd>
    </div>
  );
}
