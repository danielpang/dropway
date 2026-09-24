// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ChatGPT's MCP connector does not always authorize with the same redirect_uri
// it registered at Dynamic Client Registration. OpenAI documents two callbacks
// on the connector host:
//
//   https://chatgpt.com/connector_platform_oauth_redirect
//   https://chatgpt.com/connector/oauth/{callback_id}
//
// When the authorization server advertises RFC 9207 issuer identification,
// ChatGPT uses the stable URI; otherwise it uses the per-connection callback.
// Better Auth then requires an exact string match against the client's
// registered redirect URIs and answers invalid_redirect when they differ.
//
// This module decides whether a requested ChatGPT callback may be appended to
// a client that ALREADY registered a ChatGPT callback on the same host. It
// does not accept arbitrary chatgpt.com URLs, and it does not accept a second
// distinct callback id (that would let a public client_id send the auth code
// to a different ChatGPT callback).

const CHATGPT_HOSTS = new Set(["chatgpt.com", "chat.openai.com"]);
const STABLE_PATH = "/connector_platform_oauth_redirect";
const CALLBACK_PREFIX = "/connector/oauth/";
const CALLBACK_ID = /^[A-Za-z0-9_-]{1,128}$/;

export type ChatgptRedirect =
  | { kind: "stable"; host: string }
  | { kind: "callback"; host: string; id: string };

function canonicalForm(parsed: ChatgptRedirect): string {
  return parsed.kind === "stable"
    ? `https://${parsed.host}${STABLE_PATH}`
    : `https://${parsed.host}${CALLBACK_PREFIX}${parsed.id}`;
}

/**
 * The requested redirect we are willing to store. The URL parser folds dot
 * segments, backslashes, and surrounding whitespace into a chatgpt.com path,
 * so classification alone is not enough: the raw string must be the canonical
 * callback, or the stable callback with one trailing slash.
 */
export function acceptableRequestedRedirect(raw: string): string | null {
  const parsed = parseChatgptRedirect(raw);
  if (!parsed) return null;
  const canonical = canonicalForm(parsed);
  if (raw === canonical) return raw;
  if (parsed.kind === "stable" && raw === `${canonical}/`) return raw;
  return null;
}

/**
 * Classify a redirect_uri as one of ChatGPT's documented connector callbacks.
 * Returns null for every other URL, including other paths on chatgpt.com.
 * Registered values may be slightly non-canonical; use
 * `acceptableRequestedRedirect` before storing a new one.
 */
export function parseChatgptRedirect(raw: string): ChatgptRedirect | null {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (url.protocol !== "https:") return null;
  if (url.username || url.password) return null;
  if (url.port) return null;
  if (url.search || url.hash) return null;
  const host = url.hostname.toLowerCase();
  if (!CHATGPT_HOSTS.has(host)) return null;

  let path = url.pathname;
  // One trailing slash is part of the stable callback only. A trailing slash
  // on /connector/oauth/{id} is a different path and is not accepted.
  if (path === `${STABLE_PATH}/`) path = STABLE_PATH;
  if (path.includes("\\") || path.split("/").includes("..")) return null;

  if (path === STABLE_PATH) return { kind: "stable", host };
  if (!path.startsWith(CALLBACK_PREFIX)) return null;
  const id = path.slice(CALLBACK_PREFIX.length);
  if (!CALLBACK_ID.test(id)) return null;
  return { kind: "callback", host, id };
}

/**
 * The exact redirect_uri string to append to the client's allowlist, or null
 * when the request must be left unchanged (already registered, not a ChatGPT
 * callback, or a second distinct callback id).
 *
 * The returned string is the URI the client sent, not a canonical rewrite.
 * Better Auth matches with ===, and the authorization code is bound to that
 * same string, which the token request must repeat.
 */
export function redirectToRegister(
  requestedRaw: string,
  registered: readonly string[],
): string | null {
  const requestedRawOk = acceptableRequestedRedirect(requestedRaw);
  if (!requestedRawOk) return null;
  const requested = parseChatgptRedirect(requestedRawOk);
  if (!requested) return null;
  if (registered.includes(requestedRawOk)) return null;

  const sameHost = registered
    .map((raw) => parseChatgptRedirect(raw))
    .filter((parsed): parsed is ChatgptRedirect => parsed !== null && parsed.host === requested.host);
  if (sameHost.length === 0) return null;

  if (requested.kind === "stable") return requestedRawOk;

  const ids = sameHost.filter((parsed) => parsed.kind === "callback");
  const hasThisId = ids.some((parsed) => parsed.kind === "callback" && parsed.id === requested.id);
  const hasOtherId = ids.some((parsed) => parsed.kind === "callback" && parsed.id !== requested.id);
  const hasStable = sameHost.some((parsed) => parsed.kind === "stable");
  // A different callback id is already on the client. Refuse another one so a
  // known client_id cannot be pointed at someone else's ChatGPT callback.
  if (hasOtherId) return null;
  if (hasThisId || hasStable) return requestedRawOk;
  return null;
}

