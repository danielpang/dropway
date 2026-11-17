"use client";

import * as React from "react";
import { Mail, Loader2 } from "lucide-react";
import { authClient } from "@/lib/auth-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { TermsDialog } from "@/components/legal/terms-dialog";
import type { ProjectInvitation } from "@/lib/project-room/sharing";

const callbackURL = "/shared-projects/accept";

export function InvitationSignIn({ invitation, verifyEmail = false }: {
  invitation: ProjectInvitation;
  verifyEmail?: boolean;
}) {
  const [name, setName] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [agreed, setAgreed] = React.useState(false);
  const [pending, setPending] = React.useState<"password" | "magic" | null>(null);
  const [notice, setNotice] = React.useState("");
  const [error, setError] = React.useState("");
  const newAccount = !invitation.hasAccount;
  const magicFirst = newAccount || verifyEmail;

  async function authenticate(method: "password" | "magic") {
    if (pending || (newAccount && !agreed)) return;
    setPending(method);
    setError("");
    setNotice("");
    try {
      if (method === "magic") {
        const result = await authClient.signIn.magicLink({
          email: invitation.email,
          ...(name.trim() ? { name: name.trim() } : {}),
          callbackURL,
          newUserCallbackURL: callbackURL,
          errorCallbackURL: callbackURL,
        });
        if (result.error) throw result.error;
        setNotice(`Sign-in link sent to ${invitation.email}. Open it in this browser to return to your invitation.`);
      } else {
        const result = await authClient.signIn.email({ email: invitation.email, password, callbackURL });
        if (result.error) throw result.error;
        window.location.assign(callbackURL);
      }
    } catch (err) {
      setError(err && typeof err === "object" && "message" in err && typeof err.message === "string"
        ? err.message : "We couldn’t sign you in. Please try again.");
    } finally { setPending(null); }
  }

  return (
    <form className="space-y-4" onSubmit={(event) => {
      event.preventDefault();
      void authenticate(magicFirst ? "magic" : "password");
    }}>
      <div className="space-y-2">
        <h2 className="text-base font-medium">{newAccount ? "Join with your email" : verifyEmail ? "Verify your email to join" : "Sign in to join"}</h2>
        <p className="text-sm leading-6 text-muted-foreground">
          {newAccount
            ? `Joining ${invitation.projectName} will create a Dropway account for ${invitation.email}. You can sign in with an email magic link, without a password.`
            : verifyEmail
              ? "We’ll email you a sign-in link to verify this address before you accept."
              : "You already have a Dropway account with this email. Sign in to accept the invitation, or request an email sign-in link."}
        </p>
      </div>
      <div className="space-y-2">
        <Label htmlFor="invitation-email">Invited email</Label>
        <Input id="invitation-email" type="email" autoComplete="email" value={invitation.email} readOnly />
      </div>
      {newAccount && <div className="space-y-2">
        <Label htmlFor="invitation-name">Your name <span className="font-normal text-muted-foreground">(optional)</span></Label>
        <Input id="invitation-name" autoComplete="name" value={name} onChange={(event) => setName(event.target.value)} disabled={!!pending} />
      </div>}
      {!magicFirst && <div className="space-y-2">
        <Label htmlFor="invitation-password">Password</Label>
        <Input id="invitation-password" type="password" autoComplete="current-password" required value={password} onChange={(event) => setPassword(event.target.value)} disabled={!!pending} />
      </div>}
      {newAccount && <div className="flex items-start gap-2.5">
        <input id="invitation-terms" type="checkbox" checked={agreed} onChange={(event) => setAgreed(event.target.checked)} disabled={!!pending} className="mt-1 size-4 shrink-0 accent-primary" />
        <label htmlFor="invitation-terms" className="text-sm leading-6 text-muted-foreground">By creating my account and joining this project, I agree to the <TermsDialog />.</label>
      </div>}
      <Button type="submit" className="w-full" disabled={!!pending || (newAccount && !agreed)}>
        {pending === (magicFirst ? "magic" : "password") ? <Loader2 className="size-4 animate-spin" aria-hidden /> : magicFirst ? <Mail className="size-4" aria-hidden /> : null}
        {magicFirst ? "Email me a sign-in link" : "Sign in to continue"}
      </Button>
      {!magicFirst && <Button type="button" variant="outline" className="w-full" disabled={!!pending} onClick={() => void authenticate("magic")}>
        {pending === "magic" ? <Loader2 className="size-4 animate-spin" aria-hidden /> : <Mail className="size-4" aria-hidden />}
        Email me a sign-in link
      </Button>}
      {error && <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p>}
      {notice && <p role="status" className="rounded-lg border border-border bg-muted/40 p-3 text-sm leading-6">{notice}</p>}
    </form>
  );
}
