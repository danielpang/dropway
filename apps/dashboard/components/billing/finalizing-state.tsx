"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { CheckCircle2, Loader2 } from "lucide-react";

import { getBillingPlanAction } from "@/app/(app)/billing/actions";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { PlanTier } from "@/lib/api";
import { TIER_LABEL } from "@/lib/billing";

/**
 * "Finalizing your subscription…" banner, shown after Stripe redirects back to
 * our success_url (`/billing?checkout=success`).
 *
 * CRITICAL: the success redirect grants NOTHING. The paid entitlement
 * (plan_tier) is written to the DB ONLY by the signature-verified Stripe webhook
 * — which arrives asynchronously and may land a beat AFTER the browser returns.
 * So we DON'T trust the redirect; we POLL GET /v1/billing until plan_tier flips
 * off the tier the user had at checkout time. Once it flips we refresh the
 * server component so the whole page re-renders against the new plan.
 *
 * We give up polling after a bounded window and show a gentle "still working"
 * message with a manual refresh — the webhook is reliable but can be delayed,
 * and we never want to spin forever.
 */

const POLL_INTERVAL_MS = 2_500;
const MAX_ATTEMPTS = 24; // ~60s of polling, then fall back to a manual refresh.

export function FinalizingState({
  /** The plan the org had BEFORE checkout — we wait for plan_tier to move off it. */
  previousTier,
}: {
  previousTier: PlanTier;
}) {
  const router = useRouter();
  const [phase, setPhase] = React.useState<"polling" | "done" | "timeout">(
    "polling",
  );
  const [newTier, setNewTier] = React.useState<PlanTier | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    let attempts = 0;
    let timer: ReturnType<typeof setTimeout>;

    async function poll() {
      attempts += 1;
      const result = await getBillingPlanAction();
      if (cancelled) return;

      // The webhook has landed once plan_tier differs from what we started with.
      if (result.ok && result.plan.plan_tier && result.plan.plan_tier !== previousTier) {
        setNewTier(result.plan.plan_tier);
        setPhase("done");
        // Let the success state read for a moment, then re-render the page
        // server-side against the new plan and drop the ?checkout param.
        setTimeout(() => {
          if (!cancelled) {
            router.replace("/billing");
            router.refresh();
          }
        }, 1_200);
        return;
      }

      if (attempts >= MAX_ATTEMPTS) {
        setPhase("timeout");
        return;
      }
      timer = setTimeout(poll, POLL_INTERVAL_MS);
    }

    poll();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [previousTier, router]);

  if (phase === "done") {
    return (
      <Card className="border-emerald-500/30 bg-emerald-500/[0.04]">
        <CardHeader className="flex-row items-center gap-3 space-y-0">
          <CheckCircle2
            className="size-5 text-emerald-600 dark:text-emerald-400"
            aria-hidden
          />
          <div className="space-y-1">
            <CardTitle className="text-base">You&rsquo;re on {TIER_LABEL[newTier ?? "business"]}</CardTitle>
            <CardDescription>
              Your subscription is active. Refreshing your plan…
            </CardDescription>
          </div>
        </CardHeader>
      </Card>
    );
  }

  if (phase === "timeout") {
    return (
      <Card>
        <CardHeader className="flex-row items-center gap-3 space-y-0">
          <Loader2 className="size-5 animate-spin text-muted-foreground" aria-hidden />
          <div className="space-y-1">
            <CardTitle className="text-base">Still finalizing…</CardTitle>
            <CardDescription>
              Stripe confirmed your payment; we&rsquo;re waiting on the final
              confirmation to activate your plan. This usually takes a few
              seconds.
            </CardDescription>
          </div>
        </CardHeader>
        <CardContent>
          <Button
            variant="outline"
            onClick={() => {
              router.replace("/billing");
              router.refresh();
            }}
          >
            Refresh now
          </Button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card aria-live="polite">
      <CardHeader className="flex-row items-center gap-3 space-y-0">
        <Loader2 className="size-5 animate-spin text-muted-foreground" aria-hidden />
        <div className="space-y-1">
          <CardTitle className="text-base">Finalizing your subscription…</CardTitle>
          <CardDescription>
            Payment received. We&rsquo;re activating your plan — this completes
            when Stripe confirms it with us (a moment after the redirect). No
            need to refresh.
          </CardDescription>
        </div>
      </CardHeader>
    </Card>
  );
}
