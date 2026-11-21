"use client";

import * as React from "react";
import { Mail, UserRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { authClient } from "@/lib/auth-client";
import {
  acceptSharedProjectAction,
  rememberProjectInvitationAction,
} from "@/app/shared-projects/actions";
import { invitationToken, type ProjectInvitationLookup } from "@/lib/project-room/sharing";
import { InvitationSignIn } from "./invitation-sign-in";
import { resetAnalytics } from "@/components/analytics/posthog-provider";

export function AcceptProjectInvitation({ email, verified, pendingInvitation, lookup: initialLookup = null }: {
  email: string | null;
  verified: boolean;
  pendingInvitation: boolean;
  lookup?: ProjectInvitationLookup | null;
}) {
  const [lookup, setLookup] = React.useState(initialLookup);
  const invitation = lookup?.status === "available" ? lookup.invitation : null;
  const [ready, setReady] = React.useState(pendingInvitation);
  const [checking, setChecking] = React.useState(false);
  const [notice, setNotice] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const pending = React.useRef(false);
  React.useEffect(() => {
    const fresh = invitationToken(window.location.hash);
    const authError = new URLSearchParams(window.location.search).get("error");
    window.history.replaceState(null, "", "/shared-projects/accept");
    if (authError) setNotice("That sign-in link has expired or was already used. Request a new link below.");
    if (!fresh) return;
    setReady(false);
    setChecking(true);
    void rememberProjectInvitationAction(fresh)
      .then((result) => {
        setReady(result.ok);
        setLookup(result.ok ? result.lookup : { status: "unavailable" });
      })
      .catch(() => { setLookup({ status: "temporary-error" }); })
      .finally(() => { setChecking(false); });
  }, []);

  async function switchAccount() {
    if (busy) return;
    setBusy(true);
    try {
      const result = await authClient.signOut();
      if (result.error) throw result.error;
      resetAnalytics();
      // Stay on the invitation, including its HTTP-only cookie and intended email.
      window.location.assign("/shared-projects/accept");
    } catch { setNotice("Could not sign out. Please try again."); setBusy(false); }
  }
  async function accept() {
    if (!ready || !invitation || pending.current) return;
    pending.current = true;
    setBusy(true);
    setNotice("");
    try {
      const result = await acceptSharedProjectAction();
      if (!result.ok) { setNotice(result.message); return; }
      window.location.assign(`/shared-projects/${encodeURIComponent(result.project.id)}`);
    } catch { setNotice("Acceptance could not be confirmed. Reopen the invitation and retry."); }
    finally { pending.current = false; setBusy(false); }
  }
  const matches = !!invitation && email?.trim().toLowerCase() === invitation.email.trim().toLowerCase();

  return (
    <section className="mx-auto w-full max-w-xl space-y-6 py-6 sm:py-10">
      <div className="flex size-11 items-center justify-center rounded-xl border border-border bg-muted/40"><Mail className="size-5 text-muted-foreground" aria-hidden /></div>
      <header className="space-y-3">
        <h1 className="text-2xl font-semibold tracking-tight">Accept a project invitation</h1>
        <p className="text-sm leading-6 text-muted-foreground">
          {invitation ? <>You’ve been invited to <span className="font-medium text-foreground">{invitation.projectName}</span>. Join to view the project and collaborate without creating an organization.</> : "Sign in with the email address that received the invitation to join the project."}
        </p>
      </header>
      {notice && <p role="status" className="rounded-lg border border-border bg-muted/40 p-3 text-sm leading-6">{notice}</p>}
      {checking ? <p role="status" className="text-sm text-muted-foreground">Checking invitation…</p> : !invitation ? (
        lookup?.status === "temporary-error" ? (
          <div className="space-y-4">
            <p role="status" className="text-sm leading-6 text-muted-foreground">The invitation could not be checked right now. Try again in a moment.</p>
            <form action="/shared-projects/accept" method="get">
              <Button type="submit">Retry</Button>
            </form>
          </div>
        ) : <p className="text-sm leading-6 text-muted-foreground">{lookup?.status === "unavailable" || ready ? "This invitation is unavailable or has expired. Ask the project owner for a fresh invitation, then open the link from your email." : "Open the original invitation email in this browser to continue."}</p>
      ) : !email ? <InvitationSignIn invitation={invitation} /> : (
        <>
          <div className="flex items-center gap-3 rounded-xl border border-border bg-muted/20 p-4">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground"><UserRound className="size-4" aria-hidden /></span>
            <div className="min-w-0 space-y-1"><p className="text-xs text-muted-foreground">Signed in as</p><p className="break-words text-sm font-medium">{email}</p></div>
          </div>
          {!matches ? (
            <div className="space-y-4">
              <p className="text-sm leading-6 text-muted-foreground">This invitation was sent to <span className="break-words font-medium text-foreground">{invitation.email}</span>. Switch to that account to continue.</p>
              <Button className="w-full sm:w-auto" disabled={busy} onClick={() => void switchAccount()}>Switch to invited account</Button>
            </div>
          ) : !verified ? <InvitationSignIn invitation={{ ...invitation, hasAccount: true }} verifyEmail /> : (
            <div className="flex flex-col gap-3 border-t border-border pt-6 sm:flex-row sm:flex-wrap">
              <Button className="w-full sm:w-auto" disabled={!ready || busy} onClick={() => void accept()}>{busy ? "Accepting invitation…" : "Accept project invitation"}</Button>
              <Button className="w-full sm:w-auto" variant="outline" disabled={busy} onClick={() => void switchAccount()}>Use another account</Button>
            </div>
          )}
        </>
      )}
    </section>
  );
}
