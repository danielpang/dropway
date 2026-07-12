import type { Metadata } from "next";

import { TwoFactorForm } from "@/components/auth/two-factor-form";

export const metadata: Metadata = { title: "Two-factor verification" };

/**
 * The 2FA sign-in challenge. Users land here mid-sign-in — from the credential
 * form (twoFactorRedirect response) or from the redirect gate covering Google /
 * magic-link — carrying the signed `two_factor` challenge cookie. Verifying a
 * TOTP or backup code completes the sign-in and continues to ?next=.
 *
 * `next` is reduced to a same-app path server-side so this page can never be
 * used as an open redirect.
 */
export default async function TwoFactorPage({
  searchParams,
}: {
  searchParams: Promise<{ next?: string }>;
}) {
  const { next } = await searchParams;
  const safeNext =
    next && next.startsWith("/") && !next.startsWith("//") ? next : "/dashboard";

  return <TwoFactorForm next={safeNext} />;
}
