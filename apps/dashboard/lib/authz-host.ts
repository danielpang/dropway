/**
 * Host + redirect validation for the /authz viewer exchange (architecture §6).
 *
 * The /authz route takes attacker-influenceable query params (`host`, `next`)
 * and ultimately 302s the browser to `https://<host>/__authz/callback?...&next=`.
 * Both values are therefore open-redirect / token-exfiltration vectors and MUST
 * be validated before they are trusted:
 *
 *  - `host` must be the platform content suffix `*.shippedusercontent.com` OR a
 *    custom domain the Go API recognizes. We accept the structural form here;
 *    the Go API is the authoritative resolver (an unknown host → 404 on mint),
 *    so a forged custom host never yields a token. We still reject obviously
 *    hostile shapes (ports, userinfo, paths, wildcards) so we never build a URL
 *    pointing somewhere we didn't intend.
 *  - `next` must be a SAME-SITE PATH (begins with a single "/", not "//" or
 *    "/\"), never an absolute URL — otherwise the post-auth redirect on the
 *    content host could be pointed off-site.
 */

/** The platform's content PSL domain. Sites serve from `<slug>.<this>`. */
export const CONTENT_SUFFIX = ".shippedusercontent.com";

/** A single DNS hostname: labels of [a-z0-9-] separated by dots, no trailing dot. */
const HOSTNAME_RE =
  /^(?=.{1,253}$)(?!-)[a-z0-9-]{1,63}(?<!-)(?:\.(?!-)[a-z0-9-]{1,63}(?<!-))+$/;

/** Chars that must never appear in a host (scheme/port/path/userinfo/wildcard). */
const HOST_FORBIDDEN_RE = /[/\\:@?#*\s[\]]/;

/**
 * Validate a content host. Returns the normalized (lowercased) host, or null if
 * it is not a syntactically valid hostname. A valid host is either under the
 * platform content suffix or any other registrable domain (a custom domain the
 * Go API may recognize) — but never a bare IP, a host with a port/userinfo/path,
 * or a wildcard.
 */
export function normalizeContentHost(raw: string | null | undefined): string | null {
  if (!raw) return null;
  const host = raw.trim().toLowerCase();

  if (HOST_FORBIDDEN_RE.test(host)) return null;
  if (!HOSTNAME_RE.test(host)) return null;

  // Reject the apex of the content suffix itself (must have a label in front).
  if (host === CONTENT_SUFFIX.slice(1)) return null;

  return host;
}

/** True when the host is a platform content host (`<label>.shippedusercontent.com`). */
export function isPlatformContentHost(host: string): boolean {
  return host.endsWith(CONTENT_SUFFIX) && host.length > CONTENT_SUFFIX.length;
}

/**
 * True if the string contains any ASCII whitespace or control character. Used to
 * reject CR/LF/TAB in a redirect path (redirect-splitting / header injection).
 */
function hasControlChar(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    if (s.charCodeAt(i) <= 0x20 || s.charCodeAt(i) === 0x7f) return true;
  }
  return false;
}

/**
 * Validate the post-auth `next` target. Must be a same-site absolute PATH:
 * starts with "/", is not protocol-relative ("//") or a backslash trick
 * ("/\"), and contains no control/whitespace chars. Falls back to "/" on
 * anything suspicious.
 */
export function safeNextPath(raw: string | null | undefined): string {
  if (!raw) return "/";
  const next = raw.trim();
  if (!next.startsWith("/")) return "/";
  if (next.startsWith("//") || next.startsWith("/\\")) return "/";
  if (hasControlChar(next)) return "/";
  return next;
}

/**
 * Build the content-host callback URL the browser is redirected to after a
 * successful mint. The Worker's `/__authz/callback` verifies the token
 * (aud == host), sets the `__Host-edge` cookie, and 302s to `next`.
 */
export function callbackUrl(host: string, token: string, next: string): string {
  const u = new URL(`https://${host}/__authz/callback`);
  u.searchParams.set("token", token);
  u.searchParams.set("next", next);
  return u.toString();
}
