import Link from "next/link";
import { ArrowLeft } from "lucide-react";

import { Skeleton } from "@/components/ui/skeleton";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

/**
 * Instant route-level loading UI for the site detail page. Next.js renders this
 * as the Suspense fallback the moment a site is clicked, so the page's structure
 * paints immediately while the (force-dynamic) server component fetches the site
 * over the API. The static scaffolding — back link, card titles and descriptions
 * — is real; only the data-dependent content (slug, badges, live URL, version
 * details) is skeletoned, mirroring the layout of page.tsx so the swap-in is
 * seamless and doesn't shift.
 */
export default function SiteDetailLoading() {
  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Link
        href="/dashboard"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-sm"
      >
        <ArrowLeft className="size-4" aria-hidden />
        All sites
      </Link>

      {/* Header: icon + slug + status badges, with the action buttons. */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-center gap-3">
          <Skeleton className="size-10 rounded-lg" />
          <div className="space-y-2">
            <Skeleton className="h-7 w-40" />
            <div className="flex items-center gap-2">
              <Skeleton className="h-5 w-16 rounded-full" />
              <Skeleton className="h-5 w-20 rounded-full" />
            </div>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Skeleton className="h-8 w-24" />
          <Skeleton className="h-8 w-24" />
        </div>
      </div>

      {/* Live URL */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Live URL</CardTitle>
          <CardDescription>
            The current published version is served here.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Skeleton className="h-10 w-full max-w-sm" />
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
        <CardContent className="space-y-3">
          {["Version id", "Site id", "Storage", "Created"].map((label) => (
            <div key={label} className="flex items-center justify-between gap-4">
              <span className="text-sm text-muted-foreground">{label}</span>
              <Skeleton className="h-4 w-32" />
            </div>
          ))}
        </CardContent>
      </Card>

      {/* Deploy */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Deploy</CardTitle>
          <CardDescription>
            Drag &amp; drop a folder of static files. Only changed files upload,
            and your folder is live the moment it finishes.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Skeleton className="h-32 w-full rounded-lg" />
        </CardContent>
      </Card>

      {/* More ways to deploy (MCP / CLI tabs) */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">More ways to deploy</CardTitle>
          <CardDescription>
            Connect an AI assistant once, then just ask it to deploy — it calls
            deploy_site and hands back the live URL.
          </CardDescription>
          <Skeleton className="mt-2 h-10 w-full rounded-lg" />
        </CardHeader>
        <CardContent>
          <Skeleton className="h-24 w-full" />
        </CardContent>
      </Card>
    </div>
  );
}
