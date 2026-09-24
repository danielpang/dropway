// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// MCP desktop clients (Cursor, Claude, mcp-remote, …) and the Dropway CLI register
// a loopback redirect at Dynamic Client Registration, then authorize with
// `http://localhost:{port}/callback` or `http://127.0.0.1:{port}/callback`.
//
// Better Auth's built-in match allows port variance only when the registered and
// requested hostnames are the same *loopback IP literal* (127.0.0.0/8 or ::1).
// The hostname `localhost` requires an exact string match, so a cached client_id
// from an earlier session (different port) or a localhost vs 127.0.0.1 mismatch
// fails authorize with invalid_redirect even though the callback is still on the
// user's machine (RFC 8252 §7.3).
//
// When a DCR client already registered one loopback callback, this module appends
// (or replaces prior loopback entries for the same path with) the URI the client
// sent on authorize, before Better Auth's exact match runs.

import {
  coerceRedirectUris,
  type OAuthClientRedirectRecord,
} from "@/lib/oauth-chatgpt-redirect";

const CALLBACK_PATH = "/callback";

/** Loopback hosts allowed for native-app OAuth redirects (RFC 8252). */
function isLoopbackRedirectHost(hostname: string): boolean {
  const host = hostname.toLowerCase();
  if (host === "localhost" || host.endsWith(".localhost")) return true;
  if (host === "::1") return true;
  const octets = host.split(".");
  if (octets.length === 4 && octets[0] === "127") {
    return octets.every((o) => /^\d{1,3}$/.test(o) && Number(o) <= 255);
  }
  return false;
}

export type LoopbackRedirect = {
  host: string;
  pathname: string;
};

/**
 * Classify a redirect_uri as an OAuth native-app loopback callback we manage.
 * Only `http` on loopback hosts with path `/callback` (Dropway CLI + MCP clients).
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
  if (!isLoopbackRedirectHost(url.hostname)) return null;
  if (url.pathname !== CALLBACK_PATH) return null;
  if (url.pathname.includes("\\") || url.pathname.split("/").includes("..")) {
    return null;
  }
  return { host: url.hostname.toLowerCase(), pathname: url.pathname };
}

/** The redirect_uri string to store, or null when the request must not change the client. */
export function redirectToRegister(
  requestedRaw: string,
  registered: readonly string[],
): string | null {
  const requested = parseLoopbackRedirect(requestedRaw);
  if (!requested) return null;
  if (registered.includes(requestedRaw)) return null;

  const hasLoopbackPeer = registered.some((raw) => {
    const existing = parseLoopbackRedirect(raw);
    return (
      existing !== null && existing.pathname === requested.pathname
    );
  });
  if (!hasLoopbackPeer) return null;
  return requestedRaw;
}

/** Persisted allowlist when a loopback alias should be added; null when unchanged. */
export function nextRedirectUris(
  requested: string,
  registered: readonly string[],
): string[] | null {
  const toAdd = redirectToRegister(requested, registered);
  if (!toAdd) return null;
  const parsed = parseLoopbackRedirect(toAdd);
  if (!parsed) return null;
  const kept = registered.filter((raw) => {
    const existing = parseLoopbackRedirect(raw);
    if (!existing) return true;
    return existing.pathname !== parsed.pathname;
  });
  return [...kept, toAdd];
}

/**
 * Append (or rotate) a loopback redirect onto an existing OAuth client before
 * Better Auth's authorize redirect check. Skips clients with no loopback URI yet.
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
  if (!current) return "skipped";
  const next = nextRedirectUris(redirectUri, current);
  if (!next) return "unchanged";

  const latestClient = await input.findClient(clientId);
  if (!latestClient) return "skipped";
  const latest = coerceRedirectUris(latestClient.redirectUris);
  if (!latest) return "skipped";
  const latestNext = nextRedirectUris(redirectUri, latest);
  if (!latestNext) return "unchanged";

  await input.updateRedirects(clientId, latestNext);
  return "updated";
}
