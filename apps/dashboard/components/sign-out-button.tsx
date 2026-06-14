"use client";

import * as React from "react";
import { LogOut } from "lucide-react";

import { Button } from "@/components/ui/button";
import { authClient } from "@/lib/auth-client";

/** Signs out the current session and returns to the sign-in surface. */
export function SignOutButton() {
  const [pending, setPending] = React.useState(false);

  async function onClick() {
    setPending(true);
    try {
      await authClient.signOut();
    } finally {
      window.location.assign("/sign-in");
    }
  }

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={onClick}
      disabled={pending}
    >
      <LogOut aria-hidden />
      Sign out
    </Button>
  );
}
