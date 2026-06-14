"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { History, Loader2 } from "lucide-react";

import { publishVersionAction } from "@/app/(app)/sites/[id]/actions";
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

/** UUID v4-ish shape check so we fail fast before the round-trip. */
const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * Rollback (re-publish) affordance on the site detail page. Publishing an older
 * version_id IS the rollback in Phase 1 (the API flips the live pointer). The
 * caller passes the current live version so we can warn when it's re-entered.
 */
export function RollbackDialog({
  siteId,
  currentVersionId,
}: {
  siteId: string;
  currentVersionId: string | null;
}) {
  const router = useRouter();

  const [open, setOpen] = React.useState(false);
  const [versionId, setVersionId] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [touched, setTouched] = React.useState(false);

  const valid = UUID_RE.test(versionId.trim());
  const isCurrent = versionId.trim() === currentVersionId;

  function reset() {
    setVersionId("");
    setError(null);
    setTouched(false);
    setPending(false);
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setTouched(true);
    if (!valid) return;

    setPending(true);
    const result = await publishVersionAction({
      siteId,
      versionId: versionId.trim(),
    });

    if (result.ok) {
      setOpen(false);
      reset();
      router.refresh();
      return;
    }
    setError(result.message);
    setPending(false);
  }

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <History aria-hidden />
        Roll back
      </Button>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) reset();
        }}
      >
        <DialogHeader>
          <DialogTitle>Publish a version</DialogTitle>
          <DialogDescription>
            Paste a version id to make it live. Rolling back is just publishing
            an earlier version — the live URL flips instantly.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={onSubmit}>
          <DialogBody>
            <div className="space-y-2">
              <Label htmlFor="version-id">Version id</Label>
              <Input
                id="version-id"
                name="version-id"
                placeholder="00000000-0000-0000-0000-000000000000"
                value={versionId}
                onChange={(e) => setVersionId(e.target.value)}
                aria-invalid={touched && !valid}
                className="font-mono text-xs"
                autoFocus
                disabled={pending}
              />
              {touched && !valid && (
                <p className="text-xs text-destructive">
                  Enter a valid version id (UUID).
                </p>
              )}
              {valid && isCurrent && (
                <p className="text-xs text-muted-foreground">
                  This is already the live version.
                </p>
              )}
              {error && (
                <p
                  role="alert"
                  className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
                >
                  {error}
                </p>
              )}
            </div>
          </DialogBody>

          <DialogFooter>
            <Button
              type="button"
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
              type="submit"
              disabled={pending || (valid && isCurrent)}
              aria-busy={pending}
            >
              {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              Publish version
            </Button>
          </DialogFooter>
        </form>
      </Dialog>
    </>
  );
}
