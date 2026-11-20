"use client";
import * as React from "react";
import { usePathname } from "next/navigation";
import { Button } from "@/components/ui/button";
import { authClient } from "@/lib/auth-client";
import { resetAnalytics } from "@/components/analytics/posthog-provider";
export function SharedProjectSignOut() {
  const pathname = usePathname();
  const [busy, setBusy] = React.useState(false);
  return (
    <Button
      variant="outline"
      size="sm"
      disabled={busy}
      onClick={() => {
        setBusy(true);
        void authClient
          .signOut()
          .finally(() => {
            resetAnalytics();
            window.location.assign(
              pathname === "/shared-projects/accept" ? pathname : `/sign-in?callbackURL=${encodeURIComponent(pathname?.startsWith("/shared-projects") ? pathname : "/shared-projects")}`,
            );
          });
      }}
    >
      Sign out
    </Button>
  );
}
