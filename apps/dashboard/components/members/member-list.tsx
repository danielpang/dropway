"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { HardDrive, Loader2, ShieldCheck, ShieldOff, Trash2 } from "lucide-react";

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
import { Select } from "@/components/ui/select";
import {
  removeMemberAction,
  resetMemberMfaAction,
} from "@/app/(app)/members/actions";
import type { Role } from "@/lib/api";
import { authClient } from "@/lib/auth-client";
import type { OrgMember } from "@/lib/org";
import { formatBytes } from "@/lib/utils";

function describeError(err: unknown): string {
  if (typeof err === "object" && err !== null) {
    const e = err as { message?: string; error?: { message?: string } };
    return e.error?.message ?? e.message ?? "Something went wrong.";
  }
  return "Something went wrong.";
}

function initials(name: string | null, email: string): string {
  const base = (name ?? email).trim();
  const parts = base.split(/\s+/).filter(Boolean);
  if (parts.length >= 2) {
    const a = parts[0]?.[0] ?? "";
    const b = parts[1]?.[0] ?? "";
    return (a + b).toUpperCase();
  }
  return base.slice(0, 2).toUpperCase() || "?";
}

const ROLE_VARIANT: Record<Role, "default" | "secondary" | "muted"> = {
  owner: "default",
  admin: "secondary",
  member: "muted",
};

/**
 * The org's member rows. Owners/admins can change a member's role
 * (`updateMemberRole`) or remove them (`removeMember`), but never their own row
 * (no self-demotion / self-removal footgun). The `owner` role isn't assignable
 * here (ownership transfer is a separate, owner-only flow).
 */
