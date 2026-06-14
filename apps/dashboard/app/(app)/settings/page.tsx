import type { Metadata } from "next";
import Link from "next/link";
import { CreditCard, Globe2, ShieldAlert, Users } from "lucide-react";

import { ExternalSharingToggle } from "@/components/settings/external-sharing-toggle";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { canManage, loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Organization settings" };
export const dynamic = "force-dynamic";

/**
 * Organization settings (architecture §5.4). The headline control is the
 * org-wide external-sharing policy: owners/admins can allow members to share
 * sites publicly or with external (non-org) emails. Disabling it downgrades any
 * existing external/public sites — the toggle confirms how many were affected.
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
            <ExternalSharingToggle />
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
