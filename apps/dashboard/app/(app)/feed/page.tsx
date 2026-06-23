import type { Metadata } from "next";
import Link from "next/link";
import { ArrowRight, Globe, Rss } from "lucide-react";

import { AccessModeBadge } from "@/components/sites/access-mode-badge";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { api, ApiError, type Site } from "@/lib/api";
import { loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Feed" };

// The feed is cross-user, live org data; never serve a stale snapshot.
export const dynamic = "force-dynamic";

/**
 * The org feed (server component): every site teammates have shared, newest at
 * the top and older sites at the bottom. A site joins the feed automatically when
 * it's created or published, unless its owner makes it private. Each card is
 * attributed to its owner via the org's member list (Better Auth identities).
 *
 * The (app) layout already guarantees an authenticated session + an active org.
 */
export default async function FeedPage() {
  const [feedResult, orgResult] = await Promise.allSettled([
    api.listFeed(),
    loadActiveOrg(),
  ]);

  let sites: Site[] | null = null;
  let loadError: string | null = null;
  if (feedResult.status === "fulfilled") {
    sites = feedResult.value;
  } else {
    const err = feedResult.reason;
    loadError =
      err instanceof ApiError
        ? `The API returned ${err.status}.`
        : "Couldn't reach the control-plane API.";
  }

  const org = orgResult.status === "fulfilled" ? orgResult.value : null;
  const myUserId = org?.myUserId ?? null;
  const orgName = org?.name ?? "your organization";

  // Map owner user id → display label so each card can be attributed. Falls back
  // to "A teammate" when the identity isn't in the member list (e.g. a removed user).
  const ownerLabel = new Map<string, string>();
  for (const m of org?.members ?? []) {
    if (!m.userId) continue;
    ownerLabel.set(m.userId, m.name ?? m.email ?? "A teammate");
  }
  const labelFor = (ownerId: string | undefined): string => {
    if (ownerId && ownerId === myUserId) return "You";
    if (ownerId && ownerLabel.has(ownerId)) return ownerLabel.get(ownerId)!;
    return "A teammate";
  };

  return (
    <div className="mx-auto max-w-5xl space-y-8">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Feed</h1>
        <p className="text-muted-foreground">
          Sites shared across {orgName}, newest first. Make a site private from its
          access settings to keep it off the feed.
        </p>
      </div>

      {loadError ? (
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          {loadError} Start the API (api.dropway.dev) and reload.
        </Card>
      ) : sites && sites.length > 0 ? (
        <ul className="space-y-3">
          {sites.map((site) => (
            <li key={site.id}>
              <FeedRow site={site} owner={labelFor(site.owner_id)} />
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState />
      )}
    </div>
  );
}

/** One feed item: a teammate's shared site as a clickable card. */
function FeedRow({ site, owner }: { site: Site; owner: string }) {
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
              {site.title?.trim() ? site.title : site.slug}
            </span>
          </div>
          {site.description?.trim() ? (
            <p className="line-clamp-2 text-sm text-muted-foreground">
              {site.description}
            </p>
          ) : null}
          <p className="truncate text-xs text-muted-foreground">
            <span className="text-foreground/80">{owner}</span>
            {" · "}
            {formatWhen(site.created_at)}
          </p>
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
            <span className="size-1.5 rounded-full bg-emerald-500" aria-hidden />
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

/** Shown when no one in the org has shared a site yet. */
function EmptyState() {
  return (
    <Card className="flex flex-col items-center gap-4 border-dashed p-12 text-center">
      <span className="grid size-12 place-items-center rounded-xl bg-secondary text-secondary-foreground">
        <Rss className="size-6" aria-hidden />
      </span>
      <div className="space-y-1">
        <p className="font-medium text-foreground">Nothing shared yet</p>
        <p className="text-sm text-muted-foreground">
          When you or a teammate creates or publishes a site, it shows up here
          automatically — unless it&rsquo;s marked private.
        </p>
      </div>
    </Card>
  );
}

/**
 * A compact "time ago" for the feed, falling back to an absolute date past a
 * week. Server-rendered against the request time; good enough for a discovery
 * list (no live ticking needed).
 */
function formatWhen(iso: string | undefined): string {
  if (!iso) return "recently";
  const then = new Date(iso);
  const ms = Date.now() - then.getTime();
  if (Number.isNaN(ms)) return "recently";
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return "just now";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 7) return `${day}d ago`;
  return then.toLocaleDateString();
}
