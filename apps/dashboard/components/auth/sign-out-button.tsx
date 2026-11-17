"use client";

import * as React from "react";
import { LogOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import { authClient } from "@/lib/auth-client";
import { resetAnalytics } from "@/components/analytics/posthog-provider";

export function SignOutButton() {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState(false);
  return (
    <div className="flex items-center gap-2">
      {error && <span role="alert" className="text-xs text-destructive">Could not sign out. Try again.</span>}
      <Button variant="ghost" size="sm" disabled={busy} onClick={async () => {
        setBusy(true);
        setError(false);
        try {
          const result = await authClient.signOut();
          if (result.error) throw result.error;
          resetAnalytics();
          window.location.assign("/sign-in");
        } catch { setError(true); setBusy(false); }
      }}>
        <LogOut className="size-4" aria-hidden />
        {busy ? "Signing out…" : "Sign out"}
      </Button>
    </div>
  );
}
