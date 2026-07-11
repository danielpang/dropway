import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { BrandMark } from "@/components/brand-mark";
import { CreateOrgForm } from "@/components/onboarding/create-org-form";
import { ThemeToggle } from "@/components/theme-toggle";
import { auth } from "@/lib/auth";
import { safeNextPath } from "@/lib/authz-host";

export const metadata: Metadata = { title: "Create your organization" };

/**
 * Onboarding step: after Google (or email) sign-in, a user with no organization
 * creates one here. An organization is the tenant boundary for everything in the
 * Go API, so it must exist before the app shell renders. Lives OUTSIDE the (app)
 * route group to avoid the org-gate redirect loop.
 *
 * `next` (optional, same-site path only) is where the user continues once an
 * org exists: the /authz viewer exchange sends org-less viewers here and needs
 * them back on the share link afterwards. Defaults to the dashboard.
 */
export default async function OnboardingPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const requestHeaders = await headers();

  const sp = await searchParams;
  const rawNext = typeof sp.next === "string" ? sp.next : undefined;
  // safeNextPath rejects absolute/protocol-relative URLs (open-redirect guard)
  // and falls back to "/", which we widen to the dashboard default.
  const validated = safeNextPath(rawNext);
  const nextPath = validated === "/" ? "/dashboard" : validated;

  const session = await auth.api.getSession({ headers: requestHeaders });
  if (!session) redirect("/sign-in");

  // Already has a tenant? Skip onboarding entirely.
  const orgs = await auth.api
    .listOrganizations({ headers: requestHeaders })
    .catch(() => []);
  if (orgs && orgs.length > 0) redirect(nextPath);

  const { user } = session;
  const suggestedName = user.name ? `${user.name.split(" ")[0]}'s Team` : "";

  return (
    <div className="auth-backdrop flex min-h-dvh flex-col">
      <header className="flex h-14 items-center justify-between px-6">
        <span className="flex items-center gap-2 text-sm font-semibold tracking-tight">
          <BrandMark />
          Dropway
        </span>
        <ThemeToggle />
      </header>

      <main className="flex flex-1 items-center justify-center p-4">
        <div className="w-full max-w-md animate-fade-in">
          <CreateOrgForm suggestedName={suggestedName} next={nextPath} />
        </div>
      </main>
    </div>
  );
}
