import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { AuthForm } from "@/components/auth/auth-form";
import { auth } from "@/lib/auth";
import { landingUrl } from "@/lib/env";
import { oauthResumePath, safeNextPath } from "@/lib/authz-host";
import { isReauthRequest } from "@/lib/reauth";

export const metadata: Metadata = { title: "Sign in" };

export default async function SignInPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  // When the Better Auth OAuth provider sends an unauthenticated user here to sign in
  // (CLI `dropway login` / MCP connect), resume that authorize flow after sign-in so
  // it reaches the consent screen, otherwise the loopback client waits forever.
  // Otherwise, a caller (e.g. the /authz viewer exchange) can ask us to return the
  // user to where they came from. Validate it to a same-site path so it can never be
  // an open redirect; default to the dashboard.
  const raw = typeof sp.callbackURL === "string" ? sp.callbackURL : undefined;
  const callbackURL =
    oauthResumePath(sp) ?? (raw ? safeNextPath(raw) : "/dashboard");

  // Already authenticated → honor the requested destination, unless this visit
  // is a forced re-login. reauth=1 is set when the API rejected the credential
  // (401) while a session cookie may still be present; bouncing that cookie
  // back to callbackURL would loop. Show the form so they can sign in again.
  const session = await auth.api.getSession({ headers: await headers() });
  const reauth = isReauthRequest(sp.reauth);
  if (session && !reauth) redirect(callbackURL);

  return (
    <AuthForm
      mode="sign-in"
      callbackURL={callbackURL}
      reauth={reauth}
      landingUrl={landingUrl()}
    />
  );
}
