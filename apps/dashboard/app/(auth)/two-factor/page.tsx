import type { Metadata } from "next";

import { TwoFactorForm } from "@/components/auth/two-factor-form";
import { safeNextPath } from "@/lib/authz-host";

export const metadata: Metadata = { title: "Two-factor verification" };

/**
 * The 2FA sign-in challenge. Users land here mid-sign-in — from the credential
 * form (twoFactorRedirect response) or from the redirect gate covering Google /
 * magic-link — carrying the signed `two_factor` challenge cookie. Verifying a
 * TOTP or backup code completes the sign-in and continues to ?next=.
 *
 * `next` is reduced to a same-app path server-side (safeNextPath: rejects
 * protocol-relative, backslash, and control-char tricks) so this page can
 * never be used as an open redirect.
 */
export default async function TwoFactorPage({
  searchParams,
}: {
  searchParams: Promise<{ next?: string }>;
}) {
  const { next } = await searchParams;
  const validated = safeNextPath(next);
  const safeNext = validated === "/" ? "/dashboard" : validated;

  return <TwoFactorForm next={safeNext} />;
}
