import Link from "next/link";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { AppContent } from "@/components/app-content";
import { AccountMenu } from "@/components/account-menu";
import { AnalyticsIdentify } from "@/components/analytics/analytics-identify";
import { SessionCacheRefresh } from "@/components/auth/session-cache-refresh";
import { BrandMark } from "@/components/brand-mark";
import { OverLimitBanner } from "@/components/billing/over-limit-banner";
import { DashboardFooter } from "@/components/dashboard-footer";
import { MainNav } from "@/components/main-nav";
import { api } from "@/lib/api";
import { object } from "@/lib/project-room/sharing";
import { collaborationCapabilities } from "@/lib/project-room/capabilities";
import { auth } from "@/lib/auth";
import { supportEmail } from "@/lib/env";
import { loadOrgBillingState } from "@/lib/billing-server";
import { canManage, loadActiveRole } from "@/lib/org";
import { getCurrentSession } from "@/lib/session";

/**
 * Authenticated app shell. Guards the whole (app) route group server-side:
 * unauthenticated requests are bounced to sign-in before any UI renders, and
 * users without an organization yet are sent through onboarding (the Go API
 * scopes every resource to an org, so a tenant must exist first).
 */
export default async function AppLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const requestHeaders = await headers();
  // Memoized per request, so the same session read is reused by loadActiveOrg /
  // role helpers further down the tree instead of re-verifying the cookie.
  const session = await getCurrentSession();
  if (!session) redirect("/sign-in");

  // Independent reads, resolved in ONE parallel step so the shell waits
  // on the slowest of them rather than their sum (previously listOrganizations
  // was awaited on its own first, a serial round trip on every app page):
  //  - orgs: a signed-in user with no org (e.g. a fresh Google sign-up) must
  //    create one before reaching the app. /onboarding lives outside this group
  //    so it renders without the org-gated shell.
  //  - billing: account state (over_limit / past_due) → drives the
  //    non-dismissible banner. UX mirror of the server-side quota enforcement;
  //    a billing-API hiccup degrades to "active" so the shell never wrongly locks.
  //  - role: gates admin-only menu items (Audit log); fails soft to "member".
  //  - shared projects: shows guest navigation only for live, enabled grants.
  //  - collaboration: shows Rooms only for the authenticated rollout audience.
  // For the rare org-less user the billing/role reads are wasted work before the
  // onboarding redirect, but both fail soft, and the common case saves a full
  // round trip.
  const [orgs, { orgStatus }, role, shared, collaboration] = await Promise.all([
    auth.api.listOrganizations({ headers: requestHeaders }).catch(() => []),
    loadOrgBillingState(),
    loadActiveRole(),
    api.sharedProjectRequest("").catch(() => null),
    collaborationCapabilities(),
  ]);
  const projects = object(shared).projects;
  const hasSharedProjects = Array.isArray(projects) && projects.length > 0;
  if (!orgs || orgs.length === 0) {
    if (hasSharedProjects) redirect("/shared-projects");
    redirect("/onboarding");
  }
  const isAdmin = canManage(role);

  // Attribute browser analytics to this user + their active org (client-side;
  // no-op when PostHog isn't configured).
  const sessionUser = session.user as
    | {
        id?: string;
        email?: string;
        name?: string | null;
        image?: string | null;
      }
    | undefined;
  const activeOrgId =
    (session.session as { activeOrganizationId?: string | null } | undefined)
      ?.activeOrganizationId ?? null;
  const activeOrgName =
    (orgs.find((o) => o.id === activeOrgId) ?? orgs[0])?.name ?? null;

  return (
    <div className="flex min-h-dvh flex-col">
      <SessionCacheRefresh />
      {sessionUser?.id ? (
        <AnalyticsIdentify
          userId={sessionUser.id}
          email={sessionUser.email}
          organization={activeOrgId}
        />
      ) : null}
      {/* z-40 keeps the sticky header (and its account menu) above page
          content that uses relative z-10 for nested clickable regions, while
          staying below portaled dialogs/sheets at z-50. */}
      <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur">
        <div className="container flex h-14 items-center justify-between">
          <Link
            href="/dashboard"
            className="flex items-center gap-2 text-sm font-semibold tracking-tight rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          >
            <BrandMark />
            Dropway
          </Link>
          <div className="flex items-center gap-3">
            {/* Inline primary sections on desktop; below md they move into
                the account menu. */}
            <div className="hidden md:block">
              <MainNav
                hasSharedProjects={hasSharedProjects}
                collaborationEnabled={collaboration.enabled}
              />
            </div>
            <AccountMenu
              user={{
                name: sessionUser?.name,
                email: sessionUser?.email,
                image: sessionUser?.image,
              }}
              orgName={activeOrgName}
              admin={isAdmin}
              hasSharedProjects={hasSharedProjects}
              collaborationEnabled={collaboration.enabled}
            />
          </div>
        </div>
      </header>

      <OverLimitBanner status={orgStatus} />

      <AppContent
        footer={<DashboardFooter contactEnabled={Boolean(supportEmail())} />}
      >
        {children}
      </AppContent>
    </div>
  );
}
