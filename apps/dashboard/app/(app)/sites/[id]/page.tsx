import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import {
  ArrowLeft,
  ExternalLink,
  Globe,
  Link2,
  MessageSquareText,
  Settings,
  Sparkles,
} from "lucide-react";

import { AccessModeBadge } from "@/components/sites/access-mode-badge";
import { ChatPanelToggle } from "@/components/chats/chat-panel-toggle";
import { sourceToolLabel } from "@/components/chats/source-tools";
import { DeployDropzone } from "@/components/sites/deploy-dropzone";
import { DeployTabs } from "@/components/sites/deploy-tabs";
import { RollbackDialog } from "@/components/sites/rollback-dialog";
import { ShareEmbedDialog } from "@/components/sites/share-embed-dialog";
import { addCommentAction } from "@/app/(app)/sites/[id]/actions";
import { SiteComments, type CommentMember } from "@/components/sites/site-comments";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError, type PlanTier, type Site, type SiteComment } from "@/lib/api";
import { customDomainsEntitled, embedBadgeRemovable } from "@/lib/billing";
import { MCP_URL } from "@/lib/env";
import { canManage, loadActiveOrg } from "@/lib/org";
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

  // Fire all three reads concurrently, they don't depend on one another, so
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
  // Whether this org may use custom domains: a PAID feature on the hosted build,
  // so a free-tier org's Domains button routes to the upgrade page instead of the
  // (server-gated) domains page. getBilling 404s on OSS/self-host, which is
  // UNLIMITED (mirrors the server's Unlimited provider) → treat as entitled so a
  // self-hoster with Cloudflare configured is never sent to a nonexistent billing
  // page. A transient failure also fails OPEN here since the server is the real gate.
  // The org's plan tier, used for two display-only entitlements: custom domains and
  // removing the embed's "Powered by Dropway" badge. getBilling 404s on OSS/self-host
  // (UNLIMITED) → null; a transient failure also collapses to null. Both entitlements
  // are enforced server-side, so this only drives which CTAs the UI offers.
  const planTierPromise: Promise<PlanTier | null> = api
    .getBilling()
    .then((b) => (b.plan_tier ?? "free") as PlanTier)
    .catch(() => null);
  // Custom domains fail OPEN on a null tier (self-host/unlimited → entitled, so a
  // self-hoster with Cloudflare configured is never sent to a nonexistent billing page).
  const domainsEntitledPromise = planTierPromise.then((tier) =>
    tier === null ? true : customDomainsEntitled(tier),
  );
  // Badge removal fails CLOSED on a null tier: self-host serves via the Go engine,
  // which injects NO badge at all, so there's nothing to remove — hide the toggle
  // rather than offer a no-op control. (On cloud, a transient billing miss just keeps
  // the badge, which is the safe default.)
  const badgeRemovablePromise = planTierPromise.then((tier) =>
    tier === null ? false : embedBadgeRemovable(tier),
  );
  // Deploy history for the rollback picker (newest first). Best-effort: an empty
  // list just renders the dialog's "no versions yet" state.
  const versionsPromise = api.listVersions(id).catch(() => []);
  // The site's comment thread + the org member list (for author/mention names and
  // the tag picker). Both best-effort so a hiccup never blocks the page.
  const commentsPromise = api.listComments(id).catch((): SiteComment[] => []);
  const orgPromise = loadActiveOrg().catch(() => null);
  // The attached chat log ("How this was made"), if any. Best-effort: a 404
  // (nothing attached) or 403 (org kill switch off) simply hides the card.
  const siteChatPromise = api.getSiteChat(id).catch(() => null);

  let site: Site;
  try {
    site = await sitePromise;
  } catch (err) {
    // 404 (absent or invisible under the tenant) → Next.js not-found page.
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }

  const [
    customDomainsEnabled,
    domainsEntitled,
    badgeRemovable,
    versions,
    comments,
    org,
    siteChat,
  ] = await Promise.all([
    customDomainsPromise,
    domainsEntitledPromise,
    badgeRemovablePromise,
    versionsPromise,
    commentsPromise,
    orgPromise,
    siteChatPromise,
  ]);
  // Free-tier orgs see the Domains button but it routes to the upgrade page; paid
  // (and self-host/unlimited) orgs go straight to the domains manager. The server
  // enforces the same gate.
  const domainsHref = domainsEntitled ? `/sites/${id}/domains` : "/billing";

  const commentMembers: CommentMember[] = (org?.members ?? []).map((m) => ({
    userId: m.userId,
    name: m.name ?? m.email ?? "A teammate",
  }));

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

      {/* Actions. On mobile the buttons are laid out on a tidy grid (primary full
          width, secondary actions two-up) instead of wrapping left-aligned; from
          sm+ they collapse back to a single row with the primary on the left and
          the secondary actions pushed right. */}
      <div className="flex flex-col gap-2 border-y border-border py-3 sm:flex-row sm:flex-wrap sm:items-center">
        <Button asChild size="sm" className="w-full sm:w-auto">
          <Link href={`/sites/${id}/builder`}>
            <Sparkles aria-hidden />
            Build with AI
          </Link>
        </Button>
        <div className="grid grid-cols-2 gap-2 sm:ml-auto sm:flex sm:flex-wrap sm:items-center">
          <Button asChild variant="outline" size="sm" className="w-full sm:w-auto">
            <Link href={`/sites/${id}/settings`}>
              <Settings aria-hidden />
              Access
            </Link>
          </Button>
          {customDomainsEnabled && (
            <Button asChild variant="outline" size="sm" className="w-full sm:w-auto">
              <Link href={domainsHref}>
                <Link2 aria-hidden />
                Domains
              </Link>
            </Button>
          )}
          <ShareEmbedDialog
            liveUrl={liveUrl}
            title={site.slug ?? "site"}
            isPrivate={site.access_mode !== "public"}
            badgeRemovable={badgeRemovable}
            disabled={!isLive}
            triggerClassName="w-full sm:w-auto"
          />
          <RollbackDialog
            siteId={site.id ?? id}
            versions={versions}
            triggerClassName="w-full sm:w-auto"
          />
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
          <Detail label="Version id" value={site.current_version_id ?? "None"} mono />
          <Detail label="Site id" value={site.id ?? id} mono />
          {/* Logical storage = this site's current-version size (not deduplicated
              across sites). 0 before the first deploy. */}
          <Detail label="Storage" value={formatBytes(site.storage_bytes ?? 0)} />
          <Detail
            label="Created"
            value={
              site.created_at
                ? new Date(site.created_at).toLocaleString()
                : "Unknown"
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

      {/* How this was made: the site's attached chat log, when one exists. */}
      {siteChat?.chat_log?.id ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <MessageSquareText className="size-4 text-muted-foreground" aria-hidden />
              How this was made
            </CardTitle>
            <CardDescription>
              The AI conversation behind this site —{" "}
              {siteChat.chat_log.message_count ?? siteChat.messages?.length ?? 0}{" "}
              {(siteChat.chat_log.message_count ?? siteChat.messages?.length ?? 0) === 1
                ? "message"
                : "messages"}{" "}
              from {sourceToolLabel(siteChat.chat_log.source_tool)}.{" "}
              <Link
                href={`/chats/${siteChat.chat_log.id}`}
                className="font-medium text-foreground underline-offset-4 hover:underline"
              >
                Read the transcript
              </Link>
              .
            </CardDescription>
          </CardHeader>
          <CardContent>
            <ChatPanelToggle
              chatId={siteChat.chat_log.id}
              initialEnabled={siteChat.chat_log.panel_enabled ?? false}
              disabled={
                !(
                  (org && canManage(org.myRole)) ||
                  (!!org?.myUserId && siteChat.chat_log.created_by === org.myUserId)
                )
              }
              hasSite
            />
          </CardContent>
        </Card>
      ) : null}

      {/* Comments: org-internal discussion, with @mentions of teammates. */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Comments</CardTitle>
          <CardDescription>
            Discuss this site with your team. Tag a teammate to loop them in.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <SiteComments
            siteId={site.id ?? id}
            initialComments={comments}
            members={commentMembers}
            currentUserId={org?.myUserId ?? null}
            addAction={addCommentAction}
          />
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
