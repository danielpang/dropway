"use client";

import * as React from "react";
import { Building2, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { authClient, oauthConsentClient } from "@/lib/auth-client";

/**
 * True when this onboarding render is part of an in-progress OAuth authorize flow —
 * i.e. the provider's postLogin hook redirected an org-less user here (carrying the
 * signed authorize query). Detected by the signed-query markers the provider appends
 * (`client_id` + `sig`). When set, we resume the flow after org creation instead of
 * landing on the dashboard.
 */
function inOAuthFlow(): boolean {
  if (typeof window === "undefined") return false;
  const p = new URLSearchParams(window.location.search);
  return p.has("client_id") && p.has("sig");
}

/** Turn a display name into a URL-safe org slug (lowercase, hyphenated). */
function slugify(value: string): string {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
}

function describeError(err: unknown): string {
  if (typeof err === "object" && err !== null) {
    const e = err as { message?: string; error?: { message?: string } };
    return e.error?.message ?? e.message ?? "Could not create the organization.";
  }
  return "Could not create the organization.";
}

/**
 * Creates the user's first organization via the Better Auth organization plugin
 * (`createOrganization`), sets it active, then lands on the dashboard. The org
 * is the tenant the Go API scopes every site/version to.
 */
export function CreateOrgForm({ suggestedName }: { suggestedName: string }) {
  const [name, setName] = React.useState(suggestedName);
  // Slug auto-follows the name until the user edits it directly.
  const [slug, setSlug] = React.useState(slugify(suggestedName));
  const [slugEdited, setSlugEdited] = React.useState(false);

  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const [touched, setTouched] = React.useState(false);
  const nameValid = name.trim().length > 0;
  const slugValid = /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug);

  function onNameChange(value: string) {
    setName(value);
    if (!slugEdited) setSlug(slugify(value));
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setTouched(true);
    if (!nameValid || !slugValid) return;

    setPending(true);
    try {
      const { data, error: err } = await authClient.organization.create({
        name: name.trim(),
        slug,
      });
      if (err) throw err;

      // Make the new org the active tenant for subsequent requests.
      if (data?.id) {
        await authClient.organization.setActive({ organizationId: data.id });
      }

      // If we got here mid-OAuth (the provider's postLogin hook sent an org-less user
      // through onboarding), resume the authorize flow now that an org exists, instead
      // of dropping the user on the dashboard and stranding the MCP/CLI client. The
      // oauthProviderClient fetch plugin auto-attaches the signed oauth_query from this
      // page's URL; /oauth2/continue re-enters authorize (org now present) → consent.
      if (inOAuthFlow()) {
        const res = await oauthConsentClient.oauth2.continue({ postLogin: true });
        const next = (res?.data as { redirect_uri?: string; url?: string } | undefined);
        const url = next?.redirect_uri ?? next?.url;
        if (url) {
          window.location.href = url;
          return;
        }
        // Fall through to the dashboard if the provider returned no redirect.
      }
      window.location.assign("/dashboard");
    } catch (err) {
      setError(describeError(err));
      setPending(false);
    }
  }

  return (
    <Card className="shadow-md">
      <CardHeader className="space-y-1.5">
        <div className="mb-1 grid size-10 place-items-center rounded-lg bg-secondary text-secondary-foreground">
          <Building2 className="size-5" aria-hidden />
        </div>
        <CardTitle>Create your organization</CardTitle>
        <CardDescription>
          Sites live inside an organization, and its slug becomes part of every
          project&rsquo;s URL. You can invite teammates and create more later.
        </CardDescription>
      </CardHeader>

      <CardContent>
        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <div className="space-y-2">
            <Label htmlFor="org-name">Organization name</Label>
            <Input
              id="org-name"
              name="org-name"
              placeholder="Acme Inc."
              value={name}
              onChange={(e) => onNameChange(e.target.value)}
              aria-invalid={touched && !nameValid}
              autoFocus
              disabled={pending}
            />
            {touched && !nameValid && (
              <p className="text-xs text-destructive">
                Enter an organization name.
              </p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="org-slug">URL slug</Label>
            <Input
              id="org-slug"
              name="org-slug"
              placeholder="acme"
              value={slug}
              onChange={(e) => {
                setSlugEdited(true);
                setSlug(slugify(e.target.value));
              }}
              aria-invalid={touched && !slugValid}
              className="font-mono"
              disabled={pending}
            />
            {touched && !slugValid && (
              <p className="text-xs text-destructive">
                Use lowercase letters, numbers, and hyphens.
              </p>
            )}
            <p className="text-xs text-muted-foreground">
              Every project you deploy is served at{" "}
              <span className="font-mono text-foreground">
                {slug || "your-org"}--your-app.dropwaycontent.com
              </span>
              . Your org slug goes in all your project URLs, so pick it carefully
              (it&rsquo;s hard to change later).
            </p>
          </div>

          <Button
            type="submit"
            className="w-full"
            disabled={pending}
            aria-busy={pending}
          >
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Create organization
          </Button>

          {error && (
            <p
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}
        </form>
      </CardContent>
    </Card>
  );
}
