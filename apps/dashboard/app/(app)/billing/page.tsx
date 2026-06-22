import type { Metadata } from "next";
import Link from "next/link";
import { CreditCard, ShieldAlert, Sparkles } from "lucide-react";

import { ChangePlanDrawer } from "@/components/billing/change-plan-drawer";
import { FinalizingState } from "@/components/billing/finalizing-state";
import { ManageBillingButton } from "@/components/billing/manage-billing-button";
import { PlanMatrix } from "@/components/billing/plan-matrix";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { api, ApiError, type BillingPlan, type PlanTier } from "@/lib/api";
import { SALES_URL, SITE_LIMIT, TIER_LABEL } from "@/lib/billing";
import { canManage, loadActiveOrg } from "@/lib/org";
import { formatBytes } from "@/lib/utils";

export const metadata: Metadata = { title: "Billing" };
export const dynamic = "force-dynamic";

/** Map the Stripe subscription status to a small status pill. */
function statusBadge(plan: BillingPlan) {
  const orgStatus = plan.org_status;
  if (orgStatus === "over_limit") {
    return <Badge variant="outline" className="border-amber-500/40 text-amber-700 dark:text-amber-400">Over limit</Badge>;
  }
  if (orgStatus === "past_due") {
    return <Badge variant="outline" className="border-destructive/40 text-destructive">Past due</Badge>;
  }
  if (orgStatus === "suspended") {
    return <Badge variant="outline" className="border-destructive/40 text-destructive">Suspended</Badge>;
  }
  if (plan.status === "trialing") {
    return <Badge variant="secondary">Trialing</Badge>;
  }
  return <Badge variant="success">Active</Badge>;
}

/**
 * Billing settings (CLOUD-ONLY). Owners/admins manage the org's
 * plan here:
 *  - current plan + status (read from GET /v1/billing → app.org_meta);
 *  - the plan/limits matrix with the current tier highlighted;
 *  - "Manage billing" → Stripe Billing Portal (self-serve plan switch / cancel);
 *  - upgrade buttons → Stripe Checkout for any self-serve tier above the current
 *    one (Pro $25, Business $150), or Contact Sales for the "Custom" Enterprise tier.
 *
 * After returning from Stripe's success_url (`?checkout=success`) we DON'T trust
 * the redirect for entitlement, we show a "finalizing…" state that POLLS the
 * plan until the signed webhook flips plan_tier. The Go API re-checks
 * owner/admin on every write; the role gate here is UX only.
 *
 * On the OSS/self-host build /v1/billing 404s; we degrade to a "not available"
 * notice (self-host is unlimited and has no Stripe).
 */
