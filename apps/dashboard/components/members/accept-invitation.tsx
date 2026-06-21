"use client";

import * as React from "react";
import { Check, Loader2, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { recordMemberJoinAction } from "@/app/(app)/members/actions";
import { authClient } from "@/lib/auth-client";

function describeError(err: unknown): string {
  if (typeof err === "object" && err !== null) {
    const e = err as { message?: string; error?: { message?: string } };
    return (
      e.error?.message ??
      e.message ??
      "This invitation can't be accepted (it may have expired or been for a different email)."
    );
  }
  return "Could not accept the invitation.";
}

/**
 * Accept or decline a pending organization invitation. On accept, Better Auth
 * adds the membership and sets the org active; we then land on the dashboard.
 * Declining rejects the invite and returns the user home.
 */
export function AcceptInvitation({ invitationId }: { invitationId: string }) {
  const [pending, setPending] = React.useState<null | "accept" | "reject">(null);
  const [error, setError] = React.useState<string | null>(null);

  async function accept() {
    setError(null);
    setPending("accept");
    try {
      const { data, error: err } =
        await authClient.organization.acceptInvitation({ invitationId });
      if (err) throw err;
      // Make the joined org active so the dashboard scopes to it.
      const orgId = (data as { invitation?: { organizationId?: string } } | null)
        ?.invitation?.organizationId;
      if (orgId) {
        await authClient.organization
          .setActive({ organizationId: orgId })
          .catch(() => undefined);
      }
      // Record the join in the org audit trail AFTER setActive, so the Go API scopes
      // the row to the org just joined (RLS + actor). Best-effort; the membership is
      // already authoritative, so a failure never blocks landing on the dashboard.
      await recordMemberJoinAction().catch(() => undefined);
      window.location.assign("/dashboard");
    } catch (err) {
      setError(describeError(err));
      setPending(null);
    }
  }

  async function reject() {
    setError(null);
    setPending("reject");
    try {
      const { error: err } = await authClient.organization.rejectInvitation({
        invitationId,
      });
      if (err) throw err;
      window.location.assign("/");
    } catch (err) {
      setError(describeError(err));
      setPending(null);
    }
  }

  const busy = pending !== null;

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-2 sm:flex-row">
        <Button
          type="button"
          className="flex-1"
          onClick={accept}
          disabled={busy}
          aria-busy={pending === "accept"}
        >
          {pending === "accept" ? (
            <Loader2 className="animate-spin" aria-hidden />
          ) : (
            <Check aria-hidden />
          )}
          Accept invitation
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={reject}
          disabled={busy}
          aria-busy={pending === "reject"}
        >
          {pending === "reject" ? (
            <Loader2 className="animate-spin" aria-hidden />
          ) : (
            <X aria-hidden />
          )}
          Decline
        </Button>
      </div>

      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}
    </div>
  );
}
