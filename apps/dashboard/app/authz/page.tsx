import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";
import { Lock, ShieldX } from "lucide-react";

import { PasswordGate } from "@/components/authz/password-gate";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError } from "@/lib/api";
import { auth } from "@/lib/auth";
import {
  callbackUrl,
  normalizeContentHost,
  safeNextPath,
} from "@/lib/authz-host";

export const metadata: Metadata = { title: "Verifying access" };

// Authz decisions are live; never cache this exchange.
export const dynamic = "force-dynamic";

/**
 * The viewer authz exchange. A gated content host's Worker
 * 302s an unauthenticated request here as
 * `/authz?host=<content_host>&next=<path>`. This server route:
 *
 *  1. Requires a Better Auth session, else bounces to sign-in and returns here.
 *  2. Validates `host` (must be a real content host) + `next` (a same-site path)
 *     to close the open-redirect / token-exfiltration hole.
 *  3. Attempts to MINT a host-scoped edge token for the viewer (org_only /
 *     allowlist). The Go API re-checks the LIVE tables, claims are only a hint.
 *      - success → 302 to `https://<host>/__authz/callback?token=&next=`, where
 *        the Worker sets the `__Host-edge` cookie and forwards to `next`.
 *      - 400 → the site is PASSWORD mode → render the platform password form
 *        (this origin, NOT tenant content, anti-phishing).
 *      - 403 → a clear "you don't have access" page.
 *      - 404 → an "unknown link" page (also covers a forged custom host).
 *
 * This route is PLATFORM-controlled and lives on app.dropway.dev, so the
 * password prompt and the minted token never touch hostile tenant JS.
 */
export default async function AuthzPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  const rawHost = typeof sp.host === "string" ? sp.host : undefined;
  const rawNext = typeof sp.next === "string" ? sp.next : undefined;

  const host = normalizeContentHost(rawHost);
  const next = safeNextPath(rawNext);

  // A malformed/absent host can't be a real share link.
  if (!host) {
    return (
      <Outcome
        icon={ShieldX}
        title="This link is invalid"
        description="The address is missing or malformed. Ask whoever shared it for a fresh link."
      />
    );
  }

  // 1) Require a session; bounce to sign-in and come straight back here.
  const requestHeaders = await headers();
  const session = await auth.api
    .getSession({ headers: requestHeaders })
    .catch(() => null);

  if (!session) {
    const returnTo = `/authz?host=${encodeURIComponent(host)}&next=${encodeURIComponent(next)}`;
    redirect(`/sign-in?callbackURL=${encodeURIComponent(returnTo)}`);
  }

  // 2) Try to mint for org_only / allowlist. The Go API is the source of truth.
  try {
    const { token } = await api.authzMint({ host, next });
    if (token) {
      // Authorized → hand the browser to the content host's callback.
      redirect(callbackUrl(host, token, next));
    }
  } catch (err) {
    // `redirect()` throws a control-flow signal, re-throw so Next handles it.
    if (!(err instanceof ApiError)) throw err;

    if (err.status === 400) {
      // Password-mode site → render the platform-controlled password form.
      return (
        <div className="space-y-6">
          <Header host={host} />
          <PasswordGate host={host} next={next} />
        </div>
      );
    }

    if (err.status === 403) {
      return (
        <Outcome
          icon={ShieldX}
          title="You don't have access"
          description="Your account isn't on this site's access list, or the share link has expired. If you think this is a mistake, ask the owner to add you."
        />
      );
    }

    if (err.status === 404) {
      return (
        <Outcome
          icon={ShieldX}
          title="We couldn't find that site"
          description="This share link doesn't point to a known site. Double-check the address with whoever sent it."
        />
      );
    }

    // 401 here means the viewer's credential can't be scoped to a tenant. Two
    // distinct causes with distinct fixes:
    //  - the user has NO organization (skipped onboarding): re-auth mints the
    //    same org-less token forever → a sign-in loop. Send them through
    //    onboarding instead, returning here once the org exists.
    //  - the user HAS an org but the token predates it / went stale: a fresh
    //    sign-in mints a token with the org claim, so bounce to sign-in.
    if (err.status === 401) {
      const returnTo = `/authz?host=${encodeURIComponent(host)}&next=${encodeURIComponent(next)}`;
      const orgs = await auth.api
        .listOrganizations({ headers: requestHeaders })
        .catch(() => null); // lookup failure → fall through to sign-in, not onboarding
      if (orgs && orgs.length === 0) {
        redirect(`/onboarding?next=${encodeURIComponent(returnTo)}`);
      }
      redirect(`/sign-in?callbackURL=${encodeURIComponent(returnTo)}`);
    }

    throw err;
  }

  // Mint returned no token but didn't error, treat as a transient failure.
  return (
    <Outcome
      icon={ShieldX}
      title="Something went wrong"
      description="We couldn't verify your access just now. Refresh to try again."
    />
  );
}

function Header({ host }: { host: string }) {
  return (
    <div className="space-y-2 text-center">
      <span className="mx-auto grid size-11 place-items-center rounded-xl bg-secondary text-secondary-foreground">
        <Lock className="size-5" aria-hidden />
      </span>
      <h1 className="text-xl font-semibold tracking-tight">
        This site is password-protected
      </h1>
      <p className="text-sm text-muted-foreground">
        Enter the password to view{" "}
        <span className="font-mono text-foreground">{host}</span>.
      </p>
    </div>
  );
}

/** A terminal status card (invalid / forbidden / not-found). */
function Outcome({
  icon: Icon,
  title,
  description,
}: {
  icon: typeof ShieldX;
  title: string;
  description: string;
}) {
  return (
    <Card className="shadow-md">
      <CardHeader className="space-y-1.5 text-center">
        <span className="mx-auto grid size-11 place-items-center rounded-xl bg-secondary text-secondary-foreground">
          <Icon className="size-5" aria-hidden />
        </span>
        <CardTitle className="text-lg">{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="text-center text-sm text-muted-foreground">
        You can safely close this tab.
      </CardContent>
    </Card>
  );
}
