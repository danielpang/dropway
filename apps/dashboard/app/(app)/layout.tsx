import Link from "next/link";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { MainNav } from "@/components/main-nav";
import { SignOutButton } from "@/components/sign-out-button";
import { ThemeToggle } from "@/components/theme-toggle";
import { auth } from "@/lib/auth";

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
  const session = await auth.api.getSession({ headers: requestHeaders });
  if (!session) redirect("/sign-in");

  // A signed-in user with no org (e.g. a fresh Google sign-up) must create one
  // before reaching the app. /onboarding lives outside this group so it renders
  // without the org-gated shell.
  const orgs = await auth.api
    .listOrganizations({ headers: requestHeaders })
    .catch(() => []);
  if (!orgs || orgs.length === 0) redirect("/onboarding");

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="sticky top-0 z-10 border-b border-border bg-background/80 backdrop-blur">
        <div className="container flex h-14 items-center justify-between">
          <Link
            href="/dashboard"
            className="flex items-center gap-2 text-sm font-semibold tracking-tight rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          >
            <span
              aria-hidden
              className="grid size-6 place-items-center rounded-md bg-primary text-primary-foreground text-xs font-bold"
            >
              S
            </span>
            Shipped
          </Link>
          <div className="flex items-center gap-2">
            <MainNav />
            <span className="mx-1 h-5 w-px bg-border" aria-hidden />
            <ThemeToggle />
            <SignOutButton />
          </div>
        </div>
      </header>

      <main className="container flex-1 py-10">{children}</main>
    </div>
  );
}
