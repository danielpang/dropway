"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle, Loader2 } from "lucide-react";

import { setAllowExternalSharingAction } from "@/app/(app)/settings/actions";
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
 * The org `allow_external_sharing` toggle. Enabling widens what's permitted and
 * applies immediately. Disabling is destructive to existing shares, it
 * downgrades public sites to org-only and revokes external grants, so it goes
 * through a confirmation dialog first, then reports how many sites were
 * downgraded (the count the Go API returns from the reconcile).
 *
 * The switch renders the org's LIVE allow_external_sharing value (fetched
 * server-side by the settings page via GET /v1/orgs/policy and passed in as
 * `initialEnabled`), then re-syncs to the authoritative value each PUT returns. It
 * no longer hardcodes OFF, which previously misrepresented an org that already had
 * external sharing ON (H10).
 */
export function ExternalSharingToggle({
  initialEnabled,
}: {
  initialEnabled: boolean;
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
    const result = await setAllowExternalSharingAction({ enabled: next });
    if (result.ok) {
      const value = result.result.allow_external_sharing ?? next;
      setEnabled(value);
      const downgraded = result.result.downgraded_sites ?? 0;
      if (!value && downgraded > 0) {
        setNotice(
          `External sharing disabled. ${downgraded} site${downgraded === 1 ? "" : "s"} downgraded to org-only.`,
        );
      } else {
        setNotice(
          value
            ? "External sharing enabled."
            : "External sharing disabled.",
        );
      }
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  function onToggle(next: boolean) {
    setError(null);
    setNotice(null);
    if (!next) {
      // Disabling is destructive → confirm first.
      setConfirmOpen(true);
      return;
    }
    void commit(true);
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p id="ext-sharing-label" className="text-sm font-medium text-foreground">
            Allow sharing outside the organization
          </p>
          <p id="ext-sharing-desc" className="text-sm text-muted-foreground">
            When on, members can make sites public or add external email
            addresses to a site&rsquo;s allowlist.
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
              aria-labelledby="ext-sharing-label"
              aria-describedby="ext-sharing-desc"
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
          <DialogTitle>Disable external sharing?</DialogTitle>
          <DialogDescription>
            This downgrades every public site to org-only and revokes external
            allowlist grants. Shared external links stop working, and you&rsquo;d
            re-share each site.
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
            variant="destructive"
            onClick={() => {
              setConfirmOpen(false);
              void commit(false);
            }}
            disabled={pending}
            aria-busy={pending}
          >
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Disable sharing
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  );
}
