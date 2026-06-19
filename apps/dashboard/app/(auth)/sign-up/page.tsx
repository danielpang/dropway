import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { AuthForm } from "@/components/auth/auth-form";
import { auth } from "@/lib/auth";
import { landingUrl, requireEmailVerification } from "@/lib/env";

export const metadata: Metadata = { title: "Sign up" };

export default async function SignUpPage() {
  // Already authenticated → skip the form.
  const session = await auth.api.getSession({ headers: await headers() });
  if (session) redirect("/dashboard");

  // Mirror the server's verification policy so the form doesn't show a dead-end
  // "verify your email" screen when verification is off (a no-email self-host).
  return (
    <AuthForm
      mode="sign-up"
      requireEmailVerification={requireEmailVerification()}
      landingUrl={landingUrl()}
    />
  );
}
