import { randomUUID } from "node:crypto";

import { betterAuth } from "better-auth";
import { createAuthMiddleware, isAPIError } from "better-auth/api";
import { jwt, magicLink, organization } from "better-auth/plugins";
import { oauthProvider } from "@better-auth/oauth-provider";
import { Pool } from "pg";

import { logIfConnectionCapacity } from "@/lib/db-capacity";
import { oauthRateLimitRules } from "@/lib/oauth-ratelimit";
import {
  betterAuthSecret,
  betterAuthUrl,
  databaseUrl,
  googleClientId,
  googleClientSecret,
  jwtAudience,
  jwtIssuer,
  mcpResourceUrl,
  MCP_URL,
  requireEmailVerification,
} from "@/lib/env";
import {
  invitationEmail,
  magicLinkEmail,
  passwordResetEmail,
  verifyEmail,
} from "@/lib/email-templates";

// NOTE: `@/lib/email` is imported LAZILY inside the send callbacks below, never at
// module top level. The `@better-auth/cli migrate` step loads THIS config under a
// plain-Node (jiti) loader, not the Next.js bundler, and lib/email.ts starts with
// `import "server-only"`, which only resolves under Next. A top-level import here
// would make `Cannot find module 'server-only'` break the one-time auth migration
// (CI + every fresh self-host). Deferring it keeps the config import graph free of
// server-only/nodemailer; the dynamic import runs only at send time, in the real
// Next runtime. (`@/lib/email-templates` IS safe to import at the top: it's pure
// string building with no `server-only`/Node deps, so it loads fine under jiti.)

/**
 * Better Auth server instance, the authoritative owner of the `identity` schema
 * (user/session/account/verification/jwks/organization/member/invitation).
 * Named `identity` (not `auth`) to avoid colliding with Postgres providers that
 * reserve their own `auth` schema (e.g. Supabase's GoTrue).
 *
 * Architecture notes:
 *  - Better Auth runs self-hosted inside the Next.js dashboard and OWNS + migrates
 *    its own identity tables in our Supabase Postgres. The Go API reads them for
 *    authz but never migrates them.
 *  - Sessions are cookie-based for the dashboard (__Host- prefix, host-only).
 *  - The jwt() plugin issues short-lived EdDSA JWTs and exposes a JWKS endpoint;
 *    the Go API is the JWT verifier / authz boundary (pins EdDSA, rejects
 *    none/HS256). The public serve path carries NO JWT.
 *  - Google sign-in is a core, first-class method alongside email/password and
 *    magic link. Email verification is required.
 */
// A single shared pg Pool for Better Auth's `identity` schema. Reused by the
// session hook below to look up a user's organization.
//
// search_path: we do NOT pin it here via the `options` startup parameter, because
// Supabase's TRANSACTION-mode pooler (the runtime target: ...:6543, IPv4, which is
// what Vercel can reach) rejects startup `options` with
// `08P01 unsupported startup parameter in options: search_path`. Instead the
// search_path is set once at the DB ROLE level so every connection inherits it
// without a startup param (transaction-pooler safe):
//   ALTER ROLE postgres IN DATABASE postgres SET search_path = "$user", public, extensions, identity;
// That lets Better Auth's UNqualified tables (user, session, member, organization,
// …) resolve to `identity`, exactly where the Go API reads them (identity.member).
//
// Pool sizing is tuned for SERVERLESS: each warm Vercel function instance holds its
// own copy of this module-level pool, so the cap must be per-instance, not global.
// `pg` defaults to max:10 with no idle timeout, so a few warm instances would open
// many backends; keep `max` small and let idle connections drain back between bursts.
const authPool = new Pool({
  connectionString: databaseUrl(),
  max: 3,
  idleTimeoutMillis: 10_000,
  connectionTimeoutMillis: 10_000,
});

