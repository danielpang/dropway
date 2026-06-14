// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Edge rate limiting + denial-of-wallet guard (docs/ARCHITECTURE.md §10
// "[MEDIUM] Denial-of-wallet", §12 Phase-4 "edge rate limiting + denial-of-wallet
// caps"). Two independent controls, both READ-ONLY-ish projections over KV so the
// Worker stays a thin consumer:
//
//   1. A per-(host|IP) request RATE LIMITER. The PRIMARY limiter is Cloudflare's
//      native Rate Limiting binding (`rateLimitNative`) — an ATOMIC, edge-local
//      counter that correctly counts a single-IP flood. A KV fixed-window counter
//      (`rateLimitDecision`) is kept ONLY as a best-effort fallback for self-host
//      deployments without the native binding: KV throttles writes to ~1/sec/key
//      and reads are eventually consistent, so it CANNOT count a real hot-key flood
//      (it never trips) — never rely on it as the sole DoW control. Over the limit
//      → 429 with a `Retry-After`.
//
//   2. A per-ORG SUSPENSION / over-limit signal — `org_status:<org_id>` in KV,
//      written by the Go API on billing suspension / quota over-limit. When set,
//      the route's content is NOT served; a platform "account suspended / over
//      limit" page is returned instead (so a runaway/abusive or unpaid org can't
//      keep spending our edge budget).
//
// Both controls keep the PUBLIC fast path cheap: org-status is a single KV get
// keyed by the org we already resolved from the route, and the rate-limit window
// counter is a single get + (best-effort) put. A missing limiter binding makes
// rate limiting a no-op (fail OPEN — availability over a soft DoW control); the
// org-suspension check, by contrast, fails OPEN too (a missing status === active)
// because the AUTHORITATIVE block is the Go API + billing, and KV is a cache.

/**
 * Minimal KV surface for the COUNTER store: a string get, a put with TTL, keyed
 * by the rate-limit window key. Separate from the route KV's json-get so the
 * limiter can run against the ROUTES KV (with a `rl:` prefix) or a dedicated
 * LIMITS binding. `put` is best-effort (a failed write just loosens the limit).
 */
export interface CounterKVLike {
  get(key: string): Promise<string | null>;
  put(key: string, value: string, opts?: { expirationTtl?: number }): Promise<void>;
}

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

/** The KV key prefix for fixed-window rate-limit counters (on the counter KV). */
export const RATE_LIMIT_PREFIX = "rl:" as const;

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

/**
 * The KV counter key for an identity's CURRENT fixed window. Folding the window
 * index into the key means each window is a fresh counter that simply expires —
 * no read-modify-reset race, and a stale counter just lapses via its TTL.
 */
export function windowKey(identity: string, nowMs: number, policy: RateLimitPolicy): string {
  const windowIndex = Math.floor(nowMs / 1000 / policy.windowSeconds);
  return `${RATE_LIMIT_PREFIX}${identity}:${windowIndex}`;
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

/**
 * PURE-ish fixed-window rate-limit decision over an injected counter KV + clock.
 *
 * Reads the current window's counter, increments it, and writes it back with a
 * TTL of one window (so the key self-expires). Returns `allowed=false` once the
 * post-increment count exceeds `policy.limit`, with `retryAfterSeconds` set to
 * the time remaining in the window.
 *
 * Failure mode: any KV error → fail OPEN (allowed:true). Rate limiting is a soft
 * denial-of-wallet control; a KV hiccup must not take the edge down. The
 * authoritative spend caps live in the Go API (per-org egress caps + 402).
 *
 * No binding (`kv === undefined`) is also a no-op allow, so the limiter is opt-in
 * per deployment.
 */
export async function rateLimitDecision(
  kv: CounterKVLike | undefined,
  identity: string,
  nowMs: number,
  policy: RateLimitPolicy = DEFAULT_RATE_LIMIT,
): Promise<RateLimitResult> {
  const remainingInWindow =
    policy.windowSeconds - (Math.floor(nowMs / 1000) % policy.windowSeconds);

  if (!kv) {
    return { allowed: true, count: 0, retryAfterSeconds: 0 };
  }

  try {
    const key = windowKey(identity, nowMs, policy);
    const prev = await kv.get(key);
    const prevCount = prev === null ? 0 : parseCount(prev);
    const count = prevCount + 1;

    // Persist the new count. Cloudflare KV requires `expirationTtl >= 60s`, so we
    // clamp to that floor; the window-indexed key means an over-long TTL is
    // harmless (a new window is a new key, and the stale key just lingers a little
    // longer before lapsing). Best-effort: a failed put simply means the next
    // request re-reads the old value (looser, never tighter).
    const ttl = Math.max(KV_MIN_TTL_SECONDS, remainingInWindow + 1);
    await kv.put(key, String(count), { expirationTtl: ttl }).catch(() => {});

    if (count > policy.limit) {
      return { allowed: false, count, retryAfterSeconds: remainingInWindow };
    }
    return { allowed: true, count, retryAfterSeconds: 0 };
  } catch {
    // Fail open on any limiter error.
    return { allowed: true, count: 0, retryAfterSeconds: 0 };
  }
}

/** Parse a stored counter string defensively (a garbled value reads as 0). */
function parseCount(raw: string): number {
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n >= 0 ? n : 0;
}

/** Cloudflare KV's minimum `expirationTtl` is 60s; clamp so short windows are valid. */
export const KV_MIN_TTL_SECONDS = 60;
