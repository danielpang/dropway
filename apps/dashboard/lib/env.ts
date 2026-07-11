/**
 * Centralized, validated access to the environment variables the dashboard
 * depends on. Secrets stay out of the bundle: only NEXT_PUBLIC_* values are
 * safe to read on the client. Server-only getters throw if accessed in the
 * browser as a guard against accidental leakage.
 */

function requireServer(): void {
  if (typeof window !== "undefined") {
    throw new Error("Server-only environment variable accessed on the client");
  }
}

/**
 * Postgres connection string Better Auth uses for its identity tables. Better Auth
 * OWNS + migrates the `identity` schema, so it connects with a PRIVILEGED role (DDL +
 * DML on the identity schema), distinct from the Go API's restricted, non-BYPASSRLS
 * `dropway_app` DATABASE_URL, which only needs SELECT on auth.* for authz reads.
 *
 * Prefers BETTER_AUTH_DATABASE_URL; falls back to DATABASE_URL for a single-role
 * setup. (lib/auth.ts also pins the connection's search_path to `identity` so the
 * unqualified Better Auth tables are created in + read from that schema.)
 */
export function databaseUrl(): string {
  requireServer();
  const v = process.env.BETTER_AUTH_DATABASE_URL ?? process.env.DATABASE_URL;
  if (!v) throw new Error("BETTER_AUTH_DATABASE_URL / DATABASE_URL is not set");
  return v;
}

/** Better Auth signing secret (cookies, tokens). */
export function betterAuthSecret(): string {
  requireServer();
  const v = process.env.BETTER_AUTH_SECRET;
  if (!v) throw new Error("BETTER_AUTH_SECRET is not set");
  return v;
}

/** Canonical base URL of the dashboard (e.g. https://app.dropway.dev). */
export function betterAuthUrl(): string {
  // Available on both sides; Better Auth's client reads it for the base path.
  return (
    process.env.BETTER_AUTH_URL ??
    process.env.NEXT_PUBLIC_BETTER_AUTH_URL ??
    "http://localhost:3000"
  );
}

/**
 * Marketing/landing site URL (e.g. https://www.dropway.dev) the auth screens link
 * back to via a "Back to landing page" control. UNSET → no link is rendered, since
 * a self-host typically has no separate marketing site (the dashboard IS the site).
 * NEXT_PUBLIC_ so the client AuthForm can read it after it's passed down from the
 * server page.
 */
export function landingUrl(): string | undefined {
  return process.env.NEXT_PUBLIC_LANDING_URL || undefined;
}

/**
 * The `iss` / `aud` Better Auth stamps on the JWTs the Go API verifies. The API
 * PINS both (internal/auth/jwks.go: WithIssuer/WithAudience), and Better Auth
 * otherwise defaults BOTH to its baseURL, so the API rejects the token (aud =
 * dashboard URL, not the API). Reading the SAME JWT_ISSUER / JWT_AUDIENCE env the
 * API verifies against makes the two agree by construction.
 */
export function jwtIssuer(): string {
  requireServer();
  return process.env.JWT_ISSUER ?? betterAuthUrl();
}

export function jwtAudience(): string {
  requireServer();
  return process.env.JWT_AUDIENCE ?? "http://localhost:8080";
}

/**
 * Whether email verification is required before sign-in. Default FALSE: a self-host
 * without an email provider wired can't SEND a verification email, so requiring it
 * would lock every new user out. Production sets REQUIRE_EMAIL_VERIFICATION=true
 * alongside a real provider (the sendVerificationEmail seam).
 */
export function requireEmailVerification(): boolean {
  requireServer();
  return process.env.REQUIRE_EMAIL_VERIFICATION === "true";
}

/**
 * SMTP connection URL the dashboard sends transactional mail through
 * (verification, password reset, magic links). SMTP is a vendor-neutral seam:
 * point it at your own server, Gmail, SES, Mailgun, Postmark, Resend's SMTP, or
 * the bundled local Mailpit, no hard dependency on any one provider.
 *
 * UNSET → email is a no-op (the message is logged, not sent). That keeps a
 * no-email self-host working: signups succeed and the link is recoverable from
 * the dashboard logs. An internet-facing deploy MUST set this (and flip
 * REQUIRE_EMAIL_VERIFICATION=true) so verification mail actually reaches users.
 */
export function mailSmtpUrl(): string | undefined {
  requireServer();
  return process.env.MAIL_SMTP_URL || undefined;
}

/** From address on outgoing mail. Defaults to a local dev sender. */
export function mailFrom(): string {
  requireServer();
  return process.env.MAIL_FROM ?? "Dropway <no-reply@localhost>";
}

/**
 * Inbox the in-app contact form (bug reports / feature requests) delivers to.
 * The contact action routes each submission here via the same SMTP seam
 * (lib/email.ts) the auth flows use, so it inherits the vendor-neutral transport
 * and the "log, don't send" degradation when MAIL_SMTP_URL is unset.
 *
 * UNSET → the contact form is unavailable (the footer link hides and the action
 * refuses), rather than silently dropping user feedback to nowhere. Set
 * SUPPORT_EMAIL to a monitored address to turn it on.
 */
