"use client";
import * as React from "react";
import Link from "next/link";
import { Mail, Send, RotateCw, UserMinus } from "lucide-react";
import { Tooltip } from "@/components/ui/tooltip";
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
    <section
      aria-label="Share project by email"
      className="space-y-5 rounded-xl border border-border bg-background p-5"
    >
      <header className="flex items-center gap-2">
        <Mail className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
        <h3 className="text-sm font-semibold">Invite guests</h3>
      </header>
      <p className="text-sm text-muted-foreground">
        Share this project without adding guests to your organization.
        Invitations expire after 7 days.
      </p>
      <p className="text-xs leading-5 text-muted-foreground">
        External sharing must be enabled by an organization administrator in{" "}
        <Link className="underline" href="/settings">
          Settings
        </Link>
        .
      </p>
      <form
        className="grid grid-cols-[minmax(0,1fr)_auto] gap-2"
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
          placeholder="name@example.com"
          className="col-span-2"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
        />
        <select
          aria-label="Guest access"
          className="min-w-0 rounded-md border border-border bg-background px-3 py-2 text-xs"
          value={role}
          onChange={(event) => setRole(event.target.value as GuestRole)}
        >
          <option value="reviewer">Reviewer · can comment</option>
          <option value="viewer">Viewer · read only</option>
        </select>
        <Tooltip label="Send invitation" align="end">
          <Button
            size="icon"
            aria-label="Send invitation"
            disabled={busy || !email.trim()}
          >
            <Send className="h-4 w-4" aria-hidden="true" />
          </Button>
        </Tooltip>
      </form>
      {notice ? (
        <p
          role="status"
          className="rounded-md bg-muted/50 p-3 text-xs leading-5"
        >
          {notice}
        </p>
      ) : null}
      <p className="text-xs leading-5 text-muted-foreground">
        Guests can view or comment. They cannot run AI tasks, accept changes, or
        publish.
      </p>
      <div className="flex items-center justify-between border-t border-border pt-4">
        <h4 className="text-xs font-medium">Guest access</h4>
        <span className="text-xs tabular-nums text-muted-foreground">
          {shares.filter((share) => !share.revoked_at).length}
        </span>
      </div>
      {shares.length === 0 ? (
        <p className="py-4 text-center text-xs text-muted-foreground">
          No guests invited yet.
        </p>
      ) : null}
      <ul className="divide-y divide-border/60">
        {shares.map((share) => (
          <li
            key={share.id}
            className="flex flex-wrap items-center gap-2 py-3 text-sm"
          >
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm" title={share.email}>
                {share.email}
              </p>
              <p className="mt-1 text-xs capitalize text-muted-foreground">
                {share.role} ·{" "}
                {share.revoked_at
                  ? "Revoked"
                  : share.accepted_at
                    ? "Accepted"
                    : Date.parse(share.expires_at) < Date.now()
                      ? "Expired"
                      : "Pending"}
              </p>
            </div>
            {!share.accepted_at && !share.revoked_at ? (
              <Tooltip label="Resend invitation" align="end">
                <Button
                  aria-label="Resend invitation"
                  size="icon"
                  variant="outline"
                  disabled={busy}
                  onClick={() =>
                    void mutate(() => invite(share.email, share.role))
                  }
                >
                  <RotateCw className="h-4 w-4" aria-hidden="true" />
                </Button>
              </Tooltip>
            ) : null}
            {!share.revoked_at ? (
              <Tooltip label="Revoke guest access" align="end">
                <Button
                  aria-label="Revoke guest access"
                  size="icon"
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
                  <UserMinus className="h-4 w-4" aria-hidden="true" />
                </Button>
              </Tooltip>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