// Detect a Postgres connection-capacity failure: log it under [db-capacity] AND raise a
// PostHog Error Tracking issue so it's alertable, not just greppable. The PostHog send is
// lazily imported (like @/lib/email and @/lib/analytics-server elsewhere here) so the
// server-only/posthog-node graph stays out of the jiti migrate-time import; it's fire-
// and-forget and self-swallows, so telemetry can never block or break the auth path.
// Returns the matched reason (or null), so callers can branch on whether it was capacity.
function reportConnectionIssue(where: string, err: unknown): string | null {
  const reason = logIfConnectionCapacity(where, err);
  if (!reason) return null;
  void (async () => {
    try {
      const { captureDbCapacityIssue } = await import("@/lib/analytics-server");
      await captureDbCapacityIssue({ reason, source: where, error: err });
    } catch {
      // Telemetry must never break auth; the [db-capacity] log already fired above.
    }
  })();
  return reason;
}

// An idle pooled client erroring out (backend dropped it, pooler reset it) emits here
// rather than rejecting a query, so without this handler `pg` would log it unattended
// or, worse, crash the process on an 'error' with no listener. Tag connection-capacity
// errors so they're alertable; surface anything else so it isn't lost.
authPool.on("error", (err) => {
  if (reportConnectionIssue("authPool idle client", err)) return;
  // eslint-disable-next-line no-console
  console.error(`[db-pool] idle client error: ${err instanceof Error ? err.message : String(err)}`);
});

// Per-instance memo of firstOrgId. The lookup runs on every new session AND every MCP
// access-token mint (customAccessTokenClaims), which during one sign-in fires several
// times within seconds, so a short TTL collapses those repeats into one query without
// adding meaningful staleness. The cache is per serverless instance (a plain Map),
// best-effort, and bounded by a hard size cap so a long-lived instance can't grow it
// without limit.
//
// Only POSITIVE results are cached. A miss (a brand-new user mid-onboarding with no
// membership yet) is never cached, so the org appears the instant onboarding creates
// it. A found org changes only when the user's OLDEST membership changes (rare); even
// then the Go API re-checks membership live per request, so a briefly stale org_id is
// rejected there rather than granting access, bounding the risk to the TTL.
const ORG_CACHE_TTL_MS = 60_000;
const ORG_CACHE_MAX = 10_000;
const orgCache = new Map<string, { orgId: string; expiresAt: number }>();

/**
 * The user's first organization id, the default "active org" for a new session AND
 * the org stamped into MCP OAuth access tokens (customAccessTokenClaims below), so the
 * MCP resource server can scope RLS straight from the token. Best-effort: returns
 * undefined on a lookup error or a user with no membership.
 */
async function firstOrgId(userId: string): Promise<string | undefined> {
  const hit = orgCache.get(userId);
  if (hit) {
    if (hit.expiresAt > Date.now()) return hit.orgId;
    orgCache.delete(userId);
  }
  try {
    const res = await authPool.query<{ organizationId: string }>(
      `SELECT "organizationId" FROM identity.member WHERE "userId" = $1 ORDER BY "createdAt" LIMIT 1`,
      [userId],
    );
    const orgId = res.rows[0]?.organizationId;
    if (orgId) {
      // Drop the whole cache rather than tracking LRU: entries are tiny and expire on
      // their own, this only guards against unbounded growth in a rare long-lived host.
      if (orgCache.size >= ORG_CACHE_MAX) orgCache.clear();
      orgCache.set(userId, { orgId, expiresAt: Date.now() + ORG_CACHE_TTL_MS });
    }
    return orgId;
  } catch (err) {
    // Stays best-effort (returns undefined so the caller falls back), but a
    // connection-capacity failure here is logged under [db-capacity] and raised to
    // PostHog rather than silently swallowed, so the org-backfill misfiring is visible.
    reportConnectionIssue("firstOrgId", err);
    return undefined;
  }
}