/** A callback id that is not already on this client's allowlist. */
export function isNewCallbackId(
  requestedRaw: string,
  registered: readonly string[],
): boolean {
  const requested = parseChatgptRedirect(requestedRaw);
  if (!requested || requested.kind !== "callback") return false;
  return !registered.some((raw) => {
    const existing = parseChatgptRedirect(raw);
    return (
      existing?.kind === "callback" &&
      existing.host === requested.host &&
      existing.id === requested.id
    );
  });
}

/**
 * Redirects to store at dynamic registration when the client already sent a
 * documented ChatGPT callback. Dropway advertises RFC 9207 issuer
 * identification, and ChatGPT then authorizes with the fixed stable callback
 * even if registration only listed the per-connection id. Adding that stable
 * URL here means the authorize request matches before any consent exists.
 * Returns null when nothing needs to be added.
 */
export function expandChatgptRegistrationRedirects(value: unknown): string[] | null {
  const uris = coerceRedirectUris(value);
  if (!uris || uris.length === 0) return null;
  const additions: string[] = [];
  for (const raw of uris) {
    const parsed = parseChatgptRedirect(raw);
    if (!parsed) continue;
    const stable = `https://${parsed.host}${STABLE_PATH}`;
    if (uris.includes(stable) || additions.includes(stable)) continue;
    additions.push(stable);
  }
  if (additions.length === 0) return null;
  return [...uris, ...additions];
}

/** Allowlist to persist when an alias should be added; null when no write is needed. */
export function nextRedirectUris(
  requested: string,
  registered: readonly string[],
): string[] | null {
  const toAdd = redirectToRegister(requested, registered);
  if (!toAdd) return null;
  return [...registered, toAdd];
}

/**
 * redirectUris comes back from the adapter as a string array. Postgres jsonb
 * (and a supportsArrays:false round-trip) can also surface the JSON text.
 * Anything else is treated as unreadable so we do not guess an allowlist.
 */
export function coerceRedirectUris(value: unknown): string[] | null {
  if (Array.isArray(value)) {
    return value.every((item) => typeof item === "string") ? value : null;
  }
  if (typeof value === "string") {
    try {
      return coerceRedirectUris(JSON.parse(value));
    } catch {
      return null;
    }
  }
  if (value == null) return [];
  return null;
}

export type OAuthClientRedirectRecord = {
  redirectUris?: unknown;
  /** When true, authorize issues a code without a consent screen. */
  skipConsent?: boolean | null;
};

/**
 * Append a documented ChatGPT callback onto an existing OAuth client before
 * the authorize endpoint's exact redirect match. No-ops unless the client
 * already registered a ChatGPT callback on the same host.
 */
export async function registerChatgptAuthorizeRedirect(input: {
  clientId: string | undefined;
  redirectUri: string | undefined;
  findClient: (
    clientId: string,
  ) => Promise<OAuthClientRedirectRecord | null | undefined>;
  updateRedirects: (clientId: string, redirectUris: string[]) => Promise<void>;
  /**
   * Whether any user has already consented to this client. Required before a
   * brand-new callback id is stored: that id is chosen by the authorize
   * request, and an existing consent would send the code there with no new
   * approval screen. Missing or failing lookup fails closed for that case.
   * Adding the fixed stable URL does not consult this.
   */
  clientHasConsent?: (clientId: string) => Promise<boolean>;
}): Promise<"updated" | "unchanged" | "skipped"> {
  const { clientId, redirectUri } = input;
  if (!clientId || !redirectUri) return "skipped";
  // Parse before any database read so ordinary (non-ChatGPT) clients, including
  // the localhost clients used by the CLI and e2e, never touch oauthClient.
  if (!acceptableRequestedRedirect(redirectUri)) return "skipped";

  const client = await input.findClient(clientId);
  if (!client) return "skipped";
  const current = coerceRedirectUris(client.redirectUris);
  if (!current) return "skipped";
  if (!(await mayStoreRedirect(input, clientId, redirectUri, current, client.skipConsent))) {
    return "unchanged";
  }

  // Re-read so a callback id committed by a concurrent authorize is not
  // overwritten with a stale copy of the allowlist.
  const latestClient = await input.findClient(clientId);
  if (!latestClient) return "skipped";
  const latest = coerceRedirectUris(latestClient.redirectUris);
  if (!latest) return "skipped";
  if (
    !(await mayStoreRedirect(
      input,
      clientId,
      redirectUri,
      latest,
      latestClient.skipConsent,
    ))
  ) {
    return "unchanged";
  }
  const next = nextRedirectUris(redirectUri, latest);
  if (!next) return "unchanged";
  await input.updateRedirects(clientId, next);
  return "updated";
}

async function mayStoreRedirect(
  input: {
    clientHasConsent?: (clientId: string) => Promise<boolean>;
  },
  clientId: string,
  redirectUri: string,
  registered: readonly string[],
  skipConsent: boolean | null | undefined,
): Promise<boolean> {
  if (!nextRedirectUris(redirectUri, registered)) return false;
  if (!isNewCallbackId(redirectUri, registered)) return true;
  if (skipConsent) return false;
  try {
    if (!input.clientHasConsent) return false;
    return !(await input.clientHasConsent(clientId));
  } catch {
    return false;
  }
}
