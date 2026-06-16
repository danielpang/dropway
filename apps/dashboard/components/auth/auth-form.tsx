"use client";

import * as React from "react";
import { Loader2, Mail, Sparkles } from "lucide-react";

import { GoogleIcon } from "@/components/icons";
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

type Mode = "sign-in" | "sign-up";

/** Pending action so each button shows its own spinner / disables siblings. */
type Pending = null | "google" | "email" | "magic";

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** Default landing after a successful credential / OAuth flow. */
const DEFAULT_CALLBACK_URL = "/dashboard";

function describeError(err: unknown): string {
  if (typeof err === "object" && err !== null) {
    const e = err as { message?: string; error?: { message?: string } };
    return e.error?.message ?? e.message ?? "Something went wrong. Try again.";
  }
  return "Something went wrong. Try again.";
}

/**
 * `callbackURL` lets a caller (e.g. the /authz viewer exchange) route the user
 * back to where they came from after sign-in. It is validated server-side to a
 * same-site path before being passed here, so it's safe to hand to Better Auth.
 */
export function AuthForm({
  mode,
  callbackURL = DEFAULT_CALLBACK_URL,
  requireEmailVerification = false,
}: {
  mode: Mode;
  callbackURL?: string;
  /**
   * Whether the server requires email verification before sign-in (mirrors the
   * Better Auth config). When false, sign-up signs the user in immediately, so we
   * go straight to the app instead of a dead-end "check your inbox" screen.
   */
  requireEmailVerification?: boolean;
}) {
  const isSignUp = mode === "sign-up";

  const [name, setName] = React.useState("");
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");

  const [pending, setPending] = React.useState<Pending>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  // Field-level validity, surfaced only after a submit attempt.
  const [touched, setTouched] = React.useState(false);
  const emailValid = EMAIL_RE.test(email);
  const passwordValid = password.length >= 8;
  const nameValid = !isSignUp || name.trim().length > 0;

  const busy = pending !== null;

  function reset() {
    setError(null);
    setNotice(null);
  }

  async function onGoogle() {
    reset();
    setPending("google");
    try {
      // Redirects to Google; on return Better Auth lands us on callbackURL.
      await authClient.signIn.social({
        provider: "google",
        callbackURL: callbackURL,
      });
    } catch (err) {
      setError(describeError(err));
      setPending(null);
    }
  }

  async function onEmailPassword(e: React.FormEvent) {
    e.preventDefault();
    reset();
    setTouched(true);
    if (!emailValid || !passwordValid || !nameValid) return;

    setPending("email");
    try {
      if (isSignUp) {
        const { error: err } = await authClient.signUp.email({
          name: name.trim(),
          email,
          password,
          callbackURL: callbackURL,
        });
        if (err) throw err;
        if (requireEmailVerification) {
          // Verification on → no session yet; tell the user to verify, then sign in.
          setNotice(
            "Check your inbox to verify your email, then sign in to continue.",
          );
        } else {
          // Verification off → Better Auth signed us in; go straight to the app
          // (the (app) layout routes a new user to onboarding to create an org).
          window.location.assign(callbackURL);
        }
      } else {
        const { error: err } = await authClient.signIn.email({
          email,
          password,
          callbackURL: callbackURL,
        });
        if (err) throw err;
        window.location.assign(callbackURL);
      }
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending((p) => (p === "email" ? null : p));
    }
  }

  async function onMagicLink() {
    reset();
    setTouched(true);
    if (!emailValid) {
      setError("Enter a valid email to receive a magic link.");
      return;
    }
    setPending("magic");
    try {
      const { error: err } = await authClient.signIn.magicLink({
        email,
        callbackURL: callbackURL,
      });
      if (err) throw err;
      setNotice(`We sent a sign-in link to ${email}. Check your inbox.`);
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending((p) => (p === "magic" ? null : p));
    }
  }

  return (
    <Card className="shadow-md">
      <CardHeader className="space-y-1.5 text-center">
        <CardTitle>{isSignUp ? "Create your account" : "Welcome back"}</CardTitle>
        <CardDescription>
          {isSignUp
            ? "Ship a folder to a live URL in one command."
            : "Sign in to your Dropway dashboard."}
        </CardDescription>
      </CardHeader>

      <CardContent className="space-y-4">
        {/* Primary method: Google. */}
        <Button
          type="button"
          variant="default"
          className="w-full"
          onClick={onGoogle}
          disabled={busy}
          aria-busy={pending === "google"}
        >
          {pending === "google" ? (
            <Loader2 className="animate-spin" aria-hidden />
          ) : (
            <GoogleIcon className="size-4" />
          )}
          Continue with Google
        </Button>

        <div className="flex items-center gap-3 py-1">
          <span className="h-px flex-1 bg-border" aria-hidden />
          <span className="text-xs uppercase tracking-wide text-muted-foreground">
            or
          </span>
          <span className="h-px flex-1 bg-border" aria-hidden />
        </div>

        {/* Secondary method: email + password (+ magic link). */}
        <form onSubmit={onEmailPassword} className="space-y-4" noValidate>
          {isSignUp && (
            <div className="space-y-2">
              <Label htmlFor="name">Name</Label>
              <Input
                id="name"
                name="name"
                autoComplete="name"
                placeholder="Ada Lovelace"
                value={name}
                onChange={(e) => setName(e.target.value)}
                aria-invalid={touched && !nameValid}
                disabled={busy}
              />
              {touched && !nameValid && (
                <p className="text-xs text-destructive">Please enter your name.</p>
              )}
            </div>
          )}

          <div className="space-y-2">
            <Label htmlFor="email">Email</Label>
            <Input
              id="email"
              name="email"
              type="email"
              inputMode="email"
              autoComplete="email"
              placeholder="you@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              aria-invalid={touched && !emailValid}
              aria-describedby="email-error"
              disabled={busy}
            />
            {touched && !emailValid && (
              <p id="email-error" className="text-xs text-destructive">
                Enter a valid email address.
              </p>
            )}
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="password">Password</Label>
              {isSignUp && (
                <span className="text-xs text-muted-foreground">
                  8+ characters
                </span>
              )}
            </div>
            <Input
              id="password"
              name="password"
              type="password"
              autoComplete={isSignUp ? "new-password" : "current-password"}
              placeholder="••••••••"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-invalid={touched && !passwordValid}
              aria-describedby="password-error"
              disabled={busy}
            />
            {touched && !passwordValid && (
              <p id="password-error" className="text-xs text-destructive">
                Use at least 8 characters.
              </p>
            )}
          </div>

          <Button
            type="submit"
            variant="secondary"
            className="w-full"
            disabled={busy}
            aria-busy={pending === "email"}
          >
            {pending === "email" ? (
              <Loader2 className="animate-spin" aria-hidden />
            ) : (
              <Mail aria-hidden />
            )}
            {isSignUp ? "Sign up with email" : "Sign in with email"}
          </Button>
        </form>

        {/* Tertiary: passwordless magic link (reuses the email field). */}
        <Button
          type="button"
          variant="ghost"
          className="w-full"
          onClick={onMagicLink}
          disabled={busy}
          aria-busy={pending === "magic"}
        >
          {pending === "magic" ? (
            <Loader2 className="animate-spin" aria-hidden />
          ) : (
            <Sparkles aria-hidden />
          )}
          Email me a magic link
        </Button>

        {/* Status region: errors and notices, announced to screen readers. */}
        {error && (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}
        {notice && (
          <p
            role="status"
            className="rounded-md border border-border bg-muted px-3 py-2 text-sm text-muted-foreground"
          >
            {notice}
          </p>
        )}
      </CardContent>

      <CardFooter className="justify-center">
        <p className="text-sm text-muted-foreground">
          {isSignUp ? "Already have an account? " : "New to Dropway? "}
          <a
            href={isSignUp ? "/sign-in" : "/sign-up"}
            className="font-medium text-foreground underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-sm"
          >
            {isSignUp ? "Sign in" : "Create one"}
          </a>
        </p>
      </CardFooter>
    </Card>
  );
}
