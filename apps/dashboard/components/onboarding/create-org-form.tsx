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
import { authClient } from "@/lib/auth-client";

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
          Sites live inside an organization. You can invite teammates and create
          more later.
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
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">shipped.app/</span>
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
            </div>
            {touched && !slugValid && (
              <p className="text-xs text-destructive">
                Use lowercase letters, numbers, and hyphens.
              </p>
            )}
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
