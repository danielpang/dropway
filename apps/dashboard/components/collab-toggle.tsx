"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { Switch } from "@/components/ui/switch";

/** The shape every per-resource collab server action resolves to. */
export type CollabActionResult =
  | { ok: true; allowMemberEdits: boolean }
  | { ok: false; message: string };

/**
 * The per-resource collaboration toggle: "Allow non-creators to modify"
 * (`allow_member_edits`, default true). One component serves sites, skills,
 * and chat logs — the caller passes the matching server action. When off,
 * content edits (deploys, uploads, appends…) are restricted to the creator and
 * org admins; deletion and security settings are governed separately.
 *
 * Flipping the toggle is itself creator-or-admin (the Go API re-checks and
 * 403s), so callers pass `disabled` for everyone else and the switch renders
 * the live state read-only. Re-syncs to the authoritative value each PUT
 * returns.
 */
export function CollabToggle({
  resourceId,
  initialAllow,
  disabled,
  action,
}: {
  resourceId: string;
  initialAllow: boolean;
  disabled: boolean;
  action: (input: {
    id: string;
    allowMemberEdits: boolean;
  }) => Promise<CollabActionResult>;
}) {
  const router = useRouter();

  const [allow, setAllow] = React.useState(initialAllow);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  async function onToggle(next: boolean) {
    setError(null);
    setNotice(null);
    setPending(true);
    const result = await action({ id: resourceId, allowMemberEdits: next });
    if (result.ok) {
      setAllow(result.allowMemberEdits);
      setNotice(
        result.allowMemberEdits
          ? "Anyone in your organization can edit this."
          : "Editing is restricted to you and org admins.",
      );
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  const labelId = `collab-label-${resourceId}`;
  const descId = `collab-desc-${resourceId}`;

  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p id={labelId} className="text-sm font-medium text-foreground">
            Allow non-creators to modify
          </p>
          <p id={descId} className="text-sm text-muted-foreground">
            Anyone in your organization can edit this. Turn off to restrict
            editing to you and org admins. Deleting and security settings are
            unaffected.
          </p>
        </div>
        <div className="pt-0.5">
          {pending ? (
            <span className="grid size-6 place-items-center text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden />
            </span>
          ) : (
            <Switch
              checked={allow}
              onCheckedChange={onToggle}
              disabled={disabled || pending}
              aria-labelledby={labelId}
              aria-describedby={descId}
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
    </div>
  );
}
