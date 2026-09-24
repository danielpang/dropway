// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Desktop and manual MCP connects register one loopback redirect and authorize
// with another. RFC 8252 §7.3 says the authorization server matches loopback
// redirects on scheme, host, path, and query, and ignores the port. Better
// Auth does that only for IP literals (127.0.0.0/8, ::1). A `localhost` name
// is exact-match only, and `localhost` is not treated as the same host as
// `127.0.0.1`.
//
// A manual connect that reached this error did exactly that: dynamic
// registration returned 200, then /oauth2/authorize one second later sent
// `http://localhost:<port>/callback` and Better Auth answered invalid_redirect.
// ChatGPT's own documented callbacks (connector_platform_oauth_redirect and
// connector/oauth/{id}) are handled separately; this module is the loopback
// mismatch those callbacks do not cover.
//
// This module appends the requested loopback URI when the client already
// registered a loopback URI with the same path. It does not accept a loopback
// redirect for a client that only registered a remote callback (that would
// let a known client_id send the authorization code off the user's machine).

import { coerceRedirectUris, type OAuthClientRedirectRecord } from "@/lib/oauth-chatgpt-redirect";

function isLoopbackHostname(hostname: string): boolean {
  // Node reports the IPv6 loopback hostname with brackets (`[::1]`).
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (host === "localhost" || host === "::1") return true;
  const parts = host.split(".");
  if (parts.length !== 4 || parts[0] !== "127") return false;
  return parts.every((part) => {
    if (!/^(0|[1-9]\d{0,2})$/.test(part)) return false;
    return Number(part) <= 255;
  });
}

/** Path identity for sibling matching. One trailing slash does not make a new path. */
function normalizedPath(pathname: string): string | null {
  if (!pathname.startsWith("/")) return null;
  if (pathname.includes("\\") || pathname.split("/").includes("..")) return null;
  if (pathname.length > 1 && pathname.endsWith("/")) return pathname.slice(0, -1);
  return pathname;
}

type LoopbackRedirect = { href: string; path: string };

/**
 * A loopback redirect we are willing to compare. The raw string must be the
 * URL parser's href before we store it: the authorization code is bound to
 * the exact string the client will repeat at the token endpoint.
 */
export function parseLoopbackRedirect(raw: string): LoopbackRedirect | null {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (url.protocol !== "http:") return null;
  if (url.username || url.password) return null;
  if (url.search || url.hash) return null;
  if (!isLoopbackHostname(url.hostname)) return null;
  const path = normalizedPath(url.pathname);
  if (!path) return null;
  if (url.href !== raw) return null;
  return { href: raw, path };
}

/**
 * The exact redirect_uri to append, or null when the request must be left
 * unchanged (not a canonical loopback callback, already registered, or the
 * client has no loopback callback with the same path).
 */
export function loopbackRedirectToRegister(
  requestedRaw: string,
  registered: readonly string[],
): string | null {
  const requested = parseLoopbackRedirect(requestedRaw);
  if (!requested) return null;
  if (registered.includes(requestedRaw)) return null;
  const sibling = registered.some((raw) => {
    const existing = parseLoopbackRedirect(raw);
    return existing !== null && existing.path === requested.path;
  });
  if (!sibling) return null;
  return requestedRaw;
}

export function nextLoopbackRedirectUris(
  requested: string,
  registered: readonly string[],
): string[] | null {
  const toAdd = loopbackRedirectToRegister(requested, registered);
  if (!toAdd) return null;
  return [...registered, toAdd];
}

/**
 * Append a loopback sibling onto an existing OAuth client before the authorize
 * endpoint's exact redirect match. No-ops unless this client already
 * registered a loopback callback with the same path.
 */
export async function registerLoopbackAuthorizeRedirect(input: {
  clientId: string | undefined;
  redirectUri: string | undefined;
  findClient: (
    clientId: string,
  ) => Promise<OAuthClientRedirectRecord | null | undefined>;
  updateRedirects: (clientId: string, redirectUris: string[]) => Promise<void>;
}): Promise<"updated" | "unchanged" | "skipped"> {
  const { clientId, redirectUri } = input;
  if (!clientId || !redirectUri) return "skipped";
  if (!parseLoopbackRedirect(redirectUri)) return "skipped";

  const client = await input.findClient(clientId);
  if (!client) return "skipped";
  const current = coerceRedirectUris(client.redirectUris);
  if (!current || !nextLoopbackRedirectUris(redirectUri, current)) return current ? "unchanged" : "skipped";

  const latestClient = await input.findClient(clientId);
  if (!latestClient) return "skipped";
  const latest = coerceRedirectUris(latestClient.redirectUris);
  if (!latest) return "skipped";
  const next = nextLoopbackRedirectUris(redirectUri, latest);
  if (!next) return "unchanged";
  await input.updateRedirects(clientId, next);
  return "updated";
}
