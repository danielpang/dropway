// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Edge rate limiting + denial-of-wallet guard ("[MEDIUM] Denial-of-wallet",
// Phase-4 "edge rate limiting + denial-of-wallet caps"). Two independent
// controls:
//
//   1. A per-(IP|host) request RATE LIMITER, backed ONLY by Cloudflare's native
//      Rate Limiting binding (`rateLimitNative`): edge-local, no KV operations.
//      Over the limit → 429 with a `Retry-After`. There is deliberately NO KV
//      fallback: a KV-backed counter costs one KV write per request (assets and
//      bot probes included), which exhausted the account-wide daily KV write quota
//      and blocked every API route write until it reset. Without the binding,
//      rate limiting is a no-op (fail OPEN) and the Worker logs a warning.
//
//   2. A per-ORG SUSPENSION / over-limit signal: `org_status:<org_id>` in KV,
//      written by the Go API on billing suspension / quota over-limit. When set,
//      the route's content is NOT served; a platform "account suspended / over
//      limit" page is returned instead. One KV read, never a write.
//
// The Worker never writes KV on the request path. Both controls fail OPEN: a
// missing binding or KV error never blocks a real request; the authoritative
// spend caps and suspension live in the Go API + billing.

/** Minimal KV surface for the org-status read (a single string get). */
export interface StatusKVLike {
  get(key: string): Promise<string | null>;
}

/**
 * Cloudflare's native Rate Limiting binding surface: a single ATOMIC
 * `limit({ key })` that returns `{ success }`. Unlike a KV counter this is
 * edge-local and correctly counts a flood on one key, so it is the PRIMARY edge
 * limiter (configured in wrangler.toml as a `ratelimit` binding). Declared as a
 * minimal interface so tests can inject a mock.
 */
export interface RateLimiterLike {
  limit(opts: { key: string }): Promise<{ success: boolean }>;
}

/**
 * The PRIMARY rate-limit decision: Cloudflare's native, atomic limiter keyed by
 * the request identity. `{ success:false }` → over the limit (429). A limiter
 * error FAILS OPEN (availability over a soft denial-of-wallet control; the
 * authoritative spend caps live in the Go API). retryAfterSeconds is the policy
 * window (the native binding doesn't expose a precise reset).
 */
export async function rateLimitNative(
  limiter: RateLimiterLike,
  identity: string,
  windowSeconds: number = DEFAULT_RATE_LIMIT.windowSeconds,
): Promise<RateLimitResult> {
  try {
    const { success } = await limiter.limit({ key: identity });
    return success
      ? { allowed: true, count: 0, retryAfterSeconds: 0 }
      : { allowed: false, count: 0, retryAfterSeconds: windowSeconds };
  } catch {
    return { allowed: true, count: 0, retryAfterSeconds: 0 };
  }
}

/** Tunable rate-limit policy. Defaults target abusive bursts, not real traffic. */
export interface RateLimitPolicy {
  /** Max requests allowed per window per identity. */
  limit: number;
  /** Window length in seconds (fixed window). */
  windowSeconds: number;
}

/** Conservative default: 600 requests / 60s per identity (10 req/s sustained). */
export const DEFAULT_RATE_LIMIT: RateLimitPolicy = { limit: 600, windowSeconds: 60 };

/** The KV key prefix for the per-org status signal (on the status KV). */
export const ORG_STATUS_PREFIX = "org_status:" as const;

/**
 * Org status values the Go API writes to `org_status:<org_id>`. Anything other
 * than these (including an absent key, an empty string, or `active`) means the
 * org may be served. `suspended` (billing/abuse) and `over_limit` (quota/egress
 * cap) both block content and show the platform page.
 */
export type OrgStatus = "active" | "suspended" | "over_limit";

/** True when an org-status string means "do not serve tenant content". */
export function isBlockingStatus(status: string | null): status is "suspended" | "over_limit" {
  return status === "suspended" || status === "over_limit";
}

/**
 * Read the per-org status from KV (`org_status:<org_id>`). Returns the raw status
 * string, or null when unset/unavailable. A KV error is swallowed to null (fail
 * OPEN — the authoritative suspension lives in the Go API/billing; KV is a cache
 * and a transient miss must not take every site offline).
 */
export async function readOrgStatus(
  kv: StatusKVLike | undefined,
  orgId: string,
): Promise<string | null> {
  if (!kv) return null;
  try {
    const raw = await kv.get(`${ORG_STATUS_PREFIX}${orgId}`);
    if (raw === null) return null;
    const trimmed = raw.trim();
    // Accept either a bare status string or a small JSON envelope `{status,...}`
    // (the Go projection may grow metadata). Be liberal in what we read.
    if (trimmed.startsWith("{")) {
      try {
        const obj = JSON.parse(trimmed) as { status?: unknown };
        return typeof obj.status === "string" ? obj.status : null;
      } catch {
        return null;
      }
    }
    return trimmed;
  } catch {
    return null;
  }
}

/** The identity a request is rate-limited under: prefer the client IP, else host. */
export function rateLimitIdentity(request: Request, host: string): string {
  // Cloudflare populates CF-Connecting-IP with the true client IP at the edge.
  const ip = request.headers.get("CF-Connecting-IP") ?? request.headers.get("X-Real-IP");
  if (ip && ip.trim() !== "") return `ip:${ip.trim()}`;
  // No IP header (tests / unusual origins) → fall back to per-host limiting so a
  // single abusive host can't escape the limiter entirely.
  return `host:${host}`;
}

/** Outcome of a rate-limit check. */
export interface RateLimitResult {
  /** True when the request is within the limit and may proceed. */
  allowed: boolean;
  /** Count AFTER this request (for observability / headers). */
  count: number;
  /** Seconds the client should wait before retrying (set when !allowed). */
  retryAfterSeconds: number;
}
