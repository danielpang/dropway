"use client";
import * as React from "react";
import { AuthForm } from "@/components/auth/auth-form";
import { Button } from "@/components/ui/button";
import { authClient } from "@/lib/auth-client";
import {
  acceptSharedProjectAction,
  rememberProjectInvitationAction,
} from "@/app/shared-projects/actions";
import { invitationToken } from "@/lib/project-room/sharing";
export function AcceptProjectInvitation({
  email,
  verified,
  pendingInvitation,
}: {
  email: string | null;
  verified: boolean;
  pendingInvitation: boolean;
}) {
  const [ready, setReady] = React.useState(pendingInvitation);
  const [notice, setNotice] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const pending = React.useRef(false);
  React.useEffect(() => {
    const fresh = invitationToken(window.location.hash);
    window.history.replaceState(null, "", "/shared-projects/accept");
    if (fresh) {
      setReady(false);
      void rememberProjectInvitationAction(fresh)
        .then((result) => {
          setReady(result.ok);
          if (!result.ok) setNotice("Reopen your invitation email.");
        })
        .catch(() =>
          setNotice(
            "The invitation could not be retained. Reopen the original email.",
          ),
        );
    }
  }, []);
  async function switchAccount() {
    await authClient.signOut();
    window.location.assign("/sign-in?callbackURL=%2Fshared-projects%2Faccept");
  }
  return (
    <section className="mx-auto max-w-xl space-y-4">
      <h1 className="text-xl font-medium">Accept a project invitation</h1>
      <p>
        Sign in with the exact email address that received the invitation. This
        grants project access without joining the organization.
      </p>
      <p role="status">{notice}</p>
      {!ready ? (
        <p>
          Open the original invitation email in this browser. The pending
          invitation is retained securely for 30 minutes while you sign in.
        </p>
      ) : null}
      {!email ? (
        <>
          <p>
            Use an email sign-in link below or your existing Google account.
            After sign-in, return here to accept.
          </p>
          <AuthForm mode="sign-in" callbackURL="/shared-projects/accept" />
        </>
      ) : (
        <>
          <p>
            Signed in as {email}
            {verified ? "" : " (email verification required)"}.
          </p>
          {!verified ? (
            <p>
              Use an email sign-in link for this address to verify it before
              accepting.
            </p>
          ) : null}
          <Button variant="outline" onClick={() => void switchAccount()}>
            Use another account or email sign-in link
          </Button>
          <Button
            disabled={!ready || !verified || busy}
            onClick={() => {
              if (!ready || pending.current) return;
              pending.current = true;
              setBusy(true);
              void acceptSharedProjectAction()
                .then((result) => {
                  if (!result.ok) {
                    setNotice(result.message);
                    return;
                  }
                  window.location.assign(
                    `/shared-projects/${encodeURIComponent(result.project.id)}`,
                  );
                })
                .catch(() =>
                  setNotice(
                    "Acceptance could not be confirmed. Reopen the invitation and retry.",
                  ),
                )
                .finally(() => {
                  pending.current = false;
                  setBusy(false);
                });
            }}
          >
            Accept project invitation
          </Button>
        </>
      )}
    </section>
  );
}