export const auth = betterAuth({
  appName: "Dropway",
  baseURL: betterAuthUrl(),
  secret: betterAuthSecret(),

  // Intercept Better Auth's own logs so a connection-capacity failure inside ITS
  // queries (e.g. findSession hitting the pooler cap, the original EMAXCONNSESSION
  // 500) is re-emitted under the alertable [db-capacity] tag instead of being buried
  // in a generic INTERNAL_SERVER_ERROR. Providing `log` replaces Better Auth's default
  // console output, so forward every record to the console to preserve its diagnostics.
  logger: {
    log: (level, message, ...args) => {
      if (level === "error") {
        for (const candidate of [message, ...args]) {
          if (reportConnectionIssue("better-auth", candidate)) break;
        }
      }
      // eslint-disable-next-line no-console
      console[level](`[Better Auth] ${message}`, ...args);
    },
  },

  // Postgres via a node-postgres Pool. Better Auth uses its built-in Kysely
  // adapter when handed a `Pool`, generating + migrating its own identity tables.
  //
  // Better Auth OWNS the `identity` schema: `databaseUrl()` is a PRIVILEGED
  // connection (it must CREATE its tables). The role-level search_path (see the
  // authPool comment above) resolves the adapter's UNqualified tables (user,
  // session, member, organization, …) to `identity`, exactly where the Go API
  // reads them (identity.member) for authz.
  database: authPool,

  // Set the user's first organization as the session's ACTIVE org on every new
  // session. Better Auth only sets activeOrganizationId on org create/switch (the
  // onboarding flow), so a RETURNING user who just signs in would otherwise have no
  // active org, and the jwt() plugin's definePayload would then mint org_id="". The
  // Go API rejects that (RLS needs the tenant), 500-ing with "claims missing org_id".
  // This backfills the active org so the minted JWT always carries it. Failures are
  // non-fatal (a user with no membership simply gets none; onboarding sets it).
  databaseHooks: {
    // Record a `user_signed_up` analytics event for every new account. The org
    // usually doesn't exist yet (it's created in onboarding), so `organization`
    // is best-effort here. Lazily imported (like @/lib/email) so the auth-config
    // import graph stays free of `server-only`/posthog-node under the CLI migrate
    // loader; never blocks signup.
    user: {
      create: {
        after: async (user) => {
          try {
            const u = user as { id: string; email?: string };
            const orgId = await firstOrgId(u.id);
            const { captureSignup } = await import("@/lib/analytics-server");
            await captureSignup({
              userId: u.id,
              email: u.email ?? null,
              organization: orgId ?? null,
            });
          } catch {
            // Analytics must never block account creation.
          }
        },
      },
    },
    session: {
      create: {
        before: async (session) => {
          const s = session as { userId: string; activeOrganizationId?: string | null };
          if (s.activeOrganizationId) return { data: session };
          // Transient lookup error or no membership: leave the active org unset
          // (onboarding's organization.create sets it explicitly).
          const orgId = await firstOrgId(s.userId);
          if (orgId) return { data: { ...session, activeOrganizationId: orgId } };
          return { data: session };
        },
        // A session was created => a successful authentication. This is the one
        // reliable signal that covers EVERY sign-in method (email/password,
        // Google, magic link); it also fires for the session Better Auth opens
        // right after signup, so `sign_in_succeeded` counts successful auths
        // (logins + signups), the denominator for the sign_in_failed rate. The
        // matching FAILURE event is emitted from `hooks.after` below. Org is read
        // off the (possibly just-backfilled) session. Lazily imported +
        // self-swallowing like the signup hook; never blocks authentication.
        after: async (session) => {
          try {
            const s = session as { userId: string; activeOrganizationId?: string | null };
            const { captureSignInSucceeded } = await import("@/lib/analytics-server");
            await captureSignInSucceeded({
              userId: s.userId,
              organization: s.activeOrganizationId ?? null,
            });
          } catch {
            // Analytics must never block authentication.
          }
        },
      },
    },
  },

  // Record a `sign_in_failed` / `sign_up_failed` event for every auth attempt that
  // errors out. `hooks.after` runs EVEN WHEN THE ENDPOINT THROWS: Better Auth's
  // dispatch catches the APIError into `ctx.context.returned` before after-hooks
  // run, so a rejected login (401 bad credentials, 400 validation, 403 unverified
  // email, 500 server error) or a rejected signup (422 email already in use, 400
  // weak password, 500) is observable here with its HTTP status. Scoped to the
  // interactive /sign-in/* and /sign-up/* endpoints and emits ONLY on an APIError:
  // it never double-counts the successes already captured at session creation
  // (sign_in_succeeded) or user creation (user_signed_up). Pure observation: it
  // returns nothing, so the response is untouched. Analytics is lazily imported +
  // self-swallowing so telemetry can't block or break auth.
  hooks: {
    after: createAuthMiddleware(async (ctx) => {
      const path = ctx.path;
      const isSignIn = path?.startsWith("/sign-in/");
      const isSignUp = path?.startsWith("/sign-up/");
      if (!path || (!isSignIn && !isSignUp)) return;
      const returned: unknown = (ctx.context as { returned?: unknown }).returned;
      if (!isAPIError(returned)) return;
      try {
        // method: the leaf endpoint (email | social→google | magic-link), labelled
        // for readability and falling back to the raw leaf for any future provider.
        const leaf = path.slice(path.indexOf("/", 1) + 1);
        const method =
          leaf === "social" ? "google" : leaf === "magic-link" ? "magic_link" : leaf;
        const body = ctx.body as { email?: unknown } | undefined;
        const email = typeof body?.email === "string" ? body.email : null;
        const code = (returned.body as { code?: unknown } | undefined)?.code;
        const failure = {
          status: returned.statusCode,
          code: typeof code === "string" ? code : undefined,
          method,
          email,
        };
        const analytics = await import("@/lib/analytics-server");
        await (isSignUp
          ? analytics.captureSignUpFailed(failure)
          : analytics.captureSignInFailed(failure));
      } catch {
        // Telemetry must never break the auth path.
      }
    }),
  },

  emailAndPassword: {
    enabled: true,
    // Required only when an email provider is wired (REQUIRE_EMAIL_VERIFICATION=true);
    // off by default so a no-email self-host can sign up. Google is pre-verified.
    requireEmailVerification: requireEmailVerification(),
    minPasswordLength: 8,
    // Password-reset link. sendEmail no-ops+logs when MAIL_SMTP_URL is unset (lib/email.ts).
    sendResetPassword: async ({ user, url }) => {
      const { sendEmail } = await import("@/lib/email");
      const { subject, html, text } = passwordResetEmail({
        url,
        appUrl: betterAuthUrl(),
      });
      await sendEmail({ to: user.email, subject, html, text });
    },
  },

  // Email-verification link. Only actually GATES sign-in when
  // REQUIRE_EMAIL_VERIFICATION=true; the callback is registered regardless so the
  // link is sent (or logged) whenever Better Auth asks to verify an address.
  emailVerification: {
    sendOnSignUp: true,
    sendVerificationEmail: async ({ user, url }) => {
      const { sendEmail } = await import("@/lib/email");
      const { subject, html, text } = verifyEmail({ url, appUrl: betterAuthUrl() });
      await sendEmail({ to: user.email, subject, html, text });
    },
  },

  socialProviders: {
    google: {
      clientId: googleClientId(),
      clientSecret: googleClientSecret(),
    },
  },

  // Short-lived signed session cookie so session verification doesn't hit
  // Postgres on every call. Without this, EVERY `auth.api.*` call (getSession,
  // listOrganizations, getActiveMember, getFullOrganization, getToken, …)
  // independently resolves the cookie against identity.session — one dashboard
  // render does ~5 such lookups through the Supabase pooler, and that DB fan-out
  // was the dominant server-side page-load cost. With the cache, a still-fresh
  // signed cookie satisfies those lookups with zero DB reads; the DB remains the
  // fallback (and re-signs the cookie) whenever the cache is absent or stale.
  //
  // Revocation tradeoff: a revoked/expired session stays usable for up to maxAge
  // on cookie-only reads. 5 minutes sits INSIDE the posture the app already
  // accepts — minted API JWTs are valid (and unrevocable) for 10 minutes, and
  // lib/token-cache.ts reuses them across requests — so this adds no new exposure
  // class, only extends it to the dashboard's own session reads.
  //
  // NOTE: server components can't set cookies, so an RSC render can READ the
  // cache but never refresh it; only requests through the /api/auth handler
  // (sign-in, client fetches) can re-sign it. SessionCacheRefresh in the (app)
  // layout pings get-session from the browser to keep the cookie warm — without
  // that, the cache would expire maxAge after sign-in and every later render
  // would silently fall back to per-call DB reads.
  session: {
    cookieCache: {
      enabled: true,
      maxAge: 5 * 60,
    },
  },

  // Cookie hardening for the dashboard origin (app.dropway.dev). The session
  // cookie is host-only (no Domain=) so it never reaches the content domain.
  advanced: {
    cookiePrefix: "dropway",
    // Secure cookies (and the `__Secure-` name prefix Better Auth adds with them)
    // require an HTTPS origin, browsers REJECT them over http://. Drive this off
    // the deployment's actual scheme, not NODE_ENV: a self-host served over plain
    // http (e.g. http://<lan-ip>:3000) with NODE_ENV=production would otherwise set
    // a `__Secure-` cookie the browser drops, silently breaking login. https origin
    // → secure cookies; http (localhost / internal http self-host) → plain cookies.
    useSecureCookies: betterAuthUrl().startsWith("https://"),
    defaultCookieAttributes: {
      sameSite: "lax",
      httpOnly: true,
    },
    // The Go API's `app` schema keys orgs/users by `uuid` (org_meta.id, sites.
    // owner_user_id, …). Better Auth's default IDs are nanoid-style strings, which a
    // uuid column rejects (22P02), so generate real UUIDs for every Better Auth row.
    database: {
      generateId: () => randomUUID(),
    },
    // Trusted client-IP source for rate limiting (M4). Better Auth defaults to the
    // LEFT-most x-forwarded-for entry, which the client controls (Vercel/Fly append
    // the real IP to the RIGHT), so the per-IP OAuth/DCR limits below would be
    // trivially bypassable by spoofing X-Forwarded-For. Pin platform-trusted headers
    // first: x-vercel-forwarded-for (Vercel, the dashboard's host, un-spoofable),
    // then fly-client-ip and x-real-ip for the Dockerfile self-host path, with
    // x-forwarded-for only as a last resort. Better Auth uses the first header that
    // is present, so on Vercel the spoofable x-forwarded-for is never consulted.
    ipAddress: {
      ipAddressHeaders: [
        "x-vercel-forwarded-for",
        "fly-client-ip",
        "x-real-ip",
        "x-forwarded-for",
      ],
    },
  },

  // Rate limiting for the unauthenticated OAuth surface (M4). The oauthProvider
  // exposes a PUBLIC, unauthenticated Dynamic Client Registration endpoint
  // (allowUnauthenticatedClientRegistration below) plus the authorize/token/
  // consent endpoints, so without throttling an anonymous caller could flood
  // /oauth2/register to exhaust oauth_application rows (and the tight Supabase
  // pooler) and degrade login platform-wide.
  //
  // Keyed per client IP + path; the IP comes from the trusted-header list set in
  // advanced.ipAddress above (NOT the spoofable left-most x-forwarded-for, which
  // would let an attacker rotate the key per request). Enabled in production only
  // so local dev + the OAuth e2e scripts aren't throttled; this mirrors Better
  // Auth's own default of enabling rate limiting in production.
  //
  // NOTE (first layer): the default storage is in-memory, i.e. per instance. On a
  // multi-instance serverless deployment that is a partial control. The durable
  // follow-up is shared-storage rate limiting (Better Auth `storage: "database"`,
  // which needs the `rateLimit` table migrated, or a secondary store) and/or an
  // edge WAF rule on /api/auth/oauth2/register. The rules live in
  // lib/oauth-ratelimit.ts so they can be unit-tested without this module.
  rateLimit: {
    enabled: process.env.NODE_ENV === "production",
    storage: "memory",
    customRules: oauthRateLimitRules,
  },

  plugins: [
    // Orgs/members/roles/invitations out of the box. Creator = owner; solo
    // users get a default single-member org. Roles: owner | admin | member.
    organization({
      // Authorization detail (admin-only policy/role changes) is enforced in the
      // Go API + DB CHECK/trigger; this plugin provides the membership tables.
      allowUserToCreateOrganization: true,
      // The AUTHORITATIVE members_per_org cap is the Go API preflight the invite
      // path calls (open-core: OSS unlimited, cloud per-tier, H8). This only LIFTS
      // Better Auth's restrictive default membershipLimit (100), which would
      // otherwise break an Enterprise org (cap 1000) and cap an unlimited self-host.
      // Keep it well above any tier cap so it never spuriously blocks.
      membershipLimit: 100_000,
      // Invitation email. Better Auth writes the identity.invitation row and then
      // calls THIS to deliver the accept link. WITHOUT this callback the invite is
      // created and the form reports success, but no mail is ever attempted (no
      // Resend activity, no error log, nothing) — that was the bug. The accept
      // landing is /accept-invitation/[invitationId].
      //
      // Delivery runs on a non-blocking next/server `after()` task so a slow or
      // rate-limited SMTP provider can never stall (or 500) the invite request, and
      // the whole send is wrapped so a failure is LOGGED rather than swallowed.
      // (sendEmail itself also no-ops+logs when MAIL_SMTP_URL is unset, and never
      // throws on an SMTP error.) `after`/`@/lib/email` are imported lazily for the
      // same reason as the other callbacks: this config is loaded under the jiti
      // CLI loader at migrate time, where those Next-only modules don't resolve.
      sendInvitationEmail: async ({ id, email, organization, inviter }) => {
        const { after } = await import("next/server");
        after(async () => {
          try {
            const { sendEmail } = await import("@/lib/email");
            const { subject, html, text } = invitationEmail({
              url: `${betterAuthUrl()}/accept-invitation/${id}`,
              appUrl: betterAuthUrl(),
              orgName: organization?.name || "your team",
              inviterName: inviter?.user?.name,
            });
            await sendEmail({ to: email, subject, html, text });
          } catch (err) {
            // Never let a mail failure surface; log so a missed invite is diagnosable.
            // eslint-disable-next-line no-console
            console.error(
              `[invite-email] failed to send to ${email}: ${String(err)}`,
            );
          }
        });
      },
    }),

    // Passwordless magic-link sign-in as a secondary method on the auth screens.
    magicLink({
      sendMagicLink: async ({ email, url }) => {
        // sendEmail no-ops+logs when MAIL_SMTP_URL is unset (lib/email.ts), so a
        // no-email self-host can still sign in by copying the link from the logs.
        const { sendEmail } = await import("@/lib/email");
        const { subject, html, text } = magicLinkEmail({
          url,
          appUrl: betterAuthUrl(),
        });
        await sendEmail({ to: email, subject, html, text });
      },
    }),

    // Short-lived EdDSA JWTs + JWKS endpoint. The Go API verifies these via JWKS.
    jwt({
      jwks: {
        keyPairConfig: { alg: "EdDSA" },
      },
      jwt: {
        // 5 to 15 min short-lived tokens. The verified token
        // carries the org/role claims the Go API uses for authz.
        expirationTime: "10m",
        // The Go API PINS iss + aud on every token. Stamp them from the SAME env it
        // verifies against (JWT_ISSUER / JWT_AUDIENCE) so issuer (dashboard) and
        // verifier (API) agree. Without this Better Auth defaults aud=baseURL (the
        // dashboard URL), which the API rejects with 401.
        issuer: jwtIssuer(),
        audience: jwtAudience(),
        // CUSTOM CLAIMS the Go API reads (internal/auth/jwks.go Claims): `org_id` is
        // the user's ACTIVE organization, REQUIRED for the per-request RLS tenant
        // context (without it the API rejects the request as unauthorized). email/
        // email_verified back the allowlist authz path. `role` is intentionally
        // omitted: it's a hint the API re-checks LIVE against identity.member, so a stale
        // claim can't grant admin. `sub` (user id) is set separately by getSubject.
        //
        // The firstOrgId fallback mirrors customAccessTokenClaims below: the session
        // hook's active-org backfill is best-effort, and a session it missed (a
        // transient lookup failure at sign-in) would otherwise mint org_id="" for its
        // whole lifetime, locking the user out until they re-authenticate. Falling
        // back to the live membership at mint time heals such sessions. A user with
        // no membership at all still mints "" (onboarding is the fix there).
        definePayload: async ({ user, session }) => ({
          org_id:
            session.activeOrganizationId ?? (await firstOrgId(user.id)) ?? "",
          email: user.email,
          email_verified: user.emailVerified,
        }),
      },
    }),

    // OAuth 2.1 authorization server for the Dropway MCP server. An LLM client adds
    // the MCP URL as a custom connector → discovers this AS → the user signs in and
    // approves on /oauth/consent → the client receives a JWT access token. The token
    // carries `org_id` (customAccessTokenClaims), which the MCP resource server reads
    // to scope RLS, the same claim shape the Go verifier already expects.
    oauthProvider({
      loginPage: "/sign-in",
      consentPage: "/oauth/consent",
      // After login, BEFORE consent, force a user with no organization through
      // onboarding so the minted token always carries org_id. The dashboard's
      // (app) layout has its own onboarding gate, but the OAuth authorize flow
      // (CLI `dropway login` / MCP connect) BYPASSES that layout, so a first-time
      // user, classically a Google-SSO signup, would otherwise reach consent with no
      // org and get a token with org_id="" that the API/MCP reject ("token has no
      // organization"). shouldRedirect → true sends them to /onboarding (carrying the
      // signed OAuth query); after they create the org, the page calls /oauth2/continue
      // (postLogin:true) to resume → consent → the client. consentReferenceId is unused
      // (we don't tie consent to a scope reference id), so it returns undefined.
      postLogin: {
        page: "/onboarding",
        consentReferenceId: async () => undefined,
        shouldRedirect: async ({ user }) => {
          const orgId = await firstOrgId(user.id);
          return !orgId;
        },
      },
      // The scopes a client may request. We keep the OIDC defaults (so this stays a
      // valid OIDC provider, "openid" is required for that) and add a custom "mcp"
      // scope: the MCP server advertises scopes_supported:["mcp"] in its RFC 9728
      // metadata, so MCP clients request scope=mcp; it must be a registered scope or
      // the authorize step 400s with invalid_scope. DCR clients inherit this list as
      // their allowed scopes (clientRegistrationAllowedScopes defaults to it).
      scopes: ["openid", "profile", "email", "offline_access", "mcp"],
      // Dynamic Client Registration (RFC 7591) is REQUIRED for the MCP "paste a
      // URL" UX: an MCP client (Claude/Cursor/Codex) self-registers a client_id
      // anonymously the first time it hits the server, the user has no client
      // credentials to enter, they authenticate later in the browser authorize
      // step. Enabling this also publishes `registration_endpoint` in the AS
      // metadata, which MCP clients discover and call. Without unauthenticated DCR
      // the connector flow dead-ends (no way to register).
      allowDynamicClientRegistration: true,
      allowUnauthenticatedClientRegistration: true,
      // The MCP server's resource id must be a VALID audience for this plugin to
      // mint a JWT (not opaque) access token: the provider only issues a JWT when
      // the client sends an RFC 8707 `resource` param AND it's in validAudiences;
      // the token's `aud` then equals that resource (which the MCP Go verifier
      // pins). Compliant MCP clients read the resource from the server's RFC 9728
      // metadata, which advertises exactly this URL. `iss` is the jwt() plugin's
      // issuer (jwtIssuer()), the same value the MCP verifier expects. We register
      // BOTH the bare and trailing-slash forms because some MCP clients (e.g.
      // mcp-remote) URL-canonicalize the resource and append a "/"
      // ("http://host" → "http://host/"). The Go API audience (jwtAudience) is also
      // registered so the CLI's `dropway login` can request a token the API accepts.
      // Also register the connect-URL forms (MCP_URL = NEXT_PUBLIC_MCP_URL, the
      // ".../mcp" endpoint shown in the Connect modal). The RFC 9728 metadata
      // advertises the BARE resource (mcpResourceUrl), so a compliant client sends
      // that, but some clients (Claude's built-in connector) use the connection URL
      // itself as the RFC 8707 resource, i.e. ".../mcp". Without these the issued
      // token's aud wouldn't match and the MCP server 401s. The MCP verifier accepts
      // the same set (services/mcp WithExtraAudiences).
      validAudiences: [
        mcpResourceUrl(),
        mcpResourceUrl() + "/",
        MCP_URL,
        MCP_URL + "/",
        jwtAudience(),
        jwtAudience() + "/",
      ],
      customAccessTokenClaims: async ({ user }) => {
        if (!user) return {};
        const orgId = await firstOrgId(user.id);
        return orgId ? { org_id: orgId } : {};
      },
    }),
  ],
});

export type Auth = typeof auth;
