import { betterAuth } from "better-auth";
import { jwt, magicLink, organization } from "better-auth/plugins";
import { Pool } from "pg";

import {
  betterAuthSecret,
  betterAuthUrl,
  databaseUrl,
  googleClientId,
  googleClientSecret,
  jwtAudience,
  jwtIssuer,
} from "@/lib/env";

/**
 * Better Auth server instance — the authoritative owner of the `auth` schema
 * (user/session/account/verification/jwks/organization/member/invitation).
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
export const auth = betterAuth({
  appName: "Shipped",
  baseURL: betterAuthUrl(),
  secret: betterAuthSecret(),

  // Postgres via a node-postgres Pool. Better Auth uses its built-in Kysely
  // adapter when handed a `Pool`, generating + migrating its own identity tables.
  //
  // Better Auth OWNS the `auth` schema (architecture §5/§8): `databaseUrl()` is a
  // PRIVILEGED connection (it must CREATE its tables), and `options` pins the
  // session search_path to `auth` so the adapter's UNqualified tables (user,
  // session, member, organization, …) are created in + read from `auth` — exactly
  // where the Go API reads them (auth.member) for authz.
  database: new Pool({
    connectionString: databaseUrl(),
    options: "-c search_path=auth",
  }),

  emailAndPassword: {
    enabled: true,
    // Email verification is required before sign-in (Google is pre-verified).
    requireEmailVerification: true,
    minPasswordLength: 8,
  },

  socialProviders: {
    google: {
      clientId: googleClientId(),
      clientSecret: googleClientSecret(),
    },
  },

  // Cookie hardening for the dashboard origin (app.shipped.app). The session
  // cookie is host-only (no Domain=) so it never reaches the content domain.
  advanced: {
    cookiePrefix: "shipped",
    useSecureCookies: process.env.NODE_ENV === "production",
    defaultCookieAttributes: {
      sameSite: "lax",
      httpOnly: true,
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
        // Wired to the transactional email provider by the infra agent. Until
        // then, log so local development can complete the flow by hand.
        if (process.env.NODE_ENV !== "production") {
          // eslint-disable-next-line no-console
          console.log(`[magic-link] ${email} -> ${url}`);
        }
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
        // omitted: it's a hint the API re-checks LIVE against auth.member, so a stale
        // claim can't grant admin. `sub` (user id) is set separately by getSubject.
        definePayload: ({ user, session }) => ({
          org_id: session.activeOrganizationId ?? "",
          email: user.email,
          email_verified: user.emailVerified,
        }),
      },
    }),
  ],
});

export type Auth = typeof auth;
