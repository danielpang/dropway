import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { AuthForm } from "@/components/auth/auth-form";
import { auth } from "@/lib/auth";

export const metadata: Metadata = { title: "Sign up" };

export default async function SignUpPage() {
  // Already authenticated → skip the form.
  const session = await auth.api.getSession({ headers: await headers() });
  if (session) redirect("/dashboard");

  return <AuthForm mode="sign-up" />;
}
