import type { Metadata } from "next";
import Link from "next/link";
import { Bot, CreditCard, Globe2, KeyRound, ShieldAlert, ShieldCheck, Sparkles, Users } from "lucide-react";

import { AiBuilderAccess } from "@/components/settings/ai-builder-access";
import { ExternalSharingToggle } from "@/components/settings/external-sharing-toggle";
import { McpAccess } from "@/components/settings/mcp-access";
import { RequireMfaToggle } from "@/components/settings/require-mfa-toggle";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError } from "@/lib/api";
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
  // Three independent API reads, resolved in one parallel step. The AI builder
  // card is shown ONLY on a paid plan (the builder requires one, so a free org
  // has nothing to toggle). getBilling 404s on OSS/self-host, which is
  // unlimited → treat as paid so a self-hoster still sees the toggle. We read
  // the live ai_enabled state; a 503 (builder not configured) hides the card.
  const [policy, billing, aiSettings] = await Promise.all([
    api.getOrgPolicy().catch(() => ({
      allow_external_sharing: false,
      mcp_enabled: true,
      require_mfa: false,
    })),
    api
      .getBilling()
      .then((b) => ({ tier: b.plan_tier ?? "free", selfHost: false }))
      // A 404 is the OSS/self-host signature (no billing service → unlimited);
      // any OTHER failure is a transient blip and must NOT be mistaken for
      // self-host, or a free-tier cloud org would briefly see gated features
      // unlocked.
      .catch((err) => ({
        tier: null,
        selfHost: err instanceof ApiError && err.status === 404,
      })),
    api.getAIOrgSettings().catch(() => null),
  ]);
  const allowExternalSharing = policy.allow_external_sharing;
  const mcpEnabled = policy.mcp_enabled;
  const requireMfa = policy.require_mfa;
  const planTier = billing.tier ?? "pro"; // billing unavailable: show the AI toggle
  const showAiBuilder = planTier !== "free" && aiSettings !== null;

  // MFA enforcement is business/enterprise; self-host (no billing) is unlimited
  // so the toggle shows there too. A transient billing error fails CLOSED to the
  // upgrade CTA (the switch would only 402 anyway). This gate is UI convenience —
  // the Go API re-checks the tier on the write.
  const mfaEnforceEligible =
    billing.selfHost || billing.tier === "business" || billing.tier === "enterprise";

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
            Controls whether sites in this organization can be shared publicly or
            with people outside your verified domains. New organizations start
            fully internal.
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
            Let AI tools reach this organization&rsquo;s sites through the Dropway
            MCP server. Public sites are also readable by crawlers via{" "}
            <code className="font-mono text-xs">/llms.txt</code>; gated sites stay
            private and are reachable only through an authorized MCP connection.
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
              billed on your plan at the model provider&rsquo;s cost. On by
              default; admins can turn it off for the whole organization.
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

      {/* Org MFA enforcement (business/enterprise) */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <ShieldCheck className="size-4 text-muted-foreground" aria-hidden />
            Two-factor authentication
          </CardTitle>
          <CardDescription>
            Anyone can turn on two-factor for their own account under Account
            security. On Business and Enterprise plans, owners and admins can
            make it mandatory for every member of this organization.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <RequireMfaToggle
            initialEnabled={requireMfa}
            canManage={manage}
            eligible={mfaEnforceEligible}
          />
        </CardContent>
      </Card>

      {/* Account security shortcut (per-user, not org policy) */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <KeyRound className="size-4 text-muted-foreground" aria-hidden />
            Your account security
          </CardTitle>
          <CardDescription>
            Two-factor authentication and backup codes for your own account.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button asChild variant="outline">
            <Link href="/account/security">Manage account security</Link>
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
