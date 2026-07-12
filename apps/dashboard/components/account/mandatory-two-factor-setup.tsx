"use client";

import * as React from "react";

import { TwoFactorEnroll } from "@/components/account/two-factor-settings";
import { authClient } from "@/lib/auth-client";

/**
 * Client shell for the mandatory setup page: the standard enroll flow
 * (auto-started — there's nothing else to do on this page), completion goes
 * straight into the app, and the only other exit is signing out.
 */
export function MandatoryTwoFactorSetup({
  requiresPassword,
}: {
  requiresPassword: boolean;
}) {
  const [signingOut, setSigningOut] = React.useState(false);

  async function onSignOut() {
    setSigningOut(true);
    try {
      await authClient.signOut();
    } finally {
      window.location.assign("/sign-in");
    }
  }

  return (
    <div className="space-y-6">
      <TwoFactorEnroll
        requiresPassword={requiresPassword}
        autoStart
        onEnrolled={() => window.location.assign("/dashboard")}
      />
      <p className="border-t border-border pt-4 text-sm text-muted-foreground">
        Not ready?{" "}
        <button
          type="button"
          onClick={onSignOut}
          disabled={signingOut}
          className="font-medium text-foreground underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-sm"
        >
          Sign out
        </button>{" "}
        and come back when you are.
      </p>
    </div>
  );
}
