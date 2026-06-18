"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2, LogOut } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { revokeAccessAction } from "@/app/(app)/members/actions";

/**
 * Org-wide "sign out everywhere" affordance. Owner/admin only.
 *
 * Bumps the KV denylist `revoked:org:<id>.min_iat` so every edge token issued
 * before now is rejected at the serving Worker and the /authz exchange — every
 * viewer of every gated site in the org is forced to re-authenticate. The short
 * (15m) token TTL is the backstop; this makes revocation immediate.
 *
 * If the API build doesn't expose the revoke endpoint yet (404), the control
 * disables itself with an explanatory note rather than erroring.
 */
export function RevokeAccessControl({ organizationId }: { organizationId: string }) {
  const router = useRouter();
  const [open, setOpen] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [unavailable, setUnavailable] = React.useState(false);
  const [doneAt, setDoneAt] = React.useState<number | null>(null);

  async function confirm() {
    setError(null);
    setBusy(true);
    try {
      const res = await revokeAccessAction({ kind: "org", id: organizationId });
      if (res.ok) {
        setDoneAt(Date.now());
        setOpen(false);
        router.refresh();
      } else if (res.unavailable) {
        setUnavailable(true);
        setOpen(false);
      } else {
        setError(res.message);
      }
    } catch {
      setError("Something went wrong. Try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0 space-y-0.5">
          <p className="text-sm font-medium text-foreground">
            Sign out everywhere
          </p>
          <p className="text-sm text-muted-foreground">
            Immediately revoke every active viewer session across all gated sites
            in this organization. Everyone will need to sign in again.
          </p>
        </div>
        <Button
          type="button"
          variant="destructive"
          onClick={() => {
            setError(null);
            setOpen(true);
          }}
          disabled={unavailable}
          className="shrink-0 gap-2"
        >
          <LogOut aria-hidden />
          Revoke access
        </Button>
      </div>

      {unavailable && (
        <p className="text-xs text-muted-foreground">
          Access revocation isn&rsquo;t enabled on this deployment yet.
        </p>
      )}
      {doneAt !== null && !unavailable && (
        <p
          role="status"
          className="text-xs text-emerald-700 dark:text-emerald-400"
        >
          Access revoked. Active sessions across the organization are now invalid.
        </p>
      )}

      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!next && !busy) setOpen(false);
        }}
        label="Revoke organization access"
      >
        <DialogHeader>
          <DialogTitle>Revoke access for everyone?</DialogTitle>
          <DialogDescription>
            This signs out every viewer from every password-, allowlist-, and
            org-only site in this organization. Anyone who still needs access will
            have to sign in again. This can&rsquo;t be undone, but it doesn&rsquo;t
            delete anything.
          </DialogDescription>
        </DialogHeader>
        <DialogBody>
          {error && (
            <p
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}
        </DialogBody>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setOpen(false)}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={confirm}
            disabled={busy}
            aria-busy={busy}
            className="gap-2"
          >
            {busy ? <Loader2 className="animate-spin" aria-hidden /> : <LogOut aria-hidden />}
            Revoke access everywhere
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  );
}
