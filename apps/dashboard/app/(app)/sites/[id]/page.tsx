import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, ExternalLink, Globe, Link2, Settings } from "lucide-react";

import { AccessModeBadge } from "@/components/sites/access-mode-badge";
import { DeployDropzone } from "@/components/sites/deploy-dropzone";
import { DeployTabs } from "@/components/sites/deploy-tabs";
import { RollbackDialog } from "@/components/sites/rollback-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError, type Site } from "@/lib/api";
import { MCP_URL } from "@/lib/env";
import { formatBytes } from "@/lib/utils";

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

  // Fire all three reads concurrently — they don't depend on one another, so
  // awaiting them in series needlessly tripled this page's time-to-render (each
  // call is its own API round-trip preceded by a JWT mint). getSite still gates
  // rendering (its 404 → not-found), but me()/listVersions() are kicked off in
  // parallel and only awaited after, so they overlap instead of queueing.
  const sitePromise = api.getSite(id);
  // Custom domains are only offered when the server has a real provider configured
  // (Cloudflare for SaaS). Hidden in self-host/dev where they can't be verified.
  const customDomainsPromise = api
    .me()
    .then((me) => me.custom_domains_enabled ?? false)
    .catch(() => false);
  // Deploy history for the rollback picker (newest first). Best-effort: an empty
  // list just renders the dialog's "no versions yet" state.
  const versionsPromise = api.listVersions(id).catch(() => []);

  let site: Site;
  try {
    site = await sitePromise;
  } catch (err) {
    // 404 (absent or invisible under the tenant) → Next.js not-found page.
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }

  const [customDomainsEnabled, versions] = await Promise.all([
    customDomainsPromise,
    versionsPromise,
  ]);

  const isLive = Boolean(site.current_version_id);
  const liveUrl = site.live_url ?? `https://${site.slug}.dropwaycontent.com`;

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
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild variant="outline" size="sm">
            <Link href={`/sites/${id}/settings`}>
              <Settings aria-hidden />
              Access
            </Link>
          </Button>
          {customDomainsEnabled && (
            <Button asChild variant="outline" size="sm">
              <Link href={`/sites/${id}/domains`}>
                <Link2 aria-hidden />
                Domains
              </Link>
            </Button>
          )}
          <RollbackDialog siteId={site.id ?? id} versions={versions} />
        </div>
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
              className="inline-flex max-w-full items-center gap-2 rounded-md border border-border bg-muted/50 px-3 py-2 font-mono text-sm text-foreground transition-colors hover:border-foreground/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
            >
              <span className="min-w-0 break-all">{liveUrl}</span>
              <ExternalLink className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            </a>
          ) : (
            <div className="break-all rounded-md border border-dashed border-border px-3 py-2 font-mono text-sm text-muted-foreground">
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
          {/* Logical storage = this site's current-version size (not deduplicated
              across sites). 0 before the first deploy. */}
          <Detail label="Storage" value={formatBytes(site.storage_bytes ?? 0)} />
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

      {/* Deploy: drag-and-drop a folder (drop → live) */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Deploy</CardTitle>
          <CardDescription>
            Drag &amp; drop a folder of static files to {isLive ? "ship a new version" : "go live"}.
            Only changed files upload, and your folder is live the moment it finishes.
            Prefer the terminal? Upload the same folder with the CLI below.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DeployDropzone siteId={site.id ?? id} isLive={isLive} />
        </CardContent>
      </Card>

      {/* Or deploy via MCP / CLI (tabbed; MCP-first for non-technical users) */}
      <DeployTabs
        slug={site.slug ?? id}
        mcpConnectorUrl={`${MCP_URL.replace(/\/$/, "")}/mcp`}
      />
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
