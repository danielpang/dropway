"use client";

import * as React from "react";
import { ArrowUpRight, Check, Sparkles } from "lucide-react";

import {
  ContactSalesButton,
  UpgradeButton,
} from "@/components/billing/upgrade-button";
import { ManageBillingButton } from "@/components/billing/manage-billing-button";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Sheet, SheetBody, SheetHeader, SheetTitle } from "@/components/ui/sheet";
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
 * Supabase-style "change plan" panel: a single Upgrade button opens a side drawer
 * with a card per tier (price, positioning, the right CTA for where the org is,
 * and feature highlights). Replaces the row of separate "Upgrade to X" buttons.
 * The actual entitlement still flows Checkout → signed webhook → plan_tier; this is
 * just the chooser.
 */

const POSITIONING: Record<PlanTier, string> = {
  free: "For personal projects and quick shares.",
  pro: "A flat price per workspace, for teams shipping every day.",
  business: "For teams that have outgrown 100 sites.",
  enterprise: "Security, compliance, and scale.",
};

const HIGHLIGHTS: Record<PlanTier, string[]> = {
  free: [
    "Up to 10 sites",
    "Unlimited team members",
    "Public, password, allowlist & org-only sharing",
    "Deploy via dashboard, CLI & MCP",
  ],
  pro: [
    "Everything in Free",
    "Up to 100 sites",
    "Custom domains",
    "Version history & instant rollback",
    "Priority email support",
  ],
  business: [
    "Everything in Pro",
    "Unlimited sites",
    "Custom domains",
    "Priority email support",
  ],
  enterprise: [
    "Everything in Business",
    "SSO / SAML & SCIM",
    "Audit logs & advanced RBAC",
    "99.9% SLA, DPA & priority support",
  ],
};

/** "Most popular" tier highlighted in the chooser. */
const POPULAR_TIER: PlanTier = "pro";

const PRICE_ROW = PLAN_MATRIX.find((r) => r.label === "Price");
const priceOf = (tier: PlanTier) => PRICE_ROW?.values[tier] ?? "";

const LANDING_PRICING = process.env.NEXT_PUBLIC_LANDING_URL
  ? `${process.env.NEXT_PUBLIC_LANDING_URL.replace(/\/$/, "")}/#pricing`
  : undefined;

export function ChangePlanDrawer({
  currentTier,
  triggerLabel = "Upgrade",
}: {
  currentTier: PlanTier;
  triggerLabel?: string;
}) {
  const [open, setOpen] = React.useState(false);

  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Sparkles aria-hidden />
        {triggerLabel}
      </Button>

      <Sheet open={open} onOpenChange={setOpen} label="Change subscription plan">
        <SheetHeader>
          <div className="flex items-center justify-between gap-4">
            <div className="space-y-1">
              <SheetTitle>Change subscription plan</SheetTitle>
              <p className="text-sm text-muted-foreground">
                Pay for capacity, not seats. Team members are unlimited on every
                plan.
              </p>
            </div>
            {LANDING_PRICING && (
              <Button asChild variant="outline" size="sm">
                <a href={LANDING_PRICING} target="_blank" rel="noopener noreferrer">
                  Pricing
                  <ArrowUpRight aria-hidden />
                </a>
              </Button>
            )}
          </div>
        </SheetHeader>

        <SheetBody>
          <div className="grid gap-4 sm:grid-cols-2">
            {MATRIX_TIERS.map((tier) => (
              <PlanCard key={tier} tier={tier} currentTier={currentTier} />
            ))}
          </div>
        </SheetBody>
      </Sheet>
    </>
  );
}

function PlanCard({
  tier,
  currentTier,
}: {
  tier: PlanTier;
  currentTier: PlanTier;
}) {
  const action = planAction(tier, currentTier);
  const isCurrent = action === "current";
  const isPopular = tier === POPULAR_TIER && !isCurrent;

  return (
    <div
      className={cn(
        "flex flex-col rounded-xl border bg-card p-5",
        isPopular ? "border-primary/50 shadow-sm" : "border-border",
      )}
    >
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-base font-semibold tracking-tight">
          {TIER_LABEL[tier]}
        </h3>
        {isCurrent && (
          <Badge variant="success" className="gap-1">
            <Check className="size-3" aria-hidden />
            Current
          </Badge>
        )}
        {isPopular && <Badge variant="secondary">Most popular</Badge>}
      </div>

      <p className="mt-3 text-2xl font-semibold tracking-tight">
        {priceOf(tier)}
      </p>
      <p className="mt-1 text-sm text-muted-foreground">{POSITIONING[tier]}</p>

      <div className="mt-4">
        <PlanCta tier={tier} action={action} />
      </div>

      <ul className="mt-5 space-y-2 text-sm">
        {HIGHLIGHTS[tier].map((feature) => (
          <li key={feature} className="flex items-start gap-2 text-foreground/90">
            <Check className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden />
            {feature}
          </li>
        ))}
      </ul>
    </div>
  );
}

function PlanCta({
  tier,
  action,
}: {
  tier: PlanTier;
  action: ReturnType<typeof planAction>;
}) {
  switch (action) {
    case "current":
      return (
        <Button variant="outline" disabled className="w-full">
          Current plan
        </Button>
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
