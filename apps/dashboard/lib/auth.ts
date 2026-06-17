import { randomUUID } from "node:crypto";

import { betterAuth } from "better-auth";
import { jwt, magicLink, organization } from "better-auth/plugins";
import { oauthProvider } from "@better-auth/oauth-provider";
import { Pool } from "pg";

import {
  betterAuthSecret,
  betterAuthUrl,
  databaseUrl,
  googleClientId,
  googleClientSecret,
  jwtAudience,
  jwtIssuer,
  mcpResourceUrl,
  requireEmailVerification,
} from "@/lib/env";

// NOTE: `@/lib/email` is imported LAZILY inside the send callbacks below, never at
// module top level. The `@better-auth/cli migrate` step loads THIS config under a
// plain-Node (jiti) loader — not the Next.js bundler — and lib/email.ts starts with
// `import "server-only"`, which only resolves under Next. A top-level import here
// would make `Cannot find module 'server-only'` break the one-time auth migration
// (CI + every fresh self-host). Deferring it keeps the config import graph free of
// server-only/nodemailer; the dynamic import runs only at send time, in the real
// Next runtime.

/**
 * Better Auth server instance — the authoritative owner of the `identity` schema
 * (user/session/account/verification/jwks/organization/member/invitation).
 * Named `identity` (not `auth`) to avoid colliding with Postgres providers that
 * reserve their own `auth` schema (e.g. Supabase's GoTrue).
 *
 * Architecture notes (see docs/ARCHITECTURE.md §2.2, §5, §8):
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
// A single shared pg Pool for Better Auth's `identity` schema (search_path pinned
// to identity). Reused by the session hook below to look up a user's organization.
const authPool = new Pool({
  connectionString: databaseUrl(),
  options: "-c search_path=identity",
});

/**
 * The user's first organization id — the default "active org" for a new session AND
 * the org stamped into MCP OAuth access tokens (customAccessTokenClaims below), so the
 * MCP resource server can scope RLS straight from the token. Best-effort: returns
 * undefined on a lookup error or a user with no membership.
 */
async function firstOrgId(userId: string): Promise<string | undefined> {
  try {
    const res = await authPool.query<{ organizationId: string }>(
      `SELECT "organizationId" FROM identity.member WHERE "userId" = $1 ORDER BY "createdAt" LIMIT 1`,
      [userId],
    );
    return res.rows[0]?.organizationId;
  } catch {
    return undefined;
  }
}

