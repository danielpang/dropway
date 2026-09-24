// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { createHash } from "node:crypto";

// Better Auth binds an authorization code to the literal redirect_uri from
// /authorize and checks it again at /token. Some desktop MCP clients use
// localhost in one request and 127.0.0.1 in the other. Both names reach the
// same local callback, but Better Auth rejects the code before checking PKCE.
// Only repair that one host spelling difference for Codex-shaped callbacks.
function codexLoopbackCallback(raw: string): URL | null {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (url.href !== raw || url.protocol !== "http:") return null;
  if (url.hostname !== "localhost" && url.hostname !== "127.0.0.1") return null;
  if (!url.port || url.username || url.password || url.search || url.hash) return null;
  if (!/^\/callback(?:\/[A-Za-z0-9_-]{1,128})?$/.test(url.pathname)) return null;
  return url;
}

export function equivalentCodexLoopbackCallbacks(
  authorized: string,
  tokenRequest: string,
): boolean {
  const first = codexLoopbackCallback(authorized);
  const second = codexLoopbackCallback(tokenRequest);
  return Boolean(
    first && second &&
      first.hostname !== second.hostname &&
      first.port === second.port &&
      first.pathname === second.pathname,
  );
}

/**
 * Return the callback recorded with this code only for the narrow loopback
 * spelling difference. Better Auth still consumes the code and checks client_id,
 * expiry, PKCE, and its exact redirect match after the request is patched.
 */
export async function authorizedCodexLoopbackRedirect(input: {
  clientId: unknown;
  code: unknown;
  redirectUri: unknown;
  findVerification: (identifier: string) => Promise<{ value?: unknown } | null | undefined>;
}): Promise<string | null> {
  const { clientId, code, redirectUri } = input;
  if (typeof clientId !== "string" || !clientId ||
      typeof code !== "string" || !/^[A-Za-z0-9]{32}$/.test(code) ||
      typeof redirectUri !== "string" || !codexLoopbackCallback(redirectUri)) return null;

  // @better-auth/oauth-provider@1.6.23 stores authorization codes using
  // SHA-256/base64url with no padding (its default `storeTokens: "hashed"`).
  const identifier = createHash("sha256").update(code).digest("base64url");
  const verification = await input.findVerification(identifier);
  if (typeof verification?.value !== "string") return null;
  let stored: unknown;
  try {
    stored = JSON.parse(verification.value);
  } catch {
    return null;
  }
  if (!stored || typeof stored !== "object") return null;
  const record = stored as { type?: unknown; query?: { client_id?: unknown; redirect_uri?: unknown } };
  const authorized = record.query?.redirect_uri;
  if (record.type !== "authorization_code" || record.query?.client_id !== clientId ||
      typeof authorized !== "string") return null;
  return equivalentCodexLoopbackCallbacks(authorized, redirectUri) ? authorized : null;
}
