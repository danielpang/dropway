import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, Brain } from "lucide-react";

import { MemoryAccess } from "@/components/settings/memory-access";
import { MemoryView } from "@/components/settings/memory-view";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError, type Memory } from "@/lib/api";
import { canManage, loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Company memory" };
export const dynamic = "force-dynamic";

/**
 * Company-memory management: browse, filter, and curate the durable facts
 * Dropway has learned about the org (from AI builds, shared chats, sites and
 * skills, or added by hand). Any member can browse and add; pin/disable/edit/
 * delete are owner/admin only — the Go API re-checks the role on every write.
 */
export default async function MemoryPage(props: {
  searchParams: Promise<{
    q?: string;
    kind?: string;
    pinned?: string;
    disabled?: string;
  }>;
}) {
  const searchParams = await props.searchParams;
  const filters = {
    q: searchParams.q ?? "",
    kind: searchParams.kind ?? "",
    pinned: searchParams.pinned === "true",
    disabled: searchParams.disabled === "true",
  };

  const org = await loadActiveOrg();

  if (!org) {
    return (
      <div className="mx-auto max-w-3xl">
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          Couldn&rsquo;t load your organization. Reload to try again.
        </Card>
      </div>
    );
  }

  const manage = canManage(org.myRole);

  // The settings route works even when the org flag is off; only a 503 (no
  // embeddings configured on this deployment) — or a transient error — nulls it.
  const settings = await api.getMemorySettings().catch(() => null);

  // Load the list only when the feature is on (the API 403s otherwise).
  let memories: Memory[] = [];
  let loadError: string | null = null;
  if (settings?.memory_enabled) {
    try {
      memories = await api.listMemories({
        q: filters.q || undefined,
        kind: filters.kind || undefined,
        pinned: filters.pinned || undefined,
        disabled: filters.disabled || undefined,
      });
    } catch (err) {
      loadError =
        err instanceof ApiError
          ? `The API returned ${err.status}.`
          : "Couldn't reach the control-plane API.";
    }
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div className="space-y-2">
        <Button
          asChild
          variant="ghost"
          size="sm"
          className="-ml-2 text-muted-foreground"
        >
          <Link href="/settings">
            <ArrowLeft className="size-4" aria-hidden />
            Organization settings
          </Link>
        </Button>
        <div className="space-y-1">
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
            <Brain className="size-5 text-muted-foreground" aria-hidden />
            Company memory
          </h1>
          <p className="text-muted-foreground">
            Durable facts Dropway has learned about{" "}
            <span className="font-medium text-foreground">
              {org.name ?? "your organization"}
            </span>{" "}
            and recalls in future AI work.
          </p>
        </div>
      </div>

      {!settings ? (
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          Company memory isn&rsquo;t available on this deployment (no embeddings
          are configured). Nothing to manage here.
        </Card>
      ) : settings.plan_allowed === false ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">
              Company memory requires a Pro plan
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              Upgrade to Pro or above to let Dropway remember your
              organization&rsquo;s brand, preferences, and past work — and
              recall them in the AI builder, MCP tools, and CLI.
            </p>
          </CardContent>
        </Card>
      ) : !settings.memory_enabled ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">
              Company memory is turned off
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <p className="text-sm text-muted-foreground">
              While it&rsquo;s off, Dropway neither records new memories nor
              recalls stored ones. Stored memories are kept, not deleted.
            </p>
            {manage ? (
              <MemoryAccess
                initialEnabled={settings.memory_enabled}
                canManage={manage}
              />
            ) : (
              <div className="flex flex-wrap items-center gap-3">
                <p className="text-sm text-muted-foreground">
                  Ask an owner or admin to enable it in your organization
                  settings.
                </p>
                <Button asChild variant="outline" size="sm">
                  <Link href="/settings">Go to settings</Link>
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      ) : (
        <MemoryView
          memories={memories}
          canManage={manage}
          filters={filters}
          count={settings.count}
          max={settings.max ?? 0}
          loadError={loadError}
        />
      )}
    </div>
  );
}
