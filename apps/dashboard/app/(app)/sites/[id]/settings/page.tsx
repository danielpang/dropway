import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, ExternalLink, ShieldAlert } from "lucide-react";

import { AccessModeBadge } from "@/components/sites/access-mode-badge";
import { AccessSettingsForm } from "@/components/sites/access-settings-form";
import { AllowlistManager } from "@/components/sites/allowlist-manager";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError, type AllowlistEntry, type Site } from "@/lib/api";
import { canManage, loadActiveOrg } from "@/lib/org";

export const dynamic = "force-dynamic";

export async function generateMetadata({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await params;
  const site = await api.getSite(id).catch(() => null);
  return { title: site?.slug ? `${site.slug} · Access` : "Access settings" };
}

/**
 * Site access settings. Owner/admin set the access mode
 * (public / unlisted / password / allowlist / org-only), an optional link
 * expiry, and, for allowlist, manage the per-site email list. Every write goes
 * to the Go API, which re-checks role and the org's external-sharing policy; a
 * member who lands here sees a read-only view with the live access state.
 */
export default async function SiteAccessSettingsPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;

  let site: Site;
  try {
    site = await api.getSite(id);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }

  const org = await loadActiveOrg();
  const manage = org ? canManage(org.myRole) : false;
  const mode = site.access_mode ?? "public";

  // Only fetch the allowlist when it's relevant (allowlist mode), and tolerate
  // a non-admin 403 by degrading to an empty list.
  let allowlist: AllowlistEntry[] = [];
  if (mode === "allowlist") {
    allowlist = await api.listAllowlist(id).catch(() => []);
  }

  const liveUrl = site.live_url ?? `https://${site.slug}.dropwaycontent.com`;

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Link
        href={`/sites/${id}`}
        className="inline-flex items-center gap-1.5 rounded-sm text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
      >
        <ArrowLeft className="size-4" aria-hidden />
        Back to {site.slug}
      </Link>

      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Access</h1>
        <p className="text-muted-foreground">
          Control who can view{" "}
          <span className="font-medium text-foreground">{site.slug}</span>.
        </p>
      </div>

      {/* Current state */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Current access</CardTitle>
          <CardDescription>
            The live URL and the access mode it serves under right now.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <AccessModeBadge mode={mode} />
          </div>
          <a
            href={liveUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 rounded-md border border-border bg-muted/50 px-3 py-2 font-mono text-sm text-foreground transition-colors hover:border-foreground/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          >
            {liveUrl}
            <ExternalLink className="size-3.5 text-muted-foreground" aria-hidden />
          </a>
        </CardContent>
      </Card>

      {!manage && (
        <Card className="border-amber-500/30 bg-amber-500/5">
          <CardContent className="flex items-start gap-3 pt-6 text-sm">
            <ShieldAlert
              className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400"
              aria-hidden
            />
            <p className="text-muted-foreground">
              Only owners and admins can change a site&rsquo;s access. Ask an
              admin if you need to share this site differently.
            </p>
          </CardContent>
        </Card>
      )}

      {/* Access mode form (admin/owner) */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Access mode</CardTitle>
          <CardDescription>
            Pick how viewers reach this site. Password and allowlist links can
            optionally expire.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <AccessSettingsForm
            siteId={id}
            currentMode={mode}
            disabled={!manage}
          />
        </CardContent>
      </Card>

      {/* Allowlist (only meaningful for allowlist mode) */}
      {mode === "allowlist" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Allowlist</CardTitle>
            <CardDescription>
              Only these verified emails can view the site. External emails (not
              on an org-verified domain) require external sharing to be enabled.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <AllowlistManager
              siteId={id}
              initialEntries={allowlist}
              disabled={!manage}
            />
          </CardContent>
        </Card>
      )}
    </div>
  );
}