export default async function BillingPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  const checkoutReturn = sp.checkout; // 'success' | 'cancel' | undefined

  const org = await loadActiveOrg();
  const manage = org ? canManage(org.myRole) : false;

  // Read the authoritative plan. A 404 means the cloud build isn't present
  // (self-host), billing simply doesn't apply.
  let plan: BillingPlan | null = null;
  try {
    plan = await api.getBilling();
  } catch (err) {
    // 404 = self-host (no cloud billing); anything else = transient. Either way
    // we can't render a plan, so fall through to the "not available" notice.
    void (err instanceof ApiError);
  }

  if (!plan) {
    return (
      <Shell>
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          Billing isn&rsquo;t available on this deployment. Self-hosted Dropway is
          unlimited and has no plans to manage.
        </Card>
      </Shell>
    );
  }

  const currentTier: PlanTier = plan.plan_tier ?? "free";
  // Show "Manage billing" (→ Stripe portal: card, invoices, cancel) whenever the org
  // has a Stripe customer to manage: a paid tier OR an existing subscription row
  // (plan.status is set only when a row exists — active/past_due/canceled/…). Gating
  // on the customer rather than `tier !== "free"` also surfaces the portal for
  // cancellations, past_due, and the case where the tier still reads Free but a
  // subscription exists, so a paying user is never stranded without a way to manage it.
  const hasBillingCustomer = currentTier !== "free" || Boolean(plan.status);
  const isCheckoutReturn = checkoutReturn === "success";
  const isCheckoutCancel = checkoutReturn === "cancel";

  // Live usage for the meter: site count + total storage across the org's sites.
  // Best-effort — if the sites list can't be read we just omit the Usage card
  // rather than failing the billing page.
  const sites = await api.listSites().catch(() => null);
  const usage =
    sites != null
      ? {
          siteCount: sites.length,
          siteLimit: SITE_LIMIT[currentTier],
          storageBytes: sites.reduce((sum, s) => sum + (s.storage_bytes ?? 0), 0),
        }
      : null;

  // Members can view billing (it drives banners + CTAs) but cannot mutate it.
  if (!manage) {
    return (
      <Shell>
        <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-4 py-3 text-sm">
          <span className="flex items-start gap-3">
            <ShieldAlert
              className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400"
              aria-hidden
            />
            <span className="text-muted-foreground">
              Only owners and admins can change billing. Your organization is on
              the{" "}
              <span className="font-medium text-foreground">
                {TIER_LABEL[currentTier]}
              </span>{" "}
              plan.
            </span>
          </span>
        </div>
        <PlanMatrix currentTier={currentTier} />
      </Shell>
    );
  }

  return (
    <Shell>
      {/*
        Returning from Stripe Checkout. The success_url redirect grants NOTHING:
        the entitlement (plan_tier) is written ONLY by the signed webhook, which
        may land a beat after the browser returns. So we poll the plan until it
        flips, then re-render against the new plan.
      */}
      {isCheckoutReturn && <FinalizingState previousTier={currentTier} />}
      {isCheckoutCancel && (
        <div className="rounded-md border border-border bg-muted/40 px-4 py-3 text-sm text-muted-foreground">
          Checkout canceled. No changes were made to your plan.
        </div>
      )}

      {/* Current plan */}
      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="space-y-1">
              <CardTitle className="flex items-center gap-2 text-base">
                <Sparkles className="size-4 text-muted-foreground" aria-hidden />
                Current plan
              </CardTitle>
              <CardDescription>
                {org?.name ? `Plan for ${org.name}.` : "Your organization's plan."}{" "}
                Entitlement is set by Stripe and synced automatically.
              </CardDescription>
            </div>
            {statusBadge(plan)}
          </div>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
            <div>
              <p className="text-xs text-muted-foreground">Plan</p>
              <p className="text-2xl font-semibold tracking-tight">
                {TIER_LABEL[currentTier]}
              </p>
            </div>
            {/* Seat-free pricing: team members are unlimited on
                every plan, so we don't surface a seat count, billing is a flat fee
                per workspace and the upgrade lever is the per-org site count. */}
          </div>

          <div className="flex flex-wrap items-center gap-3">
            {/* Orgs with a Stripe customer manage/switch/cancel/update-card via the
                Stripe portal. A single "Upgrade" opens the change-plan drawer (every
                tier + the right CTA); Enterprise has nothing higher, so it only gets
                Manage billing. */}
            {hasBillingCustomer && <ManageBillingButton />}
            {currentTier !== "enterprise" && (
              <ChangePlanDrawer currentTier={currentTier} />
            )}
          </div>
        </CardContent>
      </Card>

      {/* Usage against the current plan's limits */}
      {usage && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Usage</CardTitle>
            <CardDescription>
              What {org?.name ?? "this organization"} is using on its current
              plan.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            {/* Sites: count vs the tier's site cap (Business/Enterprise unlimited). */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between text-sm">
                <span className="text-muted-foreground">Sites</span>
                <span className="font-medium tabular-nums">
                  <span
                    className={
                      usage.siteLimit != null && usage.siteCount > usage.siteLimit
                        ? "text-destructive"
                        : "text-foreground"
                    }
                  >
                    {usage.siteCount}
                  </span>
                  <span className="text-muted-foreground">
                    {" / "}
                    {usage.siteLimit ?? "Unlimited"}
                  </span>
                </span>
              </div>
              {usage.siteLimit != null && (
                <Progress
                  value={(usage.siteCount / usage.siteLimit) * 100}
                />
              )}
            </div>

            {/* Storage: cumulative across all of the org's sites. */}
            <div className="flex items-center justify-between text-sm">
              <span className="text-muted-foreground">Storage used</span>
              <span className="font-medium tabular-nums text-foreground">
                {formatBytes(usage.storageBytes)}
              </span>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Plan/limits matrix */}
      <section className="space-y-3">
        <div className="space-y-1">
          <h2 className="text-lg font-semibold tracking-tight">Plans &amp; limits</h2>
          <p className="text-sm text-muted-foreground">
            Upgrading raises your limits automatically. Caps are enforced live
            against your current plan.
          </p>
        </div>
        <PlanMatrix currentTier={currentTier} canManage />
        <p className="text-xs text-muted-foreground">
          Need more than Enterprise?{" "}
          <Button asChild variant="link" className="h-auto p-0 text-xs">
            <Link href={SALES_URL} target="_blank" rel="noopener noreferrer">
              Talk to sales
            </Link>
          </Button>
          .
        </p>
      </section>
    </Shell>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <div className="flex items-center gap-2">
        <CreditCard className="size-5 text-muted-foreground" aria-hidden />
        <h1 className="text-2xl font-semibold tracking-tight">Billing</h1>
      </div>
      {children}
    </div>
  );
}
