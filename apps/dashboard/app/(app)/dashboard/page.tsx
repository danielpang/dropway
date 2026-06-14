import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { auth } from "@/lib/auth";

export const metadata: Metadata = { title: "Dashboard" };

/**
 * Minimal authenticated landing (server component). Shows the signed-in user
 * and their active organization. Business data (sites, deploys) comes from the
 * Go API via lib/api.ts — wired once the API ships; this page proves the auth +
 * org plumbing end-to-end (Phase 1: login → org → site → deploy).
 */
export default async function DashboardPage() {
  const requestHeaders = await headers();

  const session = await auth.api.getSession({ headers: requestHeaders });
  if (!session) redirect("/sign-in");

  const { user } = session;

  // The active organization for the session (from the Better Auth org plugin).
  // Solo users get a default single-member org, so this is generally present.
  const org = await auth.api
    .getFullOrganization({ headers: requestHeaders })
    .catch(() => null);

  return (
    <div className="mx-auto max-w-3xl space-y-8">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">
          Welcome{user.name ? `, ${user.name.split(" ")[0]}` : ""}
        </h1>
        <p className="text-muted-foreground">
          Your Shipped control plane. Deploy a folder, get a live URL.
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Account</CardTitle>
            <CardDescription>The signed-in user.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <Row label="Name" value={user.name || "—"} />
            <Row label="Email" value={user.email} />
            <Row
              label="Email verified"
              value={user.emailVerified ? "Yes" : "No"}
            />
            <Row label="User ID" value={user.id} mono />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Organization</CardTitle>
            <CardDescription>Your active tenant.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            {org ? (
              <>
                <Row label="Name" value={org.name} />
                <Row label="Slug" value={org.slug ?? "—"} mono />
                <Row label="Org ID" value={org.id} mono />
                <Row
                  label="Members"
                  value={String(org.members?.length ?? 0)}
                />
              </>
            ) : (
              <p className="text-muted-foreground">
                No active organization yet.
              </p>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Sites</CardTitle>
          <CardDescription>
            Deployed sites will appear here once the control-plane API is wired
            up (lib/api.ts → api.shipped.app).
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="rounded-md border border-dashed border-border px-4 py-10 text-center text-sm text-muted-foreground">
            No sites yet. Use{" "}
            <code className="font-mono text-foreground">shipped deploy ./dist</code>{" "}
            or drag a folder here to create your first one.
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function Row({
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