export const auth = betterAuth({
  appName: "Dropway",
  baseURL: betterAuthUrl(),
  secret: betterAuthSecret(),

  // Postgres via a node-postgres Pool. Better Auth uses its built-in Kysely
  // adapter when handed a `Pool`, generating + migrating its own identity tables.
  //
  // Better Auth OWNS the `identity` schema (architecture §5/§8): `databaseUrl()` is
  // a PRIVILEGED connection (it must CREATE its tables), and `options` pins the
  // session search_path to `identity` so the adapter's UNqualified tables (user,
  // session, member, organization, …) are created in + read from `identity` —
  // exactly where the Go API reads them (identity.member) for authz.
  database: authPool,

  // Set the user's first organization as the session's ACTIVE org on every new
  // session. Better Auth only sets activeOrganizationId on org create/switch (the
  // onboarding flow), so a RETURNING user who just signs in would otherwise have no
  // active org, and the jwt() plugin's definePayload would then mint org_id="". The
  // Go API rejects that (RLS needs the tenant), 500-ing with "claims missing org_id".
  // This backfills the active org so the minted JWT always carries it. Failures are
  // non-fatal (a user with no membership simply gets none; onboarding sets it).
  databaseHooks: {
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
      },
    },
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
      await sendEmail({
        to: user.email,
        subject: "Reset your Dropway password",
        text:
          `Reset your Dropway password by opening this link:\n\n${url}\n\n` +
          `If you didn't request this, you can safely ignore this email.`,
      });
    },
  },

  // Email-verification link. Only actually GATES sign-in when
  // REQUIRE_EMAIL_VERIFICATION=true; the callback is registered regardless so the
  // link is sent (or logged) whenever Better Auth asks to verify an address.
  emailVerification: {
    sendOnSignUp: true,
    sendVerificationEmail: async ({ user, url }) => {
      const { sendEmail } = await import("@/lib/email");
      await sendEmail({
        to: user.email,
        subject: "Verify your email for Dropway",
        text:
          `Welcome to Dropway! Confirm your email by opening this link:\n\n${url}\n\n` +
          `If you didn't create a Dropway account, you can ignore this email.`,
      });
    },
  },

  socialProviders: {
    google: {
      clientId: googleClientId(),
      clientSecret: googleClientSecret(),
    },
  },

  // Cookie hardening for the dashboard origin (app.dropway.dev). The session
  // cookie is host-only (no Domain=) so it never reaches the content domain.
  advanced: {
    cookiePrefix: "dropway",
    // Secure cookies (and the `__Secure-` name prefix Better Auth adds with them)
    // require an HTTPS origin — browsers REJECT them over http://. Drive this off
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
  },

  plugins: [
    // Orgs/members/roles/invitations out of the box. Creator = owner; solo
    // users get a default single-member org. Roles: owner | admin | member.
    organization({
      // Authorization detail (admin-only policy/role changes) is enforced in the
      // Go API + DB CHECK/trigger; this plugin provides the membership tables.
      allowUserToCreateOrganization: true,
      // The AUTHORITATIVE members_per_org cap is the Go API preflight the invite
      // path calls (open-core: OSS unlimited, cloud per-tier — H8). This only LIFTS
      // Better Auth's restrictive default membershipLimit (100), which would
      // otherwise break an Enterprise org (cap 1000) and cap an unlimited self-host.
      // Keep it well above any tier cap so it never spuriously blocks.
      membershipLimit: 100_000,
    }),

    // Passwordless magic-link sign-in as a secondary method on the auth screens.
    magicLink({
      sendMagicLink: async ({ email, url }) => {
        // sendEmail no-ops+logs when MAIL_SMTP_URL is unset (lib/email.ts), so a
        // no-email self-host can still sign in by copying the link from the logs.
        const { sendEmail } = await import("@/lib/email");
        await sendEmail({
          to: email,
          subject: "Your Dropway sign-in link",
          text:
            `Sign in to Dropway by opening this link:\n\n${url}\n\n` +
            `This link expires shortly. If you didn't request it, ignore this email.`,
        });
      },
    }),

    // Short-lived EdDSA JWTs + JWKS endpoint. The Go API verifies these via JWKS.
    jwt({
      jwks: {
        keyPairConfig: { alg: "EdDSA" },
      },
      jwt: {
        // 5–15 min short-lived tokens (architecture §2.2). The verified token
        // carries the org/role claims the Go API uses for authz.
        expirationTime: "10m",
        // The Go API PINS iss + aud on every token. Stamp them from the SAME env it
        // verifies against (JWT_ISSUER / JWT_AUDIENCE) so issuer (dashboard) and
        // verifier (API) agree. Without this Better Auth defaults aud=baseURL (the
        // dashboard URL), which the API rejects with 401.
        issuer: jwtIssuer(),
        audience: jwtAudience(),
        // CUSTOM CLAIMS the Go API reads (internal/auth/jwks.go Claims): `org_id` is
        // the user's ACTIVE organization — REQUIRED for the per-request RLS tenant
        // context (without it the API 500s "claims missing org_id"). email/
        // email_verified back the allowlist authz path. `role` is intentionally
        // omitted: it's a hint the API re-checks LIVE against identity.member, so a stale
        // claim can't grant admin. `sub` (user id) is set separately by getSubject.
        definePayload: ({ user, session }) => ({
          org_id: session.activeOrganizationId ?? "",
          email: user.email,
          email_verified: user.emailVerified,
        }),
      },
    }),

    // OAuth 2.1 authorization server for the Dropway MCP server. An LLM client adds
    // the MCP URL as a custom connector → discovers this AS → the user signs in and
    // approves on /oauth/consent → the client receives a JWT access token. The token
    // carries `org_id` (customAccessTokenClaims), which the MCP resource server reads
    // to scope RLS — the same claim shape the Go verifier already expects.
    oauthProvider({
      loginPage: "/sign-in",
      consentPage: "/oauth/consent",
      // The scopes a client may request. We keep the OIDC defaults (so this stays a
      // valid OIDC provider — "openid" is required for that) and add a custom "mcp"
      // scope: the MCP server advertises scopes_supported:["mcp"] in its RFC 9728
      // metadata, so MCP clients request scope=mcp; it must be a registered scope or
      // the authorize step 400s with invalid_scope. DCR clients inherit this list as
      // their allowed scopes (clientRegistrationAllowedScopes defaults to it).
      scopes: ["openid", "profile", "email", "offline_access", "mcp"],
      // Dynamic Client Registration (RFC 7591) is REQUIRED for the MCP "paste a
      // URL" UX: an MCP client (Claude/Cursor/Codex) self-registers a client_id
      // anonymously the first time it hits the server — the user has no client
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
      // issuer (jwtIssuer()) — the same value the MCP verifier expects. We register
      // BOTH the bare and trailing-slash forms because some MCP clients (e.g.
      // mcp-remote) URL-canonicalize the resource and append a "/"
      // ("http://host" → "http://host/"); the MCP verifier accepts both too.
      validAudiences: [mcpResourceUrl(), mcpResourceUrl() + "/"],
      customAccessTokenClaims: async ({ user }) => {
        if (!user) return {};
        const orgId = await firstOrgId(user.id);
        return orgId ? { org_id: orgId } : {};
      },
    }),
  ],
});

export type Auth = typeof auth;
