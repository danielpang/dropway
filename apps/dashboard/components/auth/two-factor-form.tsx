"use client";

import * as React from "react";
import { KeyRound, Loader2, ShieldCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { authClient } from "@/lib/auth-client";

const TOTP_RE = /^\d{6}$/;

function describeError(err: unknown): string {
  if (typeof err === "object" && err !== null) {
    const e = err as { message?: string; error?: { message?: string } };
    return e.error?.message ?? e.message ?? "Verification failed. Try again.";
  }
  return "Verification failed. Try again.";
}

/**
 * The mid-sign-in 2FA challenge card. Works off the signed `two_factor` cookie
 * the server set when it withheld the session; on a verified code Better Auth
 * mints the real session and we continue to `next`. The challenge cookie lives
 * 10 minutes — an expired one surfaces as an error with a path back to sign-in.
 */
export function TwoFactorForm({ next }: { next: string }) {
  const [mode, setMode] = React.useState<"totp" | "backup">("totp");
  const [code, setCode] = React.useState("");
  const [trustDevice, setTrustDevice] = React.useState(false);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const isTotp = mode === "totp";
  const codeValid = isTotp ? TOTP_RE.test(code.trim()) : code.trim().length > 0;

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!codeValid) {
      setError(
        isTotp
          ? "Enter the 6-digit code from your authenticator app."
          : "Enter one of your backup codes.",
      );
      return;
    }
    setPending(true);
    try {
      const trimmed = code.trim();
      const { error: err } = isTotp
        ? await authClient.twoFactor.verifyTotp({
            code: trimmed,
            trustDevice,
          })
        : await authClient.twoFactor.verifyBackupCode({
            code: trimmed,
            trustDevice,
          });
      if (err) throw err;
      window.location.assign(next);
    } catch (err) {
      setError(describeError(err));
      setPending(false);
    }
  }

  return (
    <Card className="shadow-md">
      <CardHeader className="space-y-1.5 text-center">
        <div className="mx-auto grid size-10 place-items-center rounded-lg bg-primary/10 text-primary">
          {isTotp ? (
            <ShieldCheck className="size-5" aria-hidden />
          ) : (
            <KeyRound className="size-5" aria-hidden />
          )}
        </div>
        <CardTitle>Two-factor verification</CardTitle>
        <CardDescription>
          {isTotp
            ? "Enter the 6-digit code from your authenticator app to finish signing in."
            : "Enter one of your one-time backup codes. Each code works once."}
        </CardDescription>
      </CardHeader>

      <CardContent>
        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <div className="space-y-2">
            <Label htmlFor="tf-code">
              {isTotp ? "Authentication code" : "Backup code"}
            </Label>
            <Input
              id="tf-code"
              name="code"
              inputMode={isTotp ? "numeric" : "text"}
              autoComplete="one-time-code"
              autoFocus
              placeholder={isTotp ? "123456" : "xxxxx-xxxxx"}
              value={code}
              onChange={(e) => setCode(e.target.value)}
              disabled={pending}
              className="text-center font-mono tracking-widest"
            />
          </div>

          <div className="flex items-start gap-2.5">
            <input
              id="trust-device"
              type="checkbox"
              checked={trustDevice}
              onChange={(e) => setTrustDevice(e.target.checked)}
              disabled={pending}
              className="mt-0.5 size-4 shrink-0 rounded border-border accent-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
            />
            <label
              htmlFor="trust-device"
              className="text-sm leading-snug text-muted-foreground"
            >
              Trust this device for 30 days
            </label>
          </div>

          <Button
            type="submit"
            className="w-full"
            disabled={pending}
            aria-busy={pending}
          >
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Verify and continue
          </Button>
        </form>

        {error && (
          <p
            role="alert"
            className="mt-4 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}
      </CardContent>

      <CardFooter className="flex-col gap-3">
        <button
          type="button"
          onClick={() => {
            setMode(isTotp ? "backup" : "totp");
            setCode("");
            setError(null);
          }}
          className="text-sm font-medium text-foreground underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-sm"
        >
          {isTotp ? "Use a backup code instead" : "Use your authenticator app"}
        </button>
        <p className="text-sm text-muted-foreground">
          Lost access?{" "}
          <a
            href="/sign-in"
            className="font-medium text-foreground underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-sm"
          >
            Back to sign in
          </a>
        </p>
      </CardFooter>
    </Card>
  );
}
