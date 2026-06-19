import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { AuthForm } from "@/components/auth/auth-form";
import { auth } from "@/lib/auth";
import { landingUrl } from "@/lib/env";
import { safeNextPath } from "@/lib/authz-host";

export const metadata: Metadata = { title: "Sign in" };

export default async function SignInPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  // A caller (e.g. the /authz viewer exchange) can ask us to return the user to
  // where they came from. Validate it to a same-site path so it can never be an
  // open redirect; default to the dashboard.
  const raw = typeof sp.callbackURL === "string" ? sp.callbackURL : undefined;
  const callbackURL = raw ? safeNextPath(raw) : "/dashboard";

  // Already authenticated → honor the requested destination.
  const session = await auth.api.getSession({ headers: await headers() });
  if (session) redirect(callbackURL);

  return (
    <AuthForm mode="sign-in" callbackURL={callbackURL} landingUrl={landingUrl()} />
  );
}
