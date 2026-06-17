import type { Metadata } from "next";
import Link from "next/link";
import { ArrowRight, Globe, Rocket } from "lucide-react";

import { AccessModeBadge } from "@/components/sites/access-mode-badge";
import { NewSiteDialog } from "@/components/sites/new-site-dialog";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { api, ApiError, type Site } from "@/lib/api";
import { loadOrgBillingState } from "@/lib/billing-server";
import { loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Sites" };

// Always render against live API data; sites are per-tenant and mutate often.
export const dynamic = "force-dynamic";

/**
 * The org's sites (server component). Lists every site visible under the
 * caller's tenant via GET /v1/sites, with a "New site" dialog that POSTs to the
 * API. The (app) layout already guarantees an authenticated session + an active
 * organization before this renders.
 */
export default async function DashboardPage() {
  let sites: Site[] | null = null;
  let loadError: string | null = null;

  // Billing-derived read-only state (over_limit / past_due) disables "New site"
  // (§9). UX mirror of server enforcement; loads in parallel with the sites list.
  const [sitesResult, billing, activeOrg] = await Promise.allSettled([
    api.listSites(),
    loadOrgBillingState(),
    loadActiveOrg(),
  ]);

  if (sitesResult.status === "fulfilled") {
    sites = sitesResult.value;
  } else {
    const err = sitesResult.reason;
    // The Go API may be unreachable in local dev; degrade to an inline notice
    // rather than crashing the shell.
    loadError =
      err instanceof ApiError
        ? `The API returned ${err.status}.`
        : "Couldn't reach the control-plane API.";
  }

  const readOnly =
    billing.status === "fulfilled" ? billing.value.readOnly : false;

  // Org slug for the "New site" URL preview (<org-slug>--<site-slug>.dropwaycontent.com).
  const orgSlug =
    activeOrg.status === "fulfilled" ? (activeOrg.value?.slug ?? null) : null;

  return (
    <div className="mx-auto max-w-5xl space-y-8">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Sites</h1>
          <p className="text-muted-foreground">
            Deploy a folder, get a live, access-controlled URL.
          </p>
        </div>
        <NewSiteDialog readOnly={readOnly} orgSlug={orgSlug} />
      </div>

      {loadError ? (
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          {loadError} Start the API (api.dropway.dev) and reload.
        </Card>
      ) : sites && sites.length > 0 ? (
        <ul className="grid gap-3 sm:grid-cols-2">
          {sites.map((site) => (
            <li key={site.id}>
              <SiteRow site={site} />
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState readOnly={readOnly} orgSlug={orgSlug} />
      )}
    </div>
  );
}

/** A single site as a clickable card linking to its detail page. */
function SiteRow({ site }: { site: Site }) {
  const isLive = Boolean(site.current_version_id);
  return (
    <Link
      href={`/sites/${site.id}`}
      className="group block rounded-lg border border-border bg-card p-5 shadow-sm transition-colors hover:border-foreground/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex items-center gap-2">
            <span className="grid size-8 shrink-0 place-items-center rounded-md bg-secondary text-secondary-foreground">
              <Globe className="size-4" aria-hidden />
            </span>
            <span className="truncate font-medium text-foreground">
              {site.slug}
            </span>
          </div>
          <p className="truncate font-mono text-xs text-muted-foreground">
            {site.live_url ?? `${site.slug}.dropwaycontent.com`}
          </p>
        </div>
        <ArrowRight
          className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5"
          aria-hidden
        />
      </div>

      <div className="mt-4 flex items-center gap-2">
        {isLive ? (
          <Badge variant="success">
            <span
              className="size-1.5 rounded-full bg-emerald-500"
              aria-hidden
            />
            Live
          </Badge>
        ) : (
          <Badge variant="muted">Not deployed</Badge>
        )}
        <AccessModeBadge mode={site.access_mode} />
      </div>
    </Link>
  );
}

/** Shown when the org has no sites yet. */
function EmptyState({
  readOnly,
  orgSlug,
}: {
  readOnly: boolean;
  orgSlug: string | null;
}) {
  return (
    <Card className="flex flex-col items-center gap-4 border-dashed p-12 text-center">
      <span className="grid size-12 place-items-center rounded-xl bg-secondary text-secondary-foreground">
        <Rocket className="size-6" aria-hidden />
      </span>
      <div className="space-y-1">
        <p className="font-medium text-foreground">No sites yet</p>
        <p className="text-sm text-muted-foreground">
          Create a site, then run{" "}
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">
            dropway deploy ./dist
          </code>{" "}
          to push your first deploy.
        </p>
      </div>
      <NewSiteDialog readOnly={readOnly} orgSlug={orgSlug} />
    </Card>
  );
}
