import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, KeyRound, ShieldAlert } from "lucide-react";

import { ApiKeysManager } from "@/components/settings/api-keys-manager";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, type ApiKey } from "@/lib/api";
import { canManage, loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "API keys" };
export const dynamic = "force-dynamic";

/**
 * Org API-keys management (owner/admin only). API keys let a CI job, a server
 * script, the SDK, or the headless CLI create and deploy sites over the API
 * without an interactive login. Managers can flip the org-wide kill switch,
 * create keys (with a one-time secret reveal), and revoke them. Non-managers get
 * a read-only notice — the Go API re-checks owner/admin on every write regardless.
 */
export default async function ApiKeysPage() {
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

  // Only managers can read the key list (the API 403s a non-manager); avoid the
  // guaranteed-failing calls for members and render the read-only notice instead.
  let keys: ApiKey[] = [];
  let enabled = true;
  if (manage) {
    keys = await api.listApiKeys().catch(() => []);
    enabled = await api
      .getOrgPolicy()
      .then((p) => p.api_keys_enabled)
      .catch(() => true);
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
            <KeyRound className="size-5 text-muted-foreground" aria-hidden />
            API keys
          </h1>
          <p className="text-muted-foreground">
            Programmatic access for CI, scripts, the SDK, and the CLI. An API
            key can create, upload, and delete sites.
          </p>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">
            Keys for {org.name ?? "your organization"}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {manage ? (
            <ApiKeysManager initialKeys={keys} initialEnabled={enabled} />
          ) : (
            <div className="flex items-start gap-3 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-3 text-sm">
              <ShieldAlert
                className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400"
                aria-hidden
              />
              <div className="space-y-1">
                <p className="text-foreground">
                  Only owners and admins can manage API keys.
                </p>
                <p className="text-muted-foreground">
                  Ask an owner or admin if you need a key.
                </p>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {manage && (
        <p className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
          <Badge variant="muted" className="font-normal">
            Tip
          </Badge>
          Set the key as <code className="font-mono">DROPWAY_API_KEY</code> in
          your CI environment — the SDK and CLI pick it up automatically.
        </p>
      )}
    </div>
  );
}
