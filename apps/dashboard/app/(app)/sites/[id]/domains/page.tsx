import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, ShieldAlert } from "lucide-react";

import { DomainsManager } from "@/components/sites/domains-manager";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError, type Domain, type Site } from "@/lib/api";
import { canManage, loadActiveOrg } from "@/lib/org";

export const dynamic = "force-dynamic";

export async function generateMetadata({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await params;
  const site = await api.getSite(id).catch(() => null);
  return { title: site?.slug ? `${site.slug} · Domains` : "Custom domains" };
}

/**
 * Custom domains for a site (architecture §9, Cloudflare for SaaS). Owners/admins
 * add a hostname; the Go API creates the custom hostname and returns the DNS DCV
 * record to publish. A per-domain poller hits GET /v1/domains/{id}/status to
 * advance verification + TLS; once both are good, the Go API writes the global
 * host route so the custom host serves the site.
 */
export default async function SiteDomainsPage({
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

  const [org, domains] = await Promise.all([
    loadActiveOrg(),
    api.listDomains(id).catch((): Domain[] => []),
  ]);
  const manage = org ? canManage(org.myRole) : false;

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
        <h1 className="text-2xl font-semibold tracking-tight">Custom domains</h1>
        <p className="text-muted-foreground">
          Serve{" "}
          <span className="font-medium text-foreground">{site.slug}</span> from
          your own domain, e.g.{" "}
          <span className="font-mono text-foreground">docs.acme.com</span>.
        </p>
      </div>

      {!manage && (
        <Card className="border-amber-500/30 bg-amber-500/5">
          <CardContent className="flex items-start gap-3 pt-6 text-sm">
            <ShieldAlert
              className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400"
              aria-hidden
            />
            <p className="text-muted-foreground">
              Only owners and admins can add or verify custom domains. The list
              below is read-only for you.
            </p>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Domains</CardTitle>
          <CardDescription>
            Add a domain, create the DNS record we show you, then verify. DNS can
            take a few minutes to propagate.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DomainsManager
            siteId={id}
            initialDomains={domains}
            disabled={!manage}
          />
        </CardContent>
      </Card>
    </div>
  );
}
