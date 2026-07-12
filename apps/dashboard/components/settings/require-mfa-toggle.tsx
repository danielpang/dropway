"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { AlertTriangle, Loader2, Lock } from "lucide-react";

import { setRequireMfaAction } from "@/app/(app)/settings/actions";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";

/**
 * The org "Require two-factor authentication" control (owner/admin only, flips
 * org_meta.require_mfa via PATCH /v1/orgs/require-mfa).
 *
 * Enforcement is next-request: when it turns on, members WITHOUT two-factor are
 * locked into the setup flow the next time they load the dashboard — so
 * enabling goes through a confirmation spelling that out.
 *
 * `eligible` is the plan gate (business/enterprise, or self-host where
 * everything is unlimited). An ineligible org sees the control locked with an
 * upgrade CTA; the Go API independently re-checks tier on the write (402), so
 * this gate is convenience, not the boundary. An ineligible org with the flag
 * somehow ON (downgraded after enabling) can still turn it OFF.
 */
export function RequireMfaToggle({
  initialEnabled,
  canManage,
  eligible,
}: {
  initialEnabled: boolean;
  canManage: boolean;
  eligible: boolean;
}) {
  const router = useRouter();

  const [enabled, setEnabled] = React.useState(initialEnabled);
  const [pending, setPending] = React.useState(false);
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  async function commit(next: boolean) {
    setError(null);
    setNotice(null);
    setPending(true);
    const result = await setRequireMfaAction({ enabled: next });
    if (result.ok) {
      setEnabled(result.requireMfa);
      setNotice(
        result.requireMfa
          ? "Two-factor authentication is now required. Members without it will be asked to set it up on their next visit."
          : "Two-factor authentication is no longer required for members.",
      );
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  function onToggle(next: boolean) {
    setError(null);
    setNotice(null);
    if (next) {
      // Enabling locks unenrolled members into setup → confirm first.
      setConfirmOpen(true);
      return;
    }
    void commit(false);
  }

  if (!canManage) {
    return (
      <div className="flex items-center justify-between gap-3 text-sm">
        <span className="text-muted-foreground">
          Only owners and admins can change this requirement.
        </span>
        <Badge variant={enabled ? "success" : "muted"} className="font-normal">
          {enabled ? "Required" : "Optional"}
        </Badge>
      </div>
    );
  }

  // Plan-locked and not already on: upsell instead of a dead switch.
  if (!eligible && !enabled) {
    return (
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p className="flex items-center gap-2 text-sm font-medium text-foreground">
            <Lock className="size-4 text-muted-foreground" aria-hidden />
            Require two-factor for all members
          </p>
          <p className="text-sm text-muted-foreground">
            Available on the Business and Enterprise plans. Members can always
            enable two-factor for themselves on any plan.
          </p>
        </div>
        <Button asChild variant="outline" className="shrink-0">
          <Link href="/billing">Upgrade to Business</Link>
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p id="require-mfa-label" className="text-sm font-medium text-foreground">
            Require two-factor for all members
          </p>
          <p id="require-mfa-desc" className="text-sm text-muted-foreground">
            When on, every member must have two-factor authentication set up.
            Members without it are taken to setup on their next visit and
            can&rsquo;t use the dashboard until they finish.
          </p>
        </div>
        <div className="pt-0.5">
          {pending ? (
            <span className="grid size-6 place-items-center text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden />
            </span>
          ) : (
            <Switch
              checked={enabled}
              onCheckedChange={onToggle}
              disabled={pending}
              aria-labelledby="require-mfa-label"
              aria-describedby="require-mfa-desc"
            />
          )}
        </div>
      </div>

      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}
      {notice && (
        <p
          role="status"
          className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-400"
        >
          {notice}
        </p>
      )}

      <Dialog
        open={confirmOpen}
        onOpenChange={(next) => {
          if (!next) setConfirmOpen(false);
        }}
      >
        <DialogHeader>
          <div className="mb-1 grid size-10 place-items-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <AlertTriangle className="size-5" aria-hidden />
          </div>
          <DialogTitle>Require two-factor authentication?</DialogTitle>
          <DialogDescription>
            Members who haven&rsquo;t set up two-factor authentication will be
            required to do so the next time they open the dashboard, before they
            can do anything else. Members who already have it are unaffected.
            You can turn this off any time.
          </DialogDescription>
        </DialogHeader>
        <DialogBody />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setConfirmOpen(false)}
            disabled={pending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={() => {
              setConfirmOpen(false);
              void commit(true);
            }}
            disabled={pending}
            aria-busy={pending}
          >
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Require two-factor
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  );
}