export function MemberList({
  members,
  organizationId,
  myUserId,
  canManage,
  myRole,
  storageByUser = {},
  mfaByUser,
}: {
  members: OrgMember[];
  organizationId: string;
  myUserId: string | null;
  canManage: boolean;
  /**
   * The viewer's own role. Owner rows are immutable to admins, but an OWNER
   * viewer may reset a co-owner's two-factor (the lockout recovery path) —
   * without this the action's owner→owner rule would be unreachable from the UI.
   */
  myRole?: Role;
  /**
   * Logical storage (bytes) per user id, the sum of each member's sites'
   * current-version sizes. Missing users render as 0. NOT deduplicated across
   * users: a file two people upload counts for both, like a Dropbox/Drive folder.
   */
  storageByUser?: Record<string, number>;
  /**
   * Two-factor enrollment per user id (admins only — the page passes undefined
   * for plain members, which hides the column entirely). Drives the 2FA badge
   * and the reset control; the first question after enforcement turns on is
   * "who isn't enrolled yet?".
   */
  mfaByUser?: Record<string, boolean>;
}) {
  const router = useRouter();
  const [busyId, setBusyId] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [removing, setRemoving] = React.useState<OrgMember | null>(null);
  const [resettingMfa, setResettingMfa] = React.useState<OrgMember | null>(null);

  async function changeRole(member: OrgMember, role: Role) {
    if (role === member.role) return;
    setError(null);
    setBusyId(member.id);
    try {
      const { error: err } = await authClient.organization.updateMemberRole({
        memberId: member.id,
        role,
        organizationId,
      });
      if (err) throw err;
      router.refresh();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusyId(null);
    }
  }

  async function confirmResetMfa() {
    if (!resettingMfa) return;
    setError(null);
    setBusyId(resettingMfa.id);
    try {
      const res = await resetMemberMfaAction({ memberId: resettingMfa.id });
      if (!res.ok) {
        setError(res.message);
        return;
      }
      setResettingMfa(null);
      router.refresh();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusyId(null);
    }
  }

  async function confirmRemove() {
    if (!removing) return;
    setError(null);
    setBusyId(removing.id);
    try {
      // Remove + revoke in one authorized server action. The action delegates the
      // removal to Better Auth (which enforces owner/admin + same-org authz) and
      // then kills the removed user's sessions and bumps the edge denylist (C2),
      // so they can't keep viewing, or re-mint tokens for, gated sites on a
      // still-valid JWT.
      const res = await removeMemberAction({ memberId: removing.id });
      if (!res.removed) {
        // The removal itself was refused — keep the dialog open with the reason.
        setError(res.message);
        return;
      }
      // The member row is gone. If the follow-up access revocation failed, the
      // removal still stands; surface a non-fatal warning to retry.
      if (!res.revoke.ok && "message" in res.revoke) {
        setError(
          `Member removed, but revoking their active access failed: ${res.revoke.message} Their tokens expire within ~15 minutes; retry "Sign out everywhere" to revoke immediately.`,
        );
      }
      setRemoving(null);
      router.refresh();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusyId(null);
    }
  }

  return (
    <>
      {error && (
        <p
          role="alert"
          className="mb-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}

      <ul className="divide-y divide-border">
        {members.map((member) => {
          const isOwner = member.role === "owner";
          const isSelf = myUserId !== null && member.userId === myUserId;
          const busy = busyId === member.id;
          // No self-mutation; owners are immutable here (admins can't touch them,
          // and ownership transfer is a separate owner-only flow). Admins can
          // edit/remove only non-owner, non-self rows.
          const canEditThis = canManage && !isOwner && !isSelf;
          // MFA reset follows the edit rules EXCEPT owner rows, which an owner
          // viewer may recover (resetMemberMfaAction enforces the same rule).
          const canResetMfaThis =
            canManage && !isSelf && (!isOwner || myRole === "owner");

          return (
            <li
              key={member.id || member.userId}
              className="flex items-center justify-between gap-3 py-3"
            >
              <div className="flex min-w-0 items-center gap-3">
                <span
                  aria-hidden
                  className="grid size-9 shrink-0 place-items-center rounded-full bg-secondary text-xs font-medium text-secondary-foreground"
                >
                  {initials(member.name, member.email)}
                </span>
                <div className="min-w-0">
                  <p className="flex items-center gap-2 text-sm font-medium text-foreground">
                    <span className="truncate">
                      {member.name ?? (member.email || "Unknown user")}
                    </span>
                    {isSelf && (
                      <Badge variant="outline" className="shrink-0">
                        You
                      </Badge>
                    )}
                  </p>
                  {member.name && member.email && (
                    <p className="truncate text-xs text-muted-foreground">
                      {member.email}
                    </p>
                  )}
                </div>
              </div>

              <div className="flex shrink-0 items-center gap-2">
                {mfaByUser &&
                  (mfaByUser[member.userId] ? (
                    <span
                      className="hidden items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400 sm:inline-flex"
                      title="Two-factor authentication is enabled"
                    >
                      <ShieldCheck className="size-3.5" aria-hidden />
                      2FA
                    </span>
                  ) : (
                    <span
                      className="hidden items-center gap-1 text-xs text-muted-foreground sm:inline-flex"
                      title="Two-factor authentication is not set up"
                    >
                      <ShieldOff className="size-3.5" aria-hidden />
                      No 2FA
                    </span>
                  ))}
                <span
                  className="hidden items-center gap-1.5 text-xs tabular-nums text-muted-foreground sm:inline-flex"
                  title="Storage used by this member's sites (logical, not deduplicated)"
                >
                  <HardDrive className="size-3.5" aria-hidden />
                  {formatBytes(storageByUser[member.userId] ?? 0)}
                </span>
                {canEditThis ? (
                  <Select
                    aria-label={`Role for ${member.email || member.name}`}
                    value={member.role}
                    onChange={(e) => changeRole(member, e.target.value as Role)}
                    disabled={busy}
                    className="h-9 w-28 text-xs"
                  >
                    <option value="member">Member</option>
                    <option value="admin">Admin</option>
                  </Select>
                ) : (
                  <Badge variant={ROLE_VARIANT[member.role]} className="capitalize">
                    {member.role}
                  </Badge>
                )}

                {canResetMfaThis && mfaByUser?.[member.userId] && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="size-9 text-muted-foreground hover:text-foreground"
                    aria-label={`Reset two-factor for ${member.email || member.name}`}
                    title="Reset two-factor (lost authenticator)"
                    onClick={() => setResettingMfa(member)}
                    disabled={busy}
                  >
                    <ShieldOff aria-hidden />
                  </Button>
                )}

                {canEditThis && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="size-9 text-muted-foreground hover:text-destructive"
                    aria-label={`Remove ${member.email || member.name}`}
                    onClick={() => setRemoving(member)}
                    disabled={busy}
                  >
                    {busy ? (
                      <Loader2 className="animate-spin" aria-hidden />
                    ) : (
                      <Trash2 aria-hidden />
                    )}
                  </Button>
                )}
              </div>
            </li>
          );
        })}
      </ul>

      <Dialog
        open={resettingMfa !== null}
        onOpenChange={(next) => {
          if (!next) setResettingMfa(null);
        }}
      >
        <DialogHeader>
          <DialogTitle>Reset two-factor authentication?</DialogTitle>
          <DialogDescription>
            Use this when {resettingMfa?.email || resettingMfa?.name} lost their
            authenticator and backup codes. Their two-factor is removed, they are
            signed out everywhere, and they set it up again at their next sign-in
            (immediately, if this organization requires two-factor). They&rsquo;ll
            be notified by email.
          </DialogDescription>
        </DialogHeader>
        <DialogBody />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setResettingMfa(null)}
            disabled={busyId !== null}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={confirmResetMfa}
            disabled={busyId !== null}
            aria-busy={busyId !== null}
          >
            {busyId !== null ? (
              <Loader2 className="animate-spin" aria-hidden />
            ) : (
              <ShieldOff aria-hidden />
            )}
            Reset two-factor
          </Button>
        </DialogFooter>
      </Dialog>

      <Dialog
        open={removing !== null}
        onOpenChange={(next) => {
          if (!next) setRemoving(null);
        }}
      >
        <DialogHeader>
          <DialogTitle>Remove member?</DialogTitle>
          <DialogDescription>
            {removing?.email || removing?.name} will lose access to this
            organization and its sites. They can be re-invited later.
          </DialogDescription>
        </DialogHeader>
        <DialogBody />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setRemoving(null)}
            disabled={busyId !== null}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={confirmRemove}
            disabled={busyId !== null}
            aria-busy={busyId !== null}
          >
            {busyId !== null ? (
              <Loader2 className="animate-spin" aria-hidden />
            ) : (
              <Trash2 aria-hidden />
            )}
            Remove member
          </Button>
        </DialogFooter>
      </Dialog>
    </>
  );
}
