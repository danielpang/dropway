"use client";
import * as React from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  inviteProjectGuestAction,
  listProjectSharesAction,
  revokeProjectGuestAction,
} from "@/app/shared-projects/actions";
import type { GuestRole, ProjectShare } from "@/lib/project-room/sharing";
export function ProjectSharing({ roomId }: { roomId: string }) {
  const [shares, setShares] = React.useState<ProjectShare[]>([]);
  const [email, setEmail] = React.useState("");
  const [role, setRole] = React.useState<GuestRole>("reviewer");
  const [notice, setNotice] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const inFlight = React.useRef(false);
  const refresh = React.useCallback(async () => {
    const result = await listProjectSharesAction(roomId);
    if (result.ok) setShares(result.shares);
    else setNotice(result.message);
  }, [roomId]);
  React.useEffect(() => {
    let active = true;
    void listProjectSharesAction(roomId).then((result) => {
      if (!active) return;
      if (result.ok) setShares(result.shares);
      else setNotice(result.message);
    });
    return () => {
      active = false;
    };
  }, [roomId]);
  async function mutate(fn: () => Promise<void>) {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    try {
      await fn();
      await refresh();
    } catch {
      setNotice("Sharing could not be updated. Refresh before retrying.");
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  async function invite(address: string, guestRole: GuestRole) {
    const result = await inviteProjectGuestAction(roomId, address, guestRole);
    setNotice(result.message);
    if (result.ok && result.delivered) setEmail("");
  }
  return (
    <details className="space-y-3 rounded border border-border p-4">
      <summary className="cursor-pointer font-medium">
        Share project by email
      </summary>
      <p className="text-sm text-muted-foreground">
        Guests receive access only to this project. Reviewers can leave
        feedback; viewers can read. Guests cannot manage the organization, run
        AI tasks, accept changes, or publish. Invitations expire after 7 days.
      </p>
      <p className="text-sm text-muted-foreground">
        External sharing must be enabled by an organization administrator in{" "}
        <Link className="underline" href="/settings">
          Settings
        </Link>
        .
      </p>
      <form
        className="flex flex-wrap gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          void mutate(() => invite(email, role));
        }}
      >
        <Input
          type="email"
          required
          maxLength={254}
          aria-label="Guest email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
        />
        <select
          aria-label="Guest access"
          className="rounded border bg-background p-2"
          value={role}
          onChange={(event) => setRole(event.target.value as GuestRole)}
        >
          <option value="reviewer">Reviewer · can comment</option>
          <option value="viewer">Viewer · read only</option>
        </select>
        <Button disabled={busy || !email.trim()}>Send invitation</Button>
      </form>
      <p role="status" className="text-sm">
        {notice}
      </p>
      <ul className="space-y-2">
        {shares.map((share) => (
          <li
            key={share.id}
            className="flex flex-wrap items-center gap-2 text-sm"
          >
            <span>
              {share.email} · {share.role} ·{" "}
              {share.revoked_at
                ? "Revoked"
                : share.accepted_at
                  ? "Accepted"
                  : Date.parse(share.expires_at) < Date.now()
                    ? "Expired"
                    : "Pending"}
            </span>
            {!share.accepted_at && !share.revoked_at ? (
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() =>
                  void mutate(() => invite(share.email, share.role))
                }
              >
                Resend
              </Button>
            ) : null}
            {!share.revoked_at ? (
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() =>
                  void mutate(async () => {
                    const result = await revokeProjectGuestAction(
                      roomId,
                      share.id,
                    );
                    setNotice(
                      result.ok ? "Project access revoked." : result.message,
                    );
                  })
                }
              >
                Revoke
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    </details>
  );
}
