"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Check, History, Loader2 } from "lucide-react";

import { publishVersionAction } from "@/app/(app)/sites/[id]/actions";
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
import type { SiteVersion } from "@/lib/api";
import { cn, formatBytes } from "@/lib/utils";

/**
 * Rollback (re-publish) affordance on the site detail page. Instead of asking for a
 * version id, it shows the site's deploy history (fetched server-side and passed in)
 * and lets the user pick a version to make live. Publishing an older version IS the
 * rollback in Phase 1 — the API flips the live pointer and the URL updates instantly.
 */
export function RollbackDialog({
  siteId,
  versions,
}: {
  siteId: string;
  versions: SiteVersion[];
}) {
  const router = useRouter();

  const [open, setOpen] = React.useState(false);
  const [selected, setSelected] = React.useState<string | null>(null);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  function reset() {
    setSelected(null);
    setError(null);
    setPending(false);
  }

  const selectedVersion = versions.find((v) => v.id === selected) ?? null;

  async function onSubmit() {
    if (!selectedVersion?.id || selectedVersion.is_current) return;
    setError(null);
    setPending(true);
    const result = await publishVersionAction({
      siteId,
      versionId: selectedVersion.id,
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
        className="max-w-lg"
      >
        <DialogHeader>
          <DialogTitle>Version history</DialogTitle>
          <DialogDescription>
            Pick a version to make live. Rolling back is just publishing an
            earlier version — the live URL flips instantly.
          </DialogDescription>
        </DialogHeader>

        <DialogBody>
          {versions.length === 0 ? (
            <p className="rounded-md border border-dashed border-border px-3 py-6 text-center text-sm text-muted-foreground">
              No versions yet. Deploy this site to create one.
            </p>
          ) : (
            <ul
              role="radiogroup"
              aria-label="Versions"
              className="max-h-[50vh] space-y-1.5 overflow-y-auto"
            >
              {versions.map((v) => {
                const ready = v.status === "ready";
                const selectable = ready && !v.is_current;
                const isSel = v.id === selected;
                return (
                  <li key={v.id ?? v.version_no}>
                    <button
                      type="button"
                      role="radio"
                      aria-checked={isSel}
                      disabled={!selectable || pending}
                      onClick={() => v.id && setSelected(v.id)}
                      className={cn(
                        "flex w-full items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors",
                        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
                        isSel
                          ? "border-primary bg-primary/5"
                          : "border-border",
                        selectable
                          ? "hover:border-foreground/30 cursor-pointer"
                          : "cursor-default opacity-70",
                      )}
                    >
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="text-sm font-medium text-foreground">
                            Version {v.version_no}
                          </span>
                          {v.is_current && <Badge variant="success">Live</Badge>}
                          {!ready && (
                            <Badge variant="muted" className="capitalize">
                              {v.status ?? "pending"}
                            </Badge>
                          )}
                        </div>
                        <p className="mt-0.5 truncate text-xs text-muted-foreground">
                          {v.created_at
                            ? new Date(v.created_at).toLocaleString()
                            : "—"}
                          {typeof v.size_bytes === "number" &&
                            ` · ${formatBytes(v.size_bytes)}`}
                        </p>
                      </div>
                      {isSel && (
                        <Check
                          className="size-4 shrink-0 text-primary"
                          aria-hidden
                        />
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          )}

          {error && (
            <p
              role="alert"
              className="mt-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}
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
            type="button"
            onClick={() => void onSubmit()}
            disabled={pending || !selectedVersion || selectedVersion.is_current}
            aria-busy={pending}
          >
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Publish version
          </Button>
        </DialogFooter>
      </Dialog>
    </>
  );
}
