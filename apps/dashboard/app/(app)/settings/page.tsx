import type { Metadata } from "next";
import Link from "next/link";
import {
  Bot,
  Brain,
  CreditCard,
  Globe2,
  KeyRound,
  MessageSquareText,
  ShieldAlert,
  Sparkles,
  Users,
} from "lucide-react";

import { AiBuilderAccess } from "@/components/settings/ai-builder-access";
import { ChatLogsAccess } from "@/components/settings/chat-logs-access";
import { ExternalSharingToggle } from "@/components/settings/external-sharing-toggle";
import { McpAccess } from "@/components/settings/mcp-access";
import { MemoryAccess } from "@/components/settings/memory-access";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api } from "@/lib/api";
import { MCP_URL } from "@/lib/env";
import { canManage, loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Organization settings" };
export const dynamic = "force-dynamic";

/**
 * Organization settings. The headline control is the
 * org-wide external-sharing policy: owners/admins can allow members to share
 * sites publicly or with external (non-org) emails. Disabling it downgrades any
 * existing external/public sites, the toggle confirms how many were affected.
 * The Go API is the authz boundary and re-checks owner/admin on the write.
 */
export default async function OrgSettingsPage() {
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

  // Render the toggles in their LIVE state (H10), not hardcoded defaults. On a
  // transient API error fall back to the safe defaults (external sharing OFF; MCP
  // ON, its column default), the steady-state values show on the next load.
  const policy = await api
    .getOrgPolicy()
    .catch(() => ({ allow_external_sharing: false, mcp_enabled: true }));
  const allowExternalSharing = policy.allow_external_sharing;
  const mcpEnabled = policy.mcp_enabled;

  // The AI builder card is shown ONLY on a paid plan (the builder requires one,
  // so a free org has nothing to toggle). getBilling 404s on OSS/self-host, which
  // is unlimited → treat as paid so a self-hoster still sees the toggle. We read
  // the live ai_enabled state; a 503 (builder not configured) hides the card.
  const planTier = await api
    .getBilling()
    .then((b) => b.plan_tier ?? "free")
    .catch(() => "pro"); // OSS/self-host: unlimited, show the toggle
  const aiSettings = await api.getAIOrgSettings().catch(() => null);
  const showAiBuilder = planTier !== "free" && aiSettings !== null;

  // Company memory: the settings route works even when the org flag is off, so
  // the card can always render the toggle in its live state. A 503 (no
  // embeddings configured on this deployment) — or any read error — hides the
  // card entirely; there is nothing to toggle then.
  const memorySettings = await api.getMemorySettings().catch(() => null);

  // Chat logs default ON (the column default), so a transient read error falls
  // back to enabled; the steady-state value shows on the next load.
  const chatLogsEnabled = await api
    .getChatSettings()
    .then((s) => s.enabled)
    .catch(() => true);

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">
          Organization settings
        </h1>
        <p className="text-muted-foreground">
          Settings for{" "}
          <span className="font-medium text-foreground">
            {org.name ?? "your organization"}
          </span>
          .
        </p>
      </div>

      {/* External sharing policy */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Globe2 className="size-4 text-muted-foreground" aria-hidden />
            External sharing
          </CardTitle>
          <CardDescription>
            Whether sites can be shared publicly or with people outside your
            verified domains.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {manage ? (
            <ExternalSharingToggle initialEnabled={allowExternalSharing} />
          ) : (
            <div className="flex items-start gap-3 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-3 text-sm">
              <ShieldAlert
                className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400"
                aria-hidden
              />
              <p className="text-muted-foreground">
                Only owners and admins can change the external-sharing policy.
              </p>
            </div>
          )}
        </CardContent>
      </Card>

      {/* LLM / MCP access */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Bot className="size-4 text-muted-foreground" aria-hidden />
            LLM access (MCP)
          </CardTitle>
          <CardDescription>
            Let AI tools reach your sites through the Dropway MCP server. Public
            sites are also crawlable via{" "}
            <code className="font-mono text-xs">/llms.txt</code>; gated sites
            stay private.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <McpAccess
            initialEnabled={mcpEnabled}
            canManage={manage}
            mcpUrl={MCP_URL}
          />
        </CardContent>
      </Card>

      {/* AI website builder (paid plans only) */}
      {showAiBuilder && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Sparkles className="size-4 text-muted-foreground" aria-hidden />
              AI website builder
            </CardTitle>
            <CardDescription>
              Let members create and edit sites by chatting with AI. Usage is
              billed at the provider&rsquo;s cost.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <AiBuilderAccess
              initialEnabled={aiSettings.ai_enabled}
              canManage={manage}
            />
          </CardContent>
        </Card>
      )}

      {/* Company memory */}
      {memorySettings && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Brain className="size-4 text-muted-foreground" aria-hidden />
              Company memory
            </CardTitle>
            <CardDescription>
              Dropway learns durable facts about your organization from AI
              builds, shared chats, sites and skills, and recalls them in
              future work.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {memorySettings.plan_allowed === false ? (
              <p className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
                Company memory requires a Pro plan or above. Upgrade your plan
                in billing to let Dropway remember your organization.
              </p>
            ) : (
              <>
                <MemoryAccess
                  initialEnabled={memorySettings.memory_enabled}
                  canManage={manage}
                />
                <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
                  <p className="text-sm text-muted-foreground">
                    {memorySettings.count === 1
                      ? "1 memory stored"
                      : `${memorySettings.count} memories stored`}
                    {(memorySettings.max ?? 0) > 0 && (
                      <> of {memorySettings.max}</>
                    )}
                  </p>
                  <Button asChild variant="outline">
                    <Link href="/settings/memory">Manage memory</Link>
                  </Button>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      )}

      {/* Shared chat logs */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <MessageSquareText
              className="size-4 text-muted-foreground"
              aria-hidden
            />
            Shared chat logs
          </CardTitle>
          <CardDescription>
            Let members share the AI conversations behind their sites, to the
            chat library and as a &ldquo;How this was made&rdquo; panel.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ChatLogsAccess initialEnabled={chatLogsEnabled} canManage={manage} />
        </CardContent>
      </Card>

      {/* API keys shortcut */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <KeyRound className="size-4 text-muted-foreground" aria-hidden />
            API keys
          </CardTitle>
          <CardDescription>
            Programmatic access for CI, scripts, the SDK, and the CLI.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button asChild variant="outline">
            <Link href="/settings/api-keys">Manage API keys</Link>
          </Button>
        </CardContent>
      </Card>

      {/* Members shortcut */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Users className="size-4 text-muted-foreground" aria-hidden />
            Members
          </CardTitle>
          <CardDescription>
            Invite teammates, change roles, and remove people.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button asChild variant="outline">
            <Link href="/members">Manage members</Link>
          </Button>
        </CardContent>
      </Card>

      {/* Billing shortcut */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <CreditCard className="size-4 text-muted-foreground" aria-hidden />
            Billing &amp; plan
          </CardTitle>
          <CardDescription>
            View your plan and limits, upgrade, or manage your subscription.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button asChild variant="outline">
            <Link href="/billing">Go to billing</Link>
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}
