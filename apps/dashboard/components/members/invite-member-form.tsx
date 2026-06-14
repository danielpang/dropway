"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2, Send } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { preflightMembersAction } from "@/app/(app)/members/actions";
import type { Role } from "@/lib/api";
import { authClient } from "@/lib/auth-client";

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function describeError(err: unknown): string {
  if (typeof err === "object" && err !== null) {
    const e = err as { message?: string; error?: { message?: string } };
    return e.error?.message ?? e.message ?? "Could not send the invitation.";
  }
  return "Could not send the invitation.";
}

/**
 * Invite a teammate to the active org via Better Auth's Organization plugin
 * (`organization.inviteMember`). Only `admin`/`member` are invitable roles here
 * — `owner` is the creator and isn't assignable through an invite. The Go API
 * still re-checks the caller is owner/admin, so this form is a convenience gate.
 */
export function InviteMemberForm({
  organizationId,
}: {
  organizationId: string;
}) {
  const router = useRouter();

  const [email, setEmail] = React.useState("");
  const [role, setRole] = React.useState<Exclude<Role, "owner">>("member");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);
  const [touched, setTouched] = React.useState(false);

  const emailValid = EMAIL_RE.test(email);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    setTouched(true);
    if (!emailValid) return;

    setPending(true);
    try {
      // H8: ask the Go API whether the org may add another member BEFORE inviting,
      // so a Free org at its cap gets a clear upgrade prompt rather than a generic
      // failure after Better Auth creates the invitation.
      const preflight = await preflightMembersAction();
      if (!preflight.ok) {
        setError(
          preflight.upgradeUrl
            ? `${preflight.message} ${preflight.upgradeUrl}`
            : preflight.message,
        );
        return;
      }
      const { error: err } = await authClient.organization.inviteMember({
        email,
        role,
        organizationId,
      });
      if (err) throw err;
      setNotice(`Invitation sent to ${email}.`);
      setEmail("");
      setTouched(false);
      router.refresh();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <form onSubmit={onSubmit} className="space-y-4" noValidate>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <div className="flex-1 space-y-2">
          <Label htmlFor="invite-email">Email</Label>
          <Input
            id="invite-email"
            name="email"
            type="email"
            inputMode="email"
            autoComplete="off"
            placeholder="teammate@company.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-invalid={touched && !emailValid}
            disabled={pending}
          />
        </div>
        <div className="space-y-2 sm:w-36">
          <Label htmlFor="invite-role">Role</Label>
          <Select
            id="invite-role"
            value={role}
            onChange={(e) =>
              setRole(e.target.value as Exclude<Role, "owner">)
            }
            disabled={pending}
          >
            <option value="member">Member</option>
            <option value="admin">Admin</option>
          </Select>
        </div>
        <Button type="submit" disabled={pending} aria-busy={pending}>
          {pending ? (
            <Loader2 className="animate-spin" aria-hidden />
          ) : (
            <Send aria-hidden />
          )}
          Invite
        </Button>
      </div>

      {touched && !emailValid && (
        <p className="text-xs text-destructive">Enter a valid email address.</p>
      )}
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
    </form>
  );
}
