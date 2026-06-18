"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2, Mail, X } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { authClient } from "@/lib/auth-client";
import type { OrgInvitation } from "@/lib/org";

function describeError(err: unknown): string {
  if (typeof err === "object" && err !== null) {
    const e = err as { message?: string; error?: { message?: string } };
    return e.error?.message ?? e.message ?? "Could not cancel the invitation.";
  }
  return "Could not cancel the invitation.";
}

/**
 * Pending invitations with a cancel control (`cancelInvitation`), owner/admin
 * only (the parent page only renders this for managers). Cancelling revokes the
 * outstanding invite so the email can no longer be used to join.
 */
export function PendingInvitations({
  invitations,
}: {
  invitations: OrgInvitation[];
}) {
  const router = useRouter();
  const [busyId, setBusyId] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  async function cancel(invitationId: string) {
    setError(null);
    setBusyId(invitationId);
    try {
      const { error: err } = await authClient.organization.cancelInvitation({
        invitationId,
      });
      if (err) throw err;
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
        {invitations.map((invite) => {
          const busy = busyId === invite.id;
          return (
            <li
              key={invite.id}
              className="flex items-center justify-between gap-3 py-3"
            >
              <div className="flex min-w-0 items-center gap-3">
                <span
                  aria-hidden
                  className="grid size-9 shrink-0 place-items-center rounded-full bg-muted text-muted-foreground"
                >
                  <Mail className="size-4" aria-hidden />
                </span>
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground">
                    {invite.email}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    Invited as {invite.role}
                    {invite.expiresAt
                      ? ` · expires ${new Date(invite.expiresAt).toLocaleDateString()}`
                      : ""}
                  </p>
                </div>
              </div>

              <div className="flex shrink-0 items-center gap-2">
                <Badge variant="muted">Pending</Badge>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="size-9 text-muted-foreground hover:text-destructive"
                  aria-label={`Cancel invitation to ${invite.email}`}
                  onClick={() => cancel(invite.id)}
                  disabled={busy}
                >
                  {busy ? (
                    <Loader2 className="animate-spin" aria-hidden />
                  ) : (
                    <X aria-hidden />
                  )}
                </Button>
              </div>
            </li>
          );
        })}
      </ul>
    </>
  );
}
