"use client";

import * as React from "react";
import { ShieldCheck } from "lucide-react";

import { authClient } from "@/lib/auth-client";
import { Button } from "@/components/ui/button";

/**
 * OAuth consent screen — the "Authorize MCP access" step of the MCP browser flow.
 * The OAuth provider (lib/auth.ts oauthProvider) redirects an authenticated user
 * here with the pending authorization in the session; the user approves or denies,
 * and we follow the redirect the provider hands back (auth code → the AI client, or
 * an error redirect on deny).
 *
 * Note: whether MCP is allowed for the org is ALSO enforced at the MCP resource
 * server (it re-checks org_meta.mcp_enabled per request and 403s when off), so an
 * admin disabling MCP takes effect immediately even for already-issued tokens.
 */
export default function OAuthConsentPage() {
  const [pending, setPending] = React.useState<null | "accept" | "deny">(null);
  const [error, setError] = React.useState<string | null>(null);
  const [scopes, setScopes] = React.useState<string[]>([]);

  React.useEffect(() => {
    const p = new URLSearchParams(window.location.search);
    const scope = p.get("scope");
    if (scope) setScopes(scope.split(/[ +]/).filter(Boolean));
  }, []);

  async function decide(accept: boolean) {
    setPending(accept ? "accept" : "deny");
    setError(null);
    try {
      const res = await authClient.oauth2.consent({ accept });
      if (res?.error) {
        setError(res.error.message ?? "Something went wrong authorizing access.");
        setPending(null);
        return;
      }
      const data = res?.data as { redirectURI?: string; url?: string } | undefined;
      const url = data?.redirectURI ?? data?.url;
      // The provider returns a redirect back to the AI client (auth code on accept,
      // error on deny). Follow it; fall back to the dashboard if none was returned.
      window.location.href = url ?? "/dashboard";
    } catch {
      setError("Couldn't reach the authorization server. Please try again.");
      setPending(null);
    }
  }

  const busy = pending !== null;

  return (
    <div className="mx-auto flex min-h-dvh max-w-md flex-col items-center justify-center px-4">
      <div className="w-full space-y-6 rounded-xl border border-border bg-card p-8 shadow-sm">
        <div className="flex flex-col items-center gap-3 text-center">
          <span className="grid size-12 place-items-center rounded-xl bg-primary/10 text-primary">
            <ShieldCheck className="size-6" aria-hidden />
          </span>
          <div className="space-y-1">
            <h1 className="text-xl font-semibold tracking-tight">
              Authorize MCP access
            </h1>
            <p className="text-sm text-muted-foreground">
              An AI tool is requesting access to your Dropway organization through
              the Model Context Protocol.
            </p>
          </div>
        </div>

        <div className="rounded-lg border border-border bg-muted/40 p-4 text-sm">
          <p className="font-medium text-foreground">This will let the tool:</p>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-muted-foreground">
            <li>List the sites in your organization</li>
            <li>Read the files of your deployed sites</li>
          </ul>
          {scopes.length > 0 && (
            <p className="mt-3 font-mono text-xs text-muted-foreground">
              scopes: {scopes.join(", ")}
            </p>
          )}
          <p className="mt-3 text-xs text-muted-foreground">
            Access stays scoped to your organization and respects every site&rsquo;s
            sharing settings. You can revoke it anytime in Settings.
          </p>
        </div>

        {error && (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}

        <div className="flex flex-col gap-2 sm:flex-row-reverse">
          <Button
            className="flex-1"
            onClick={() => decide(true)}
            disabled={busy}
            aria-busy={pending === "accept"}
          >
            {pending === "accept" ? "Authorizing…" : "Authorize"}
          </Button>
          <Button
            variant="outline"
            className="flex-1"
            onClick={() => decide(false)}
            disabled={busy}
          >
            Deny
          </Button>
        </div>
      </div>
    </div>
  );
}
