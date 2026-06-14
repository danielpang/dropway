import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, ExternalLink, Globe } from "lucide-react";

import { AccessModeBadge } from "@/components/sites/access-mode-badge";
import { DeployInstructions } from "@/components/sites/deploy-instructions";
import { RollbackDialog } from "@/components/sites/rollback-dialog";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError, type Site } from "@/lib/api";

export const dynamic = "force-dynamic";

export async function generateMetadata({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await params;
  const site = await api.getSite(id).catch(() => null);
  return { title: site?.slug ? `${site.slug} · Site` : "Site" };
}

/**
 * Site detail (server component): the site's identity, its current live URL +
 * deployed version, a "deploy via CLI" panel, and a rollback action that
 * re-publishes an earlier version. Versions aren't listed in the Phase 1 API
 * surface, so the live version is shown directly and rollback takes a version id
 * (the value the CLI prints on each deploy).
 */
export default async function SiteDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;

  let site: Site;
  try {
    site = await api.getSite(id);
  } catch (err) {
    // 404 (absent or invisible under the tenant) → Next.js not-found page.
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }

  const isLive = Boolean(site.current_version_id);
  const liveUrl = site.live_url ?? `https://${site.slug}.shippedusercontent.com`;

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Link
        href="/dashboard"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-sm"
      >
        <ArrowLeft className="size-4" aria-hidden />
        All sites
      </Link>

      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-center gap-3">
          <span className="grid size-10 place-items-center rounded-lg bg-secondary text-secondary-foreground">
            <Globe className="size-5" aria-hidden />
          </span>
          <div className="space-y-1">
            <h1 className="text-2xl font-semibold tracking-tight">{site.slug}</h1>
            <div className="flex items-center gap-2">
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
          </div>
        </div>
        <RollbackDialog
          siteId={site.id ?? id}
          currentVersionId={site.current_version_id ?? null}
        />
      </div>

      {/* Live URL */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Live URL</CardTitle>
          <CardDescription>
            {isLive
              ? "The current published version is served here."
              : "Deploy a version to bring this URL online."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLive ? (
            <a
              href={liveUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 rounded-md border border-border bg-muted/50 px-3 py-2 font-mono text-sm text-foreground transition-colors hover:border-foreground/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
            >
              {liveUrl}
              <ExternalLink className="size-3.5 text-muted-foreground" aria-hidden />
            </a>
          ) : (
            <div className="rounded-md border border-dashed border-border px-3 py-2 font-mono text-sm text-muted-foreground">
              {liveUrl}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Current version */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Current version</CardTitle>
          <CardDescription>
            The immutable deploy this site is pointed at. Roll back by publishing
            an earlier version id.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <Detail label="Version id" value={site.current_version_id ?? "—"} mono />
          <Detail label="Site id" value={site.id ?? id} mono />
          <Detail
            label="Created"
            value={
              site.created_at
                ? new Date(site.created_at).toLocaleString()
                : "—"
            }
          />
        </CardContent>
      </Card>

      {/* Deploy via CLI */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Deploy via CLI</CardTitle>
          <CardDescription>
            Push a folder of static files. Each deploy prints a version id you
            can publish or roll back to.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DeployInstructions slug={site.slug ?? id} />
        </CardContent>
      </Card>
    </div>
  );
}

function Detail({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-muted-foreground">{label}</span>
      <span
        className={`truncate text-right text-foreground${mono ? " font-mono text-xs" : ""}`}
      >
        {value}
      </span>
    </div>
  );
}
