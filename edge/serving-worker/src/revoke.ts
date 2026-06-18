// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// HARD REVOCATION — the KV denylist / per-subject `min_iat` check on the GATED
// path ("Revocation story", "[HIGH] Revocation under staleness — mandatory
// deny-list", Phase-4 "KV denylist / min_iat hard-revocation"). The short
// 15-minute edge-token TTL is the backstop; this
// denylist makes a ban / unshare / org-suspension take effect IMMEDIATELY instead
// of waiting out the TTL.
//
// REVOCATION DENYLIST CONTRACT (the Go API writes, the Worker + /authz read):
//   revoked:user:<userId>  → { "min_iat": <unix seconds> }
//   revoked:site:<siteId>  → { "min_iat": <unix seconds> }
//   revoked:org:<orgId>    → { "min_iat": <unix seconds> }
// An edge token with claims {sub, site_id, org} is REJECTED if ANY of
//   revoked:user:<sub> / revoked:site:<site_id> / revoked:org:<org>
// has `min_iat > token.iat`. The org dimension is taken from the ROUTE value
// (the authoritative org for the content host the Go API projected), not from a
// token claim — the edge token carries no `org`, and the route binding is the
// trustworthy source.
//
// Direction of safety: the denylist is REBUILDABLE from Postgres and only ever
// fails CLOSED — a stale or partially-missing denylist causes at most an extra
// re-auth (302 → /authz), never opens access. A KV read error is therefore
// treated as "revoked unknown" → fail closed (302), matching the fail-closed
// mandate for the revocation check.

/** The KV key prefixes of the three denylist dimensions (matches the contract). */
export const REVOKED_USER_PREFIX = "revoked:user:" as const;
export const REVOKED_SITE_PREFIX = "revoked:site:" as const;
export const REVOKED_ORG_PREFIX = "revoked:org:" as const;

/**
 * Minimal KV surface for the denylist reads: three string gets per gated request
 * (user/site/org). Reuses the ROUTES KV (with the `revoked:` prefix) or a
 * dedicated REVOKED binding. Read-only — the Go API is the sole writer.
 */
export interface RevokedKVLike {
  get(key: string): Promise<string | null>;
}

/** A parsed denylist entry. Only `min_iat` is load-bearing. */
export interface RevokedEntry {
  /** Unix SECONDS: every token issued before this is invalid for the subject. */
  min_iat: number;
}

/**
 * Parse a raw denylist KV value into a `min_iat`. Accepts the contract's JSON
 * envelope `{ "min_iat": <number> }` and, defensively, a bare numeric string.
 * Returns null when the value is absent or unparseable — the caller treats an
 * UNPARSEABLE present-key as "no constraint" only for malformed data, but a KV
 * ERROR (vs a clean miss) is handled by `isRevoked` as fail-closed. A negative or
 * non-finite `min_iat` is ignored (reads as null).
 */
export function parseRevokedEntry(raw: string | null): RevokedEntry | null {
  if (raw === null) return null;
  const trimmed = raw.trim();
  if (trimmed === "") return null;

  let minIat: unknown;
  if (trimmed.startsWith("{")) {
    try {
      minIat = (JSON.parse(trimmed) as { min_iat?: unknown }).min_iat;
    } catch {
      return null;
    }
  } else {
    minIat = Number.parseInt(trimmed, 10);
  }

  if (typeof minIat !== "number" || !Number.isFinite(minIat) || minIat < 0) {
    return null;
  }
  return { min_iat: minIat };
}

/** The three denylist keys for a token's subject/site/org. */
export function denylistKeys(sub: string, siteId: string, orgId: string): {
  user: string;
  site: string;
  org: string;
} {
  return {
    user: `${REVOKED_USER_PREFIX}${sub}`,
    site: `${REVOKED_SITE_PREFIX}${siteId}`,
    org: `${REVOKED_ORG_PREFIX}${orgId}`,
  };
}

/** Inputs to the revocation check (the token's revocation-relevant claims + org). */
export interface RevocationSubject {
  /** Token `sub` (viewer user id, or anon:<random> for password mode). */
  sub: string;
  /** Token `site_id` (already verified == route.site_id). */
  siteId: string;
  /** The route's org_id (authoritative org for this content host). */
  orgId: string;
  /** Token `iat` (unix seconds). A token without one is 0 → always revocable. */
  iat: number;
}

/**
 * Decide whether a verified edge token is HARD-REVOKED by the KV denylist.
 *
 * Reads the three denylist keys (user/site/org) and returns true (→ caller 302s
 * to /authz for re-auth) if ANY has `min_iat > token.iat`. The three reads run in
 * parallel and ONLY for gated requests, so the public fast path is untouched.
 *
 * FAIL CLOSED: if the KV is unavailable (`kv === undefined`) or a read THROWS, we
 * return true (revoked) — a revocation check we could not complete must deny.
 * A clean MISS (key absent) is "not revoked" for that dimension. This is the
 * safe direction: the worst case of a stale/down denylist is an extra re-auth.
 */
export async function isRevoked(
  kv: RevokedKVLike | undefined,
  subject: RevocationSubject,
): Promise<boolean> {
  // No denylist binding configured → we cannot prove the token is NOT revoked.
  // Fail closed: better an extra /authz round-trip than serving a banned
  // viewer. A deployment that wants gated serving MUST wire the denylist KV.
  if (!kv) return true;

  const keys = denylistKeys(subject.sub, subject.siteId, subject.orgId);

  try {
    const [userRaw, siteRaw, orgRaw] = await Promise.all([
      kv.get(keys.user),
      kv.get(keys.site),
      kv.get(keys.org),
    ]);

    for (const raw of [userRaw, siteRaw, orgRaw]) {
      const entry = parseRevokedEntry(raw);
      // A token issued at or before `min_iat` is revoked. Using strict `>` means a
      // token minted at exactly `min_iat` is also revoked (min_iat is the first
      // VALID second), matching "all tokens issued before min_iat are invalid"
      // with second-granularity safety on the boundary.
      if (entry !== null && entry.min_iat > subject.iat) {
        return true;
      }
    }
    return false;
  } catch {
    // A KV error mid-check is indistinguishable from "maybe revoked" → fail closed.
    return true;
  }
}
