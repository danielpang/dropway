import type { Metadata } from "next";
import { Mail, ShieldAlert, Users } from "lucide-react";

import { InviteMemberForm } from "@/components/members/invite-member-form";
import { MemberList } from "@/components/members/member-list";
import { PendingInvitations } from "@/components/members/pending-invitations";
import { RevokeAccessControl } from "@/components/members/revoke-access-control";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { canManage, loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Members" };

// Membership mutates often and is per-tenant; always render live.
export const dynamic = "force-dynamic";

/**
 * Team members management (architecture §5.4). Lists the active org's members
 * and pending invitations from Better Auth (which owns the membership tables),
 * with invite / role-change / remove controls gated to owner & admin. The Go API
 * independently re-checks role on every privileged write, so a non-admin who
 * forged a request is still rejected server-side — this UI gate is convenience,
 * not the security boundary.
 */
export default async function MembersPage() {
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
    <div className="mx-auto max-w-3xl space-y-8">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Members</h1>
        <p className="text-muted-foreground">
          People in{" "}
          <span className="font-medium text-foreground">
            {org.name ?? "your organization"}
          </span>
          . {manage ? "Invite teammates and manage their roles." : "Ask an admin to change roles or invite people."}
        </p>
      </div>

      {manage && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Mail className="size-4 text-muted-foreground" aria-hidden />
              Invite a teammate
            </CardTitle>
            <CardDescription>
              They&rsquo;ll get an email to join {org.name ?? "the organization"}.
              Members can create and deploy sites; admins can also manage access
              and people.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <InviteMemberForm organizationId={org.organizationId} />
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Users className="size-4 text-muted-foreground" aria-hidden />
            Team ({org.members.length})
          </CardTitle>
          <CardDescription>
            {manage
              ? "Change a member's role or remove them. You can't change your own role."
              : "Everyone with access to this organization."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <MemberList
            members={org.members}
            organizationId={org.organizationId}
            myUserId={org.myUserId}
            canManage={manage}
          />
        </CardContent>
      </Card>

      {manage && org.invitations.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">
              Pending invitations ({org.invitations.length})
            </CardTitle>
            <CardDescription>
              People who&rsquo;ve been invited but haven&rsquo;t joined yet.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <PendingInvitations invitations={org.invitations} />
          </CardContent>
        </Card>
      )}

      {/* Danger zone: org-wide hard revocation (architecture §6/§10). Admin-only;
          the Go API re-checks role and writes the KV denylist min_iat. */}
      {manage && (
        <Card className="border-destructive/30">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldAlert className="size-4 text-destructive" aria-hidden />
              Security
            </CardTitle>
            <CardDescription>
              Revoke access in an emergency — for example after a removed member,
              a leaked link, or a suspected breach.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <RevokeAccessControl organizationId={org.organizationId} />
          </CardContent>
        </Card>
      )}
    </div>
  );
}
