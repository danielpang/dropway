// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Rate-limit rules for the unauthenticated OAuth surface (M4), consumed by
// betterAuth({ rateLimit: { customRules } }) in lib/auth.ts. Kept in this small,
// pg-free module so it can be unit-tested without importing the auth instance
// (which opens a live Postgres pool at import).
//
// Better Auth keys each bucket per client IP + path and applies the matching
// rule's rolling `window` (seconds) / `max`. Paths are relative to the auth
// router (e.g. the DCR endpoint is `/oauth2/register`, served at
// `/api/auth/oauth2/register`).

export interface RateLimitRule {
  window: number;
  max: number;
}

export const oauthRateLimitRules: Record<string, RateLimitRule> = {
  // Unauthenticated Dynamic Client Registration: the tightest bound. A user
  // connecting an MCP client registers once; a flood is abuse (oauth_application
  // row exhaustion + pooler pressure).
  "/oauth2/register": { window: 3600, max: 10 },
  // Consent + authorization-code start.
  "/oauth2/authorize": { window: 60, max: 30 },
  "/oauth2/consent": { window: 60, max: 30 },
  // Token issuance/refresh: a little more headroom for legitimate refreshes.
  "/oauth2/token": { window: 60, max: 60 },
};
