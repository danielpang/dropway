// SPDX-License-Identifier: FSL-1.1-Apache-2.0

/**
 * Canonical OAuth `aud` / RFC 8707 `resource` forms for the Dropway MCP server.
 *
 * MUST stay in lockstep with Go `internal/auth.MCPResourceAudiences`: Better Auth
 * mints the access token with whichever form the client sent, and both the MCP
 * gate and the Go API accept this same set. If the dashboard's
 * `oauthProvider.validAudiences` omits a form, the token endpoint rejects the
 * handshake with `invalid_request` / "requested resource invalid" BEFORE a token
 * is minted — which is what production PostHog issue
 * 01a05aa3-af99-75c0-80de-afdc432e507b was (`oauth token failed: invalid_request`
 * for `resource=https://mcp.dropway.dev/mcp`).
 *
 * Forms:
 *   - bare URL            the RFC 9728 resource the MCP server advertises
 *   - trailing slash      clients that URL-canonicalize (e.g. mcp-remote)
 *   - `.../mcp`           the connector URL shown in the Connect modal
 *   - `.../mcp/`          Claude's built-in connector (RFC 8707 resource)
 *
 * `publicUrl`'s trailing slash is ignored so `https://mcp.example` and
 * `https://mcp.example/` produce the same four strings.
 */
export function mcpResourceAudiences(publicUrl: string): string[] {
  const base = publicUrl.replace(/\/+$/, "");
  return [base, `${base}/`, `${base}/mcp`, `${base}/mcp/`];
}

/**
 * The full `validAudiences` list for the Better Auth oauthProvider plugin:
 * every MCP resource form (from both the server-side MCP_PUBLIC_URL and the
 * public NEXT_PUBLIC_MCP_URL, which can theoretically differ) plus the Go API
 * audience the CLI's `dropway login` requests.
 */
export function oauthProviderValidAudiences(input: {
  mcpResourceUrl: string;
  mcpUrl: string;
  jwtAudience: string;
}): string[] {
  const jwt = input.jwtAudience.replace(/\/+$/, "");
  return unique([
    ...mcpResourceAudiences(input.mcpResourceUrl),
    ...mcpResourceAudiences(input.mcpUrl),
    jwt,
    `${jwt}/`,
  ]);
}

function unique(values: string[]): string[] {
  return [...new Set(values)];
}
