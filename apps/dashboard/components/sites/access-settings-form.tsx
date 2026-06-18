"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Globe, Loader2, Lock, Mail, Users, EyeOff } from "lucide-react";

import {
  setAccessAction,
  type AccessSelection,
} from "@/app/(app)/sites/[id]/settings/actions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { AccessMode } from "@/lib/api";
import { cn } from "@/lib/utils";

/** The five selectable access shapes. */
const OPTIONS: {
  value: AccessSelection;
  label: string;
  description: string;
  icon: typeof Globe;
}[] = [
  {
    value: "public",
    label: "Public",
    description: "Anyone with the link. Listed and cacheable.",
    icon: Globe,
  },
  {
    value: "unlisted",
    label: "Unlisted",
    description: "Anyone with the link, but the URL is unguessable.",
    icon: EyeOff,
  },
  {
    value: "password",
    label: "Password",
    description: "Viewers must enter a shared password.",
    icon: Lock,
  },
  {
    value: "allowlist",
    label: "Allowlist",
    description: "Only specific verified emails can view it.",
    icon: Mail,
  },
  {
    value: "org_only",
    label: "Org only",
    description: "Only members of your organization can view it.",
    icon: Users,
  },
];

/** Map the site's stored access_mode to the form's initial selection. */
function initialSelection(mode: AccessMode): AccessSelection {
  // `public` may be listed or unlisted; the Site resource doesn't distinguish,
  // so default to "public" (the admin re-picks "unlisted" if that's intended).
  return mode;
}

export function AccessSettingsForm({
  siteId,
  currentMode,
  disabled,
}: {
  siteId: string;
  currentMode: AccessMode;
  disabled: boolean;
}) {
  const router = useRouter();

  const [selection, setSelection] = React.useState<AccessSelection>(
    initialSelection(currentMode),
  );
  const [password, setPassword] = React.useState("");
  const [expiresAt, setExpiresAt] = React.useState("");

  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  const needsPassword = selection === "password";
  const supportsExpiry = selection === "password" || selection === "allowlist";

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);

    if (needsPassword && password.length < 1) {
      setError("Set a password for password-protected access.");
      return;
    }

    setPending(true);
    const result = await setAccessAction({
      siteId,
      selection,
      password: needsPassword ? password : undefined,
      expiresAt: supportsExpiry && expiresAt ? expiresAt : null,
    });

    if (result.ok) {
      setNotice("Access updated.");
      setPassword("");
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  return (
    <form onSubmit={onSubmit} className="space-y-5">
      <fieldset disabled={disabled} className="space-y-3">
        <legend className="sr-only">Access mode</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          {OPTIONS.map((opt) => {
            const Icon = opt.icon;
            const checked = selection === opt.value;
            return (
              <label
                key={opt.value}
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors",
                  "focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-2 focus-within:ring-offset-background",
                  checked
                    ? "border-foreground/30 bg-secondary/60"
                    : "border-border hover:border-foreground/20",
                  disabled && "cursor-not-allowed opacity-60",
                )}
              >
                <input
                  type="radio"
                  name="access-mode"
                  value={opt.value}
                  checked={checked}
                  onChange={() => setSelection(opt.value)}
                  className="sr-only"
                />
                <span
                  aria-hidden
                  className={cn(
                    "mt-0.5 grid size-8 shrink-0 place-items-center rounded-md",
                    checked
                      ? "bg-primary text-primary-foreground"
                      : "bg-secondary text-secondary-foreground",
                  )}
                >
                  <Icon className="size-4" />
                </span>
                <span className="min-w-0">
                  <span className="block text-sm font-medium text-foreground">
                    {opt.label}
                  </span>
                  <span className="block text-xs text-muted-foreground">
                    {opt.description}
                  </span>
                </span>
              </label>
            );
          })}
        </div>

        {needsPassword && (
          <div className="space-y-2 pt-1">
            <Label htmlFor="access-password">Password</Label>
            <Input
              id="access-password"
              name="access-password"
              type="password"
              autoComplete="new-password"
              placeholder="Set a password viewers will enter"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Stored hashed; it&rsquo;s never shown again. Re-enter to change it.
            </p>
          </div>
        )}

        {supportsExpiry && (
          <div className="space-y-2 pt-1">
            <Label htmlFor="access-expiry">Link expiry (optional)</Label>
            <Input
              id="access-expiry"
              name="access-expiry"
              type="datetime-local"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
              className="w-full sm:w-72"
            />
            <p className="text-xs text-muted-foreground">
              After this time, the share link stops working. Leave blank for no
              expiry.
            </p>
          </div>
        )}
      </fieldset>

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

      <Button type="submit" disabled={disabled || pending} aria-busy={pending}>
        {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
        Save access
      </Button>
    </form>
  );
}
