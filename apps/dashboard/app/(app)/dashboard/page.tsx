import type { Metadata } from "next";
import Link from "next/link";
import { ArrowRight, EyeOff, Globe, Rocket } from "lucide-react";

import { AccessModeBadge } from "@/components/sites/access-mode-badge";
import { NewSiteDialog } from "@/components/sites/new-site-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { api, ApiError, type Site } from "@/lib/api";
import { loadOrgBillingState } from "@/lib/billing-server";
import { loadActiveOrg } from "@/lib/org";
import { isSiteVisibleTo, ownerLabel } from "@/lib/sites-visibility";

export const metadata: Metadata = { title: "Sites" };

// Always render against live API data; sites are per-tenant and mutate often.
export const dynamic = "force-dynamic";

/**
 * The org's sites (server component). Lists every site in the caller's tenant
 * via GET /v1/sites — org-shared sites, plus the viewer's own (see
 * lib/sites-visibility.ts; it's a discovery filter, not access control) — with
 * an All/Mine filter driven by `?owner=me` and a "New site" dialog that POSTs to
 * the API. The (app) layout already guarantees an authenticated session + an
 * active organization before this renders.
 */
export default async function DashboardPage({
  searchParams,
}: {
  searchParams: Promise<{ owner?: string }>;
}) {
  let sites: Site[] | null = null;
  let loadError: string | null = null;

  // Billing-derived read-only state (over_limit / past_due) disables "New site".
  // UX mirror of server enforcement; loads in parallel with the sites list.
  const [{ owner }, [sitesResult, billing, activeOrg]] = await Promise.all([
    searchParams,
    Promise.allSettled([
      api.listSites(),
      loadOrgBillingState(),
      loadActiveOrg(),
    ]),
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

  const org = activeOrg.status === "fulfilled" ? activeOrg.value : null;

  // Org slug for the "New site" URL preview (<org-slug>--<site-slug>.dropwaycontent.com).
  const orgSlug = org?.slug ?? null;

  // Without the org we have no viewer identity, so the listing rule would hide
  // the viewer's OWN private sites — a transient auth hiccup would read as data
  // loss. Fail open to the pre-filter behavior instead: show everything, and
  // drop the filter row, bylines, and footnote. Safe because the rule is
  // discovery, not access control (the API returns these rows either way).
  const degraded = org === null;
  const viewer = { userId: org?.myUserId ?? null };
  const members = org?.members ?? [];

  const allVisible = degraded
    ? (sites ?? [])
    : (sites ?? []).filter((site) => isSiteVisibleTo(site, viewer));
  const mine = allVisible.filter(
    (site) => Boolean(site.owner_id) && site.owner_id === viewer.userId,
  );

  const mineOnly = !degraded && owner === "me";
  const shown = mineOnly ? mine : allVisible;
  const showFilters = !degraded && (allVisible.length > 0 || mineOnly);

  return (
    <div className="mx-auto max-w-5xl space-y-8">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0 flex-1 space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Sites</h1>
          {/* Kept verbatim in loading.tsx so the heading block doesn't shift. */}
          <p className="text-muted-foreground">
            Every site in your org. Deploy a folder, get a live,
            access-controlled URL.
          </p>
        </div>
        {/* ml-auto keeps the control right-aligned when the header wraps on narrow viewports. */}
        <div className="ml-auto shrink-0">
          <NewSiteDialog readOnly={readOnly} orgSlug={orgSlug} />
        </div>
      </div>

      {!loadError && showFilters ? (
        <nav
          aria-label="Filter sites by owner"
          className="flex flex-wrap items-center gap-2"
        >
          <FilterChip href="/dashboard" active={!mineOnly} count={allVisible.length}>
            All
          </FilterChip>
          {/* An active "Mine" links back to All so a second tap always toggles off. */}
          <FilterChip
            href={mineOnly ? "/dashboard" : "/dashboard?owner=me"}
            active={mineOnly}
            count={mine.length}
          >
            Mine
          </FilterChip>
        </nav>
      ) : null}

      {loadError ? (
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          {loadError} Start the API (api.dropway.dev) and reload.
        </Card>
      ) : allVisible.length === 0 ? (
        // Checked before the filter: with an empty org, "Browse all 0 sites"
        // would be a dead end.
        <EmptyState readOnly={readOnly} orgSlug={orgSlug} />
      ) : shown.length === 0 ? (
        <NoSitesOfYourOwn
          readOnly={readOnly}
          orgSlug={orgSlug}
          totalCount={allVisible.length}
        />
      ) : (
        <>
          <ul
            className="grid grid-cols-1 gap-3 sm:grid-cols-2"
            aria-label={mineOnly ? "Your sites" : "All sites"}
          >
            {shown.map((site) => (
              <li key={site.id}>
                <SiteRow
                  site={site}
                  owner={
                    degraded ? null : ownerLabel(site.owner_id, viewer, members)
                  }
                />
              </li>
            ))}
          </ul>
          {degraded ? null : (
            <p className="text-center text-xs text-muted-foreground">
              Sites marked private are only shown to their owner.
            </p>
          )}
        </>
      )}
    </div>
  );
}

/**
 * One All/Mine chip. A link rather than a button so the page stays a server
 * component: no client bundle, and middle-click / back-button work for free.
 */
function FilterChip({
  href,
  active,
  count,
  children,
}: {
  href: string;
  active: boolean;
  count: number;
  children: React.ReactNode;
}) {
  return (
    <Button asChild variant={active ? "secondary" : "ghost"} size="sm">
      <Link href={href} aria-current={active ? "page" : undefined}>
        {children}
        <span className="ml-1.5 text-xs text-muted-foreground">{count}</span>
      </Link>
    </Button>
  );
}

/**
 * A single site as a clickable card linking to its detail page. `owner` is the
 * byline label, or null when the org (and so the viewer's identity) is
 * unavailable.
 */
function SiteRow({ site, owner }: { site: Site; owner: string | null }) {
  const isLive = Boolean(site.current_version_id);
  return (
    <Link
      href={`/sites/${site.id}`}
      className="group block rounded-lg border border-border bg-card p-5 shadow-sm transition-colors hover:border-foreground/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="grid size-8 shrink-0 place-items-center rounded-md bg-secondary text-secondary-foreground">
              <Globe className="size-4" aria-hidden />
            </span>
            <span className="min-w-0 truncate font-medium text-foreground">
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

      <div className="mt-4 flex flex-wrap items-center gap-2">
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
        {site.feed_visible === false ? (
          <Badge
            variant="outline"
            title="Hidden from the org feed. Only its owner sees it in this list."
          >
            <EyeOff className="size-3" aria-hidden />
            Hidden
          </Badge>
        ) : null}
        {owner ? (
          <span className="ml-auto min-w-0 truncate text-xs text-muted-foreground">
            by {owner}
          </span>
        ) : null}
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

/** Shown under ?owner=me when the org has sites but the viewer owns none. */
function NoSitesOfYourOwn({
  readOnly,
  orgSlug,
  totalCount,
}: {
  readOnly: boolean;
  orgSlug: string | null;
  totalCount: number;
}) {
  return (
    <Card className="flex flex-col items-center gap-4 border-dashed p-12 text-center">
      <span className="grid size-12 place-items-center rounded-xl bg-secondary text-secondary-foreground">
        <Rocket className="size-6" aria-hidden />
      </span>
      <div className="space-y-1">
        <p className="font-medium text-foreground">
          You haven&rsquo;t created a site yet
        </p>
        <p className="text-sm text-muted-foreground">
          Create one, then run{" "}
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">
            dropway deploy ./dist
          </code>{" "}
          to push your first deploy.
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-center gap-2">
        <NewSiteDialog readOnly={readOnly} orgSlug={orgSlug} />
        <Button variant="outline" asChild>
          <Link href="/dashboard">
            Browse all {totalCount} {totalCount === 1 ? "site" : "sites"}
          </Link>
        </Button>
      </div>
    </Card>
  );
}
