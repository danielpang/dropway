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

/** Postgres connection string Better Auth uses for its identity tables. */
export function databaseUrl(): string {
  requireServer();
  const v = process.env.DATABASE_URL;
  if (!v) throw new Error("DATABASE_URL is not set");
  return v;
}

/** Better Auth signing secret (cookies, tokens). */
export function betterAuthSecret(): string {
  requireServer();
  const v = process.env.BETTER_AUTH_SECRET;
  if (!v) throw new Error("BETTER_AUTH_SECRET is not set");
  return v;
}

/** Canonical base URL of the dashboard (e.g. https://app.shipped.app). */
export function betterAuthUrl(): string {
  // Available on both sides; Better Auth's client reads it for the base path.
  return (
    process.env.BETTER_AUTH_URL ??
    process.env.NEXT_PUBLIC_BETTER_AUTH_URL ??
    "http://localhost:3000"
  );
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
 * Public base URL of the Go API (api.shipped.app). The dashboard calls this for
 * ALL business data, carrying a short-lived Better Auth EdDSA JWT — it never
 * opens a Postgres connection for business data.
 */
export const API_URL: string =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
