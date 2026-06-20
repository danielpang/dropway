import "server-only";

/**
 * Process-local, short-TTL cache for minted bearer JWTs.
 *
 * Better Auth's `getToken()` does a `jwks` table read + a private-key decrypt +
 * an EdDSA sign on EVERY call (it caches no key), so re-minting on each page
 * load is the dashboard's dominant per-request auth cost. The minted token is
 * valid for ~10 minutes; this cache reuses it for a short window (default 60s)
 * so a burst of navigations by the same user shares one mint instead of
 * re-signing every time.
 *
 * Keyed by session id + active org so a different user — or the SAME user after
 * an org switch — never reuses another context's token (the key changes the
 * moment `activeOrganizationId` does, so a switch re-mints immediately). The TTL
 * is kept far below the token's expiry, so a cached token is always still valid
 * when the Go API verifies it. State is per server instance — a fresh mint after
 * a deploy/instance start is correct — and bounded so it can't grow unboundedly.
 *
 * This deliberately does NOT try to invalidate on a downstream 401: these are
 * full-lifetime user JWTs (the verifier keeps rotated keys in its JWKS until the
 * token's own expiry), so a cached token stays verifiable for its whole life,
 * and any pathological staleness self-heals within one TTL.
 */
export interface TokenCacheOptions {
  /** How long a minted token is reused before re-minting. Default 60_000ms. */
  ttlMs?: number;
  /** Hard cap on retained entries; at the cap, stale entries are swept and then
   * the oldest insertion is evicted. Default 10_000. */
  maxEntries?: number;
  /** Injectable clock (tests). Defaults to `Date.now`. */
  now?: () => number;
}

interface Entry {
  token: string;
  expiresAt: number;
}

export class TokenCache {
  private readonly store = new Map<string, Entry>();
  private readonly ttlMs: number;
  private readonly maxEntries: number;
  private readonly now: () => number;

  constructor(options: TokenCacheOptions = {}) {
    this.ttlMs = options.ttlMs ?? 60_000;
    this.maxEntries = options.maxEntries ?? 10_000;
    this.now = options.now ?? Date.now;
  }

  /** A still-valid cached token for the key, or null on a miss / expiry. */
  get(key: string): string | null {
    const hit = this.store.get(key);
    if (!hit) return null;
    if (hit.expiresAt <= this.now()) {
      // Drop the expired entry eagerly so it can't linger past its window.
      this.store.delete(key);
      return null;
    }
    return hit.token;
  }

  /** Cache a freshly minted token under the key for `ttlMs`. */
  set(key: string, token: string): void {
    this.evictIfNeeded();
    this.store.set(key, { token, expiresAt: this.now() + this.ttlMs });
  }

  /** Forget a key (so the next call re-mints). */
  delete(key: string): void {
    this.store.delete(key);
  }

  /** Retained entry count (inspection / tests). */
  get size(): number {
    return this.store.size;
  }

  // Keep the map bounded: at the cap, sweep expired entries first, then — if
  // everything is still live — drop oldest-first (Map preserves insertion order).
  private evictIfNeeded(): void {
    if (this.store.size < this.maxEntries) return;
    const now = this.now();
    for (const [k, v] of this.store) {
      if (v.expiresAt <= now) this.store.delete(k);
    }
    while (this.store.size >= this.maxEntries) {
      const oldest = this.store.keys().next().value;
      if (oldest === undefined) break;
      this.store.delete(oldest);
    }
  }
}

/**
 * Stable cache key for a session's token. Tenant-scoped: the active org is part
 * of the key so switching orgs (which changes the minted token's `org_id`
 * claim) never reuses the previous org's token. A missing active org collapses
 * to the empty string (the same "" the jwt plugin would stamp as `org_id`).
 */
export function tokenCacheKey(
  sessionId: string,
  activeOrgId: string | null | undefined,
): string {
  return `${sessionId}:${activeOrgId ?? ""}`;
}
