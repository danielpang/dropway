import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";
import { KeyRound } from "lucide-react";

import { TwoFactorSettings } from "@/components/account/two-factor-settings";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { auth } from "@/lib/auth";
import { userHasPasswordCredential } from "@/lib/mfa-server";

export const metadata: Metadata = { title: "Account security" };
export const dynamic = "force-dynamic";

/**
 * Per-user security settings (unlike /settings, which is org-scoped). Currently
 * hosts two-factor authentication: enrollment with an authenticator app, backup
 * code management, and the off switch. Available to every user on every tier.
 *
 * The session read here bypasses the 5-minute signed-cookie cache: this page's
 * whole job is to render the LIVE twoFactorEnabled state (a user who just
 * enrolled must not see a stale "off"), and one uncached read on a settings
 * page is cheap.
 */
export default async function AccountSecurityPage() {
  const requestHeaders = await headers();
  // Both reads only need the headers, so they run in parallel: the uncached
  // session (live twoFactorEnabled) and whether a password credential exists
  // (Google-only / magic-link users have none → the UI skips the password
  // prompt, matching allowPasswordless on the server).
  const [session, hasPassword] = await Promise.all([
    auth.api
      .getSession({
        headers: requestHeaders,
        method: "GET",
        query: { disableCookieCache: true },
      })
      .catch(() => null),
    userHasPasswordCredential(requestHeaders),
  ]);
  if (!session) redirect("/sign-in");

  const user = session.user as {
    email: string;
    twoFactorEnabled?: boolean | null;
  };

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">
          Account security
        </h1>
        <p className="text-muted-foreground">
          Security settings for{" "}
          <span className="font-medium text-foreground">{user.email}</span>.
          These apply to your account across every organization.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <KeyRound className="size-4 text-muted-foreground" aria-hidden />
            Two-factor authentication
          </CardTitle>
          <CardDescription>
            Protect your account with a second step at sign-in: a 6-digit code
            from an authenticator app such as 1Password, Google Authenticator,
            or Authy. Backup codes cover you if you lose your device.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <TwoFactorSettings
            enabled={user.twoFactorEnabled === true}
            requiresPassword={hasPassword}
          />
        </CardContent>
      </Card>
    </div>
  );
}
