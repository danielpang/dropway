import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { AuthForm } from "@/components/auth/auth-form";
import { auth } from "@/lib/auth";
import { landingUrl, requireEmailVerification } from "@/lib/env";
import { oauthResumePath, safeNextPath } from "@/lib/authz-host";

export const metadata: Metadata = { title: "Sign up" };

export default async function SignUpPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  // Preserve an in-progress OAuth authorize flow (a new user who clicked "Create one"
  // from the sign-in screen during `dropway login` / MCP connect): resume authorize
  // after sign-up so it reaches consent rather than stranding the loopback client.
  // Falls back to a passed-through callbackURL, then the dashboard.
  const raw = typeof sp.callbackURL === "string" ? sp.callbackURL : undefined;
  const callbackURL =
    oauthResumePath(sp) ?? (raw ? safeNextPath(raw) : "/dashboard");

  // Already authenticated → honor the destination (resumes authorize if present).
  const session = await auth.api.getSession({ headers: await headers() });
  if (session) redirect(callbackURL);

  // Mirror the server's verification policy so the form doesn't show a dead-end
  // "verify your email" screen when verification is off (a no-email self-host).
  return (
    <AuthForm
      mode="sign-up"
      callbackURL={callbackURL}
      requireEmailVerification={requireEmailVerification()}
      landingUrl={landingUrl()}
    />
  );
}
