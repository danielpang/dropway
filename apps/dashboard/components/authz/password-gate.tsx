"use client";

import * as React from "react";
import { Loader2 } from "lucide-react";

import { submitPasswordAction } from "@/app/authz/actions";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

/**
 * The PLATFORM-controlled password form for a password-protected site. It is
 * rendered on app.dropway.dev (NOT inside tenant content), so tenant JS can
 * never observe or script the password (anti-phishing).
 *
 * On submit it calls the server action, which mints an ANONYMOUS edge token via
 * the JWT-free Go endpoint and returns the content-host callback URL. We then do
 * a full cross-origin navigation to that URL — the Worker verifies the token,
 * sets the `__Host-edge` cookie on the content host, and forwards to `next`.
 */
export function PasswordGate({ host, next }: { host: string; next: string }) {
  const [password, setPassword] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!password) {
      setError("Enter the password to continue.");
      return;
    }

    setPending(true);
    const result = await submitPasswordAction({ host, next, password });

    if (result.ok) {
      // Cross-origin redirect to the content host's Worker callback. A full
      // navigation (not router.push) is required to leave app.dropway.dev.
      window.location.assign(result.redirectTo);
      return;
    }

    setError(result.message);
    setPending(false);
  }

  return (
    <Card className="shadow-md">
      <CardContent className="pt-6">
        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <div className="space-y-2">
            <Label htmlFor="authz-password">Password</Label>
            <Input
              id="authz-password"
              name="password"
              type="password"
              autoComplete="current-password"
              placeholder="••••••••"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-invalid={error !== null}
              autoFocus
              disabled={pending}
            />
          </div>

          {error && (
            <p
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}

          <Button
            type="submit"
            className="w-full"
            disabled={pending}
            aria-busy={pending}
          >
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Unlock site
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
