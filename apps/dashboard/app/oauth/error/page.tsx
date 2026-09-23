import type { Metadata } from "next";
import Link from "next/link";
import { ShieldAlert } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  firstQueryValue,
  oauthErrorPresentation,
} from "@/lib/oauth-error-page";

export const metadata: Metadata = { title: "Authorization failed" };

/**
 * OAuth / sign-in failure screen. Better Auth redirects the browser here
 * (onAPIError.errorURL) when it cannot send the error to the client — most
 * importantly invalid_redirect, which must not fall through to the sites list.
 *
 * This route lives outside the (app) shell so a signed-in user stays on the
 * explanation instead of being bounced to /dashboard.
 */
export default async function OAuthErrorPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  const error = firstQueryValue(sp.error);
  const description = firstQueryValue(sp.error_description);
  const copy = oauthErrorPresentation(error);

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col items-center justify-center px-4">
      <div
        className="w-full space-y-6 rounded-xl border border-border bg-card p-8 shadow-sm"
        role="alert"
      >
        <div className="flex flex-col items-center gap-3 text-center">
          <span className="grid size-12 place-items-center rounded-xl bg-destructive/10 text-destructive">
            <ShieldAlert className="size-6" aria-hidden />
          </span>
          <div className="space-y-1">
            <h1 className="text-xl font-semibold tracking-tight">{copy.title}</h1>
            <p className="text-sm text-muted-foreground">{copy.body}</p>
          </div>
        </div>

        <p className="text-sm text-muted-foreground">{copy.hint}</p>

        {(error || description) && (
          <div className="rounded-lg border border-border bg-muted/40 p-4 text-sm">
            {error && (
              <p className="font-mono text-xs text-foreground">error: {error}</p>
            )}
            {description && (
              <p className="mt-2 text-xs text-muted-foreground">{description}</p>
            )}
          </div>
        )}

        <Button asChild variant="outline" className="w-full">
          <Link href="/dashboard">Back to your sites</Link>
        </Button>
      </div>
    </main>
  );
}
