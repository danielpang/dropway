import Link from "next/link";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { AnalyticsIdentify } from "@/components/analytics/analytics-identify";
import { SessionCacheRefresh } from "@/components/auth/session-cache-refresh";
import { BrandMark } from "@/components/brand-mark";
import { OverLimitBanner } from "@/components/billing/over-limit-banner";
import { DashboardFooter } from "@/components/dashboard-footer";
import { MainNav } from "@/components/main-nav";
import { MobileNav } from "@/components/mobile-nav";
import { SignOutButton } from "@/components/sign-out-button";
import { ThemeToggle } from "@/components/theme-toggle";
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

  // Three independent reads, resolved in ONE parallel step so the shell waits
  // on the slowest of them rather than their sum (previously listOrganizations
  // was awaited on its own first, a serial round trip on every app page):
  //  - orgs: a signed-in user with no org (e.g. a fresh Google sign-up) must
  //    create one before reaching the app. /onboarding lives outside this group
  //    so it renders without the org-gated shell.
  //  - billing: account state (over_limit / past_due) → drives the
  //    non-dismissible banner. UX mirror of the server-side quota enforcement;
  //    a billing-API hiccup degrades to "active" so the shell never wrongly locks.
  //  - role: gates admin-only nav (the Audit link); fails soft to "member".
  // For the rare org-less user the billing/role reads are wasted work before the
  // onboarding redirect, but both fail soft, and the common case saves a full
  // round trip.
  const [orgs, { orgStatus }, role] = await Promise.all([
    auth.api.listOrganizations({ headers: requestHeaders }).catch(() => []),
    loadOrgBillingState(),
    loadActiveRole(),
  ]);
  if (!orgs || orgs.length === 0) redirect("/onboarding");
  const isAdmin = canManage(role);

  // Attribute browser analytics to this user + their active org (client-side;
  // no-op when PostHog isn't configured).
  const sessionUser = session.user as { id?: string; email?: string } | undefined;
  const activeOrgId =
    (session.session as { activeOrganizationId?: string | null } | undefined)
      ?.activeOrganizationId ?? null;

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
      <header className="sticky top-0 z-10 border-b border-border bg-background/80 backdrop-blur">
        <div className="container flex h-14 items-center justify-between">
          <Link
            href="/dashboard"
            className="flex items-center gap-2 text-sm font-semibold tracking-tight rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          >
            <BrandMark />
            Dropway
          </Link>
          {/* Desktop: full nav + actions inline. */}
          <div className="hidden items-center gap-2 md:flex">
            <MainNav admin={isAdmin} />
            <span className="mx-1 h-5 w-px bg-border" aria-hidden />
            <ThemeToggle />
            <SignOutButton />
          </div>
          {/* Mobile: theme toggle + the nav/actions collapsed into a menu. */}
          <div className="flex items-center gap-1 md:hidden">
            <ThemeToggle />
            <MobileNav admin={isAdmin} />
          </div>
        </div>
      </header>

      <OverLimitBanner status={orgStatus} />

      <main className="container flex-1 py-6 sm:py-10">{children}</main>

      <DashboardFooter contactEnabled={Boolean(supportEmail())} />
    </div>
  );
}