export function supportEmail(): string | undefined {
  requireServer();
  return process.env.SUPPORT_EMAIL || undefined;
}

export function googleClientId(): string {
  requireServer();
  return process.env.GOOGLE_CLIENT_ID ?? "";
}

export function googleClientSecret(): string {
  requireServer();
  return process.env.GOOGLE_CLIENT_SECRET ?? "";
}

/**
 * The deployment environment label stamped onto every analytics event
 * (`environment` property) so PostHog can segment production from staging,
 * self-host, and local dev. Sourced from a single literal `ENVIRONMENT` var,
 * falling back to NODE_ENV ("development" / "production") and then "development".
 *
 * `ENVIRONMENT` is exposed to the BROWSER bundle via next.config's `env` (Next
 * only auto-exposes NEXT_PUBLIC_*), so the same value drives both server-side
 * capture (posthog-node) and the browser SDK. It is resolved at build time.
 */
export function appEnvironment(): string {
  return process.env.ENVIRONMENT || process.env.NODE_ENV || "development";
}

/**
 * PostHog project API key for the BROWSER SDK (posthog-js). The project API key
 * (`phc_…`) is safe to expose — it can only ingest events, not read data — so it
 * ships as NEXT_PUBLIC_. UNSET → analytics is disabled (the provider no-ops), so
 * a self-host with no PostHog simply collects nothing.
 */
export function posthogClientKey(): string | undefined {
  return process.env.NEXT_PUBLIC_POSTHOG_KEY || undefined;
}

/**
 * PostHog project API key for SERVER-side capture (posthog-node): signups, sites
 * created, domains added. Prefers a server-only POSTHOG_KEY but falls back to the
 * same public project key (it's the same `phc_…` ingest key). UNSET → server
 * analytics no-ops.
 */
export function posthogServerKey(): string | undefined {
  requireServer();
  return process.env.POSTHOG_KEY || process.env.NEXT_PUBLIC_POSTHOG_KEY || undefined;
}

/**
 * PostHog ingestion host. Defaults to US cloud; set NEXT_PUBLIC_POSTHOG_HOST to
 * the EU host (https://eu.posthog.com) or a reverse-proxy origin. NEXT_PUBLIC_ so
 * both the browser and server clients read the same value.
 */
export function posthogHost(): string {
  return process.env.NEXT_PUBLIC_POSTHOG_HOST || "https://us.posthog.com";
}

/**
 * Base URL of the Go API (api.dropway.dev) the dashboard calls for ALL business
 * data, carrying a short-lived Better Auth EdDSA JWT, it never opens a Postgres
 * connection for business data.
 *
 * SERVER-side (RSC / server actions) prefers the runtime, non-public `API_URL` env:
 * in Docker that's the INTERNAL service URL (http://api:8080), because inside the
 * dashboard container `localhost` is the container, not the api. The BROWSER can't
 * see a non-public env, so it falls back to the baked NEXT_PUBLIC_API_URL
 * (http://localhost:8080 → the host-published api). In production both resolve to the
 * same public api URL, so NEXT_PUBLIC_API_URL alone suffices there.
 */
export const API_URL: string =
  process.env.API_URL ??
  process.env.NEXT_PUBLIC_API_URL ??
  "http://localhost:8080";

/**
 * Public base URL of the Dropway MCP server (mcp.dropway.dev in cloud, the
 * host-published :8092 locally). Shown in the "Connect" instructions so a user can
 * add it as a custom MCP connector in Claude / Cursor / Codex. Must be NEXT_PUBLIC_
 * because the Connect modal is a client component. The MCP server itself is the
 * OAuth resource server; clients discover the dashboard authorization server from
 * its 401 (RFC 9728), so only this one URL is needed in the UI.
 */
export const MCP_URL: string =
  process.env.NEXT_PUBLIC_MCP_URL ?? "http://localhost:8092";

/**
 * The MCP server's canonical resource identifier, the `aud` an OAuth access token
 * for the MCP server must carry, and what the dashboard registers as a valid
 * audience so the @better-auth/oauth-provider plugin issues a JWT (not opaque) access
 * token for it. MUST equal both (a) the `resource` the MCP server advertises in its
 * RFC 9728 metadata and (b) the audience the MCP Go verifier enforces (MCP_PUBLIC_URL
 * on that service). Server-side only: prefers the runtime MCP_PUBLIC_URL (the exact
 * value the mcp service is configured with), falling back to the public URL. No
 * trailing slash so the audience string is byte-identical across services.
 */
export function mcpResourceUrl(): string {
  return (
    process.env.MCP_PUBLIC_URL ??
    process.env.NEXT_PUBLIC_MCP_URL ??
    "http://localhost:8092"
  ).replace(/\/$/, "");
}
