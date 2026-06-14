"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2, Trash2 } from "lucide-react";

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
import { finalizeMemberRemovalAction } from "@/app/(app)/members/actions";
import type { Role } from "@/lib/api";
import { authClient } from "@/lib/auth-client";
import type { OrgMember } from "@/lib/org";

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
 * (`updateMemberRole`) or remove them (`removeMember`) — but never their own row
 * (no self-demotion / self-removal footgun). The `owner` role isn't assignable
 * here (ownership transfer is a separate, owner-only flow).
 */
export function MemberList({
  members,
  organizationId,
  myUserId,
  canManage,
}: {
  members: OrgMember[];
  organizationId: string;
  myUserId: string | null;
  canManage: boolean;
}) {
  const router = useRouter();
  const [busyId, setBusyId] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [removing, setRemoving] = React.useState<OrgMember | null>(null);

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

  async function confirmRemove() {
    if (!removing) return;
    setError(null);
    setBusyId(removing.id);
    try {
      const { error: err } = await authClient.organization.removeMember({
        memberIdOrEmail: removing.id,
        organizationId,
      });
      if (err) throw err;
      // Removal isn't complete until the removed user's access is actually revoked
      // (C2 / ARCHITECTURE.md §10): kill their Better Auth sessions AND bump the edge
      // denylist so they can't keep viewing — or re-mint tokens for — gated sites on
      // a still-valid JWT. Do this BEFORE refreshing; a non-fatal failure here is
      // surfaced (the member row is already gone, but access revocation matters).
      const revoked = await finalizeMemberRemovalAction({ userId: removing.userId });
      if (!revoked.ok && "message" in revoked) {
        setError(
          `Member removed, but revoking their active access failed: ${revoked.message} Their tokens expire within ~15 minutes; retry "Sign out everywhere" to revoke immediately.`,
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
