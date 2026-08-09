// SPDX-License-Identifier: FSL-1.1-Apache-2.0

/**
 * MCP OAuth token lifetimes (seconds) for the Better Auth oauthProvider plugin.
 *
 * Product rule: a user connects Dropway once; the grant must keep working for
 * `list_sites`, `create_site`, deploy, and every other MCP tool until they
 * manually disconnect the connector (or an org admin flips mcp_enabled off).
 *
 * Why these are so long: MCP clients (Claude/Cursor/Codex) refresh when the
 * access token's `expires_in` approaches. Better Auth rotates refresh tokens on
 * every refresh and treats any reuse of a rotated RT as theft, wiping the whole
 * token family ("family revocation"). Hourly access tokens made that race
 * common — reads could still work on a cached AT while writes (create_site /
 * deploy) failed with 401 until the user re-authorized. Far-future access
 * tokens mean clients almost never refresh, so the grant stays intact.
 *
 * Cap stays under 2^31-1 seconds (~68y) so clients that store `expires_in` in
 * a signed 32-bit int don't overflow. JWTs still carry `exp` (required by the
 * Go verifier); "forever" here means effectively non-expiring for product use.
 */
export const MCP_OAUTH_ACCESS_TOKEN_EXPIRES_IN = 60 * 60 * 24 * 365 * 50; // 50 years
export const MCP_OAUTH_REFRESH_TOKEN_EXPIRES_IN = 60 * 60 * 24 * 365 * 50; // 50 years (sliding)

/** Upper bound for OAuth `expires_in` values we mint (signed 32-bit max). */
export const MCP_OAUTH_EXPIRES_IN_MAX_SAFE = 2_147_483_647;
