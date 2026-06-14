"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Check, Loader2, Plus, Trash2 } from "lucide-react";

import {
  addAllowlistAction,
  removeAllowlistAction,
} from "@/app/(app)/sites/[id]/settings/actions";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { AllowlistEntry } from "@/lib/api";

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/**
 * Manage a site's allowlist of viewer emails (architecture §6). Adding an email
 * the Go API deems "external" (not on an org-verified domain) is rejected unless
 * the org allows external sharing — the action surfaces that 403 as a message.
 * Each row shows whether the grant has been claimed (a verified account signed
 * in and matched) and whether it's external.
 */
export function AllowlistManager({
  siteId,
  initialEntries,
  disabled,
}: {
  siteId: string;
  initialEntries: AllowlistEntry[];
  disabled: boolean;
}) {
  const router = useRouter();
  const [entries, setEntries] = React.useState<AllowlistEntry[]>(initialEntries);
  const [email, setEmail] = React.useState("");
  const [adding, setAdding] = React.useState(false);
  const [busyEmail, setBusyEmail] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [touched, setTouched] = React.useState(false);

  // Keep in sync if the server re-renders with fresh data.
  React.useEffect(() => setEntries(initialEntries), [initialEntries]);

  const emailValid = EMAIL_RE.test(email);

  async function onAdd(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setTouched(true);
    if (!emailValid) return;

    setAdding(true);
    const result = await addAllowlistAction({ siteId, email });
    if (result.ok) {
      if (result.entry) {
        const added = result.entry;
        setEntries((prev) => {
          const without = prev.filter((p) => p.email !== added.email);
          return [...without, added];
        });
      }
      setEmail("");
      setTouched(false);
      router.refresh();
    } else {
      setError(result.message);
    }
    setAdding(false);
  }

  async function onRemove(target: string) {
    setError(null);
    setBusyEmail(target);
    const result = await removeAllowlistAction({ siteId, email: target });
    if (result.ok) {
      setEntries((prev) => prev.filter((p) => p.email !== target));
      router.refresh();
    } else {
      setError(result.message);
    }
    setBusyEmail(null);
  }

  return (
    <div className="space-y-4">
      {!disabled && (
        <form onSubmit={onAdd} className="space-y-2">
          <Label htmlFor="allowlist-email" className="sr-only">
            Email to allow
          </Label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input
              id="allowlist-email"
              name="allowlist-email"
              type="email"
              inputMode="email"
              autoComplete="off"
              placeholder="viewer@company.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              aria-invalid={touched && !emailValid}
              disabled={adding}
            />
            <Button type="submit" disabled={adding} aria-busy={adding}>
              {adding ? (
                <Loader2 className="animate-spin" aria-hidden />
              ) : (
                <Plus aria-hidden />
              )}
              Add
            </Button>
          </div>
          {touched && !emailValid && (
            <p className="text-xs text-destructive">
              Enter a valid email address.
            </p>
          )}
        </form>
      )}

      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}

      {entries.length === 0 ? (
        <p className="rounded-md border border-dashed border-border px-3 py-6 text-center text-sm text-muted-foreground">
          No one is on the allowlist yet.
        </p>
      ) : (
        <ul className="divide-y divide-border rounded-md border border-border">
          {entries.map((entry) => {
            const busy = busyEmail === entry.email;
            return (
              <li
                key={entry.email}
                className="flex items-center justify-between gap-3 px-3 py-2.5"
              >
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-sm text-foreground">
                    {entry.email}
                  </span>
                  {entry.is_external && (
                    <Badge variant="outline" className="shrink-0">
                      External
                    </Badge>
                  )}
                  {entry.claimed_at ? (
                    <Badge variant="success" className="shrink-0">
                      <Check className="size-3" aria-hidden />
                      Claimed
                    </Badge>
                  ) : (
                    <Badge variant="muted" className="shrink-0">
                      Pending
                    </Badge>
                  )}
                </div>
                {!disabled && entry.email && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="size-8 text-muted-foreground hover:text-destructive"
                    aria-label={`Remove ${entry.email}`}
                    onClick={() => onRemove(entry.email as string)}
                    disabled={busy}
                  >
                    {busy ? (
                      <Loader2 className="animate-spin" aria-hidden />
                    ) : (
                      <Trash2 aria-hidden />
                    )}
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
