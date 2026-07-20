"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle, Loader2, Trash2 } from "lucide-react";

import { deleteSiteAction } from "@/app/(app)/sites/[id]/settings/actions";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

/**
 * The site "Danger zone": a permanent delete gated by a two-step confirmation.
 * Opening the dialog is the first warning; the destructive button stays disabled
 * until the user types the site's exact slug, so a stray click can't destroy a
 * site (the GitHub "type the name" pattern). The Go API re-checks owner/admin, so
 * this is UX, not the authz boundary. On success the site is gone, so we send the
 * user back to the dashboard.
 */
export function DeleteSite({ siteId, slug }: { siteId: string; slug: string }) {
  const router = useRouter();
  const [open, setOpen] = React.useState(false);
  const [typed, setTyped] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const confirmed = typed.trim() === slug;

  function reset() {
    setTyped("");
    setError(null);
    setPending(false);
  }

  async function onDelete() {
    if (!confirmed || pending) return;
    setPending(true);
    setError(null);
    const res = await deleteSiteAction({ siteId });
    if (!res.ok) {
      setError(res.message);
      setPending(false);
      return;
    }
    // The site no longer exists; leave the (now-404) detail route for the list.
    router.replace("/dashboard");
    router.refresh();
  }

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-1">
          <p className="text-sm font-medium text-foreground">
            Delete this site
          </p>
          <p className="text-sm text-muted-foreground">
            Permanently remove the site, every version, and its live URL. This
            cannot be undone.
          </p>
        </div>
        <Button
          variant="destructive"
          onClick={() => {
            reset();
            setOpen(true);
          }}
          className="shrink-0"
        >
          <Trash2 className="size-4" aria-hidden />
          Delete site
        </Button>
      </div>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (pending) return; // don't close mid-delete
          setOpen(next);
          if (!next) reset();
        }}
        label="Delete site"
      >
        <DialogHeader>
          <div className="mb-1 grid size-10 place-items-center rounded-lg bg-destructive/10 text-destructive">
            <AlertTriangle className="size-5" aria-hidden />
          </div>
          <DialogTitle>Delete {slug}?</DialogTitle>
          <DialogDescription>
            This permanently deletes the site and{" "}
            <strong>all its versions</strong>, and its live URL will stop
            working immediately. This action cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <DialogBody>
          <Label htmlFor="confirm-slug" className="text-sm">
            Type{" "}
            <span className="font-mono font-medium text-foreground">
              {slug}
            </span>{" "}
            to confirm.
          </Label>
          <Input
            id="confirm-slug"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            placeholder={slug}
            className="mt-2 font-mono"
            aria-invalid={typed.length > 0 && !confirmed}
            onKeyDown={(e) => {
              if (e.key === "Enter" && confirmed) void onDelete();
            }}
          />
          {error && (
            <p className="mt-2 text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
        </DialogBody>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
              reset();
            }}
            disabled={pending}
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={() => void onDelete()}
            disabled={!confirmed || pending}
          >
            {pending ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <Trash2 className="size-4" aria-hidden />
            )}
            Delete site
          </Button>
        </DialogFooter>
      </Dialog>
    </>
  );
}
