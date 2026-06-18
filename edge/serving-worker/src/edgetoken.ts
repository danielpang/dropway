// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Edge-token verification — the Worker side of the EDGE-TOKEN SPEC.
// The Go API "edge signer" mints a compact EdDSA JWT
// (a SEPARATE keypair from Better Auth's user JWT); this Worker verifies it with
// `jose` against the public JWKS served at GET /.well-known/edge-jwks.
//
// What we enforce on verify (and what we reject):
//   - alg pinned to EdDSA            → rejects `none` and HS* (algorithm confusion)
//   - iss == EDGE_TOKEN_ISSUER       → only our edge signer
//   - aud == the request content host→ a token minted for host A can't replay at B
//   - exp present + not past         → short-lived (15m); revoked sessions stop re-minting
//   - site_id claim == the route's site_id → token bound to THIS site, not a sibling
//   - mode claim ∈ {password,allowlist,org_only}
//
// The JWKS is fetched once and cached in-Worker (per isolate) with a short TTL;
// `kid` selects the key. Keys are imported as OKP/Ed25519 (alg=EdDSA). We NEVER
// read the operator dashboard JWT here — only the host-scoped edge token.

import { type JWK, type JWTPayload, importJWK, jwtVerify } from "jose";

import { EDGE_TOKEN_ISSUER } from "./config";

/** The pinned signing algorithm — the ONLY one accepted (rejects none/HS*). */
const EDGE_ALG = "EdDSA" as const;

/** Allowed `mode` claim values (mirrors internal/edgetoken on the Go side). */
const EDGE_MODES = ["password", "allowlist", "org_only"] as const;
export type EdgeMode = (typeof EDGE_MODES)[number];

/** Verified edge-token claims the serving path consumes. */
export interface EdgeClaims {
  /** Viewer user id (org_only/allowlist) or "anon:<random>" (password). */
  sub: string;
  /** Content host the token is bound to (aud). */
  aud: string;
  /** Site the token authorizes — re-checked against the route's site_id. */
  site_id: string;
  /** Access mode the token authorizes. */
  mode: EdgeMode;
  /**
   * Issued-at (unix SECONDS). Surfaced so the Phase-4 hard-revocation check can
   * compare it against the KV denylist's per-subject `min_iat`: a token whose
   * `iat` predates `revoked:{user|site|org}.min_iat` is treated as invalid even
   * before its 15-minute exp. `requiredClaims` does NOT force `iat`, so a token
   * minted without one reads as 0 (which a non-zero `min_iat` always revokes →
   * fail closed). The Go signer always sets `iat`.
   */
  iat: number;
}

/**
 * Minimal fetch surface so tests can inject a mocked JWKS endpoint without the
 * global `fetch`. The real Worker passes the runtime `fetch`.
 */
export type FetchLike = (input: string) => Promise<{
  ok: boolean;
  status: number;
  json: () => Promise<unknown>;
}>;

/** The OKP/Ed25519 JWKS shape the Go API serves (one or more keys). */
interface EdgeJWKS {
  keys: JWK[];
}

/**
 * In-isolate JWKS cache: imported CryptoKey-likes keyed by `kid`, with an expiry.
 * Cached on `globalThis` so it survives across requests within the same Worker
 * isolate (the common warm path) but is naturally bounded by isolate lifetime.
 */
interface JwksCacheEntry {
  /** kid → imported public key (jose `KeyLike`). */
  keys: Map<string, CryptoKey | Uint8Array>;
  /** Absolute epoch-ms after which the cache must be refetched. */
  expiresAt: number;
}

/** Default JWKS cache TTL (ms). Short so a key rotation propagates quickly. */
const JWKS_TTL_MS = 5 * 60_000;

/**
 * How long past its TTL a cached key set may still be served when a refetch is
 * FAILING (a transient JWKS outage). Bounded on purpose: an UNBOUNDED stale
 * fallback would let a rotated-out (or compromised) key keep verifying for the
 * whole isolate lifetime as long as the JWKS endpoint kept erroring (M1). After
 * TTL + this grace with no successful refetch we fail closed (throw → the caller
 * 302s to /authz) instead of trusting a key the signer may have revoked. Max stale
 * window ≈ JWKS_TTL_MS + this, measured from the last SUCCESSFUL fetch.
 */
const JWKS_STALE_GRACE_MS = 10 * 60_000;

/** The cache map: jwksUrl → entry. Module-scoped (one per isolate). */
const jwksCache = new Map<string, JwksCacheEntry>();

/** Reset the JWKS cache — test seam only. */
export function __resetJwksCacheForTests(): void {
  jwksCache.clear();
}

/**
 * Fetch + import the edge JWKS, caching the imported keys per `jwksUrl`. Imports
 * only OKP/Ed25519 (`alg=EdDSA`) keys; any other key type is skipped so a JWKS
 * carrying an unrelated key can't be coerced into the wrong algorithm.
 *
 * Returns the kid→key map. On a fetch/parse failure it returns the LAST GOOD
 * cached map when one is still around (even if past TTL) so a transient JWKS
 * outage doesn't 302 every viewer; only a cold cache with a failing fetch throws.
 */
async function loadKeys(
  jwksUrl: string,
  fetchImpl: FetchLike,
  now: number,
): Promise<Map<string, CryptoKey | Uint8Array>> {
  const cached = jwksCache.get(jwksUrl);
  if (cached && now < cached.expiresAt) return cached.keys;

  let doc: EdgeJWKS | null = null;
  try {
    const res = await fetchImpl(jwksUrl);
    if (res.ok) {
      const raw = (await res.json()) as unknown;
      doc = parseJwks(raw);
    }
  } catch {
    doc = null;
  }

  if (doc === null) {
    // Fetch/parse failed. Fall back to a stale-but-good cache ONLY within the
    // bounded grace window (M1) — past it, fail closed rather than trust keys the
    // signer may have rotated/revoked while the endpoint stays down.
    if (cached && now < cached.expiresAt + JWKS_STALE_GRACE_MS) return cached.keys;
    throw new Error("edge JWKS unavailable");
  }

  const keys = new Map<string, CryptoKey | Uint8Array>();
  for (const jwk of doc.keys) {
    // Only import OKP/Ed25519 signing keys; pin the alg at import time.
    if (jwk.kty !== "OKP" || jwk.crv !== "Ed25519" || typeof jwk.kid !== "string") {
      continue;
    }
    try {
      const key = await importJWK({ ...jwk, alg: EDGE_ALG }, EDGE_ALG);
      keys.set(jwk.kid, key as CryptoKey | Uint8Array);
    } catch {
      // Skip a malformed key rather than failing the whole set.
    }
  }

  if (keys.size === 0) {
    // A fetched-but-empty/all-filtered key set: keep serving the last good keys
    // only within the bounded grace window (M1), then fail closed.
    if (cached && now < cached.expiresAt + JWKS_STALE_GRACE_MS) return cached.keys;
    throw new Error("edge JWKS has no usable Ed25519 keys");
  }

  jwksCache.set(jwksUrl, { keys, expiresAt: now + JWKS_TTL_MS });
  return keys;
}

/** Validate the untrusted JWKS document shape. Returns null on any mismatch. */
function parseJwks(raw: unknown): EdgeJWKS | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const v = raw as Record<string, unknown>;
  if (!Array.isArray(v.keys)) return null;
  const keys: JWK[] = [];
  for (const k of v.keys) {
    if (k !== null && typeof k === "object" && !Array.isArray(k)) {
      keys.push(k as JWK);
    }
  }
  return { keys };
}

/** Parameters for verifying an edge token against a route. */
export interface VerifyParams {
  /** The compact JWT (cookie value). */
  token: string;
  /** The exact content host (becomes the required `aud`). */
  host: string;
  /** The route's site_id — the token's `site_id` claim MUST equal this. */
  siteId: string;
  /**
   * The ROUTE's current access_mode — the token's `mode` claim MUST equal this
   * (H1). Without this binding, a token minted while the site was one gated mode
   * (e.g. `password`, with an anon subject) would still verify after the operator
   * switched the site to another mode (e.g. `org_only`) without republishing,
   * serving member-only content to a password-token holder. Omit (undefined) only
   * where the route mode isn't applicable.
   */
  expectedMode?: string;
  /** The edge JWKS endpoint (env-configurable). */
  jwksUrl: string;
  /** Injected fetch (Worker runtime / test mock). */
  fetchImpl: FetchLike;
  /** Clock injection for tests; defaults to Date.now(). */
  now?: number;
}

/**
 * Verify an edge token and return its claims, or null if invalid for any reason
 * (bad signature, wrong alg, wrong iss/aud, expired, unknown kid, wrong site_id,
 * bad mode). Never throws on a bad token — callers fail closed to the 302/`/authz`
 * exchange. A genuine JWKS outage on a cold cache DOES throw (so the caller can
 * 302 rather than silently treating an outage as an invalid token); see serve.
 */
export async function verifyEdgeToken(p: VerifyParams): Promise<EdgeClaims | null> {
  if (!p.token || !p.host || !p.siteId) return null;
  const now = p.now ?? Date.now();

  const keys = await loadKeys(p.jwksUrl, p.fetchImpl, now);

  let payload: JWTPayload;
  try {
    const result = await jwtVerify(
      p.token,
      // Resolve the verification key by the token's `kid` header against the
      // imported JWKS. Pinning `algorithms: [EdDSA]` rejects none/HS* outright.
      async (header) => {
        const kid = header.kid;
        const key = typeof kid === "string" ? keys.get(kid) : undefined;
        if (key === undefined) throw new Error("unknown kid");
        return key;
      },
      {
        algorithms: [EDGE_ALG],
        issuer: EDGE_TOKEN_ISSUER,
        audience: p.host,
        // jose enforces exp automatically when present; require it explicitly so
        // a token without exp is rejected (mirrors the Go verifier).
        requiredClaims: ["exp", "sub"],
        // Default clock tolerance is 0; keep it tight (short-lived tokens).
        clockTolerance: 0,
        currentDate: new Date(now),
      },
    );
    payload = result.payload;
  } catch {
    return null;
  }

  // Edge-specific claims: site binding + mode. (aud/iss/exp already enforced.)
  const siteId = payload["site_id"];
  const mode = payload["mode"];
  if (typeof siteId !== "string" || siteId !== p.siteId) return null;
  if (typeof mode !== "string" || !(EDGE_MODES as readonly string[]).includes(mode)) {
    return null;
  }
  // H1: the token's mode must match the ROUTE's current access_mode — a token
  // minted for a now-stale mode (after a mode switch without republish) is invalid.
  if (p.expectedMode !== undefined && mode !== p.expectedMode) return null;
  const sub = payload.sub;
  if (typeof sub !== "string" || sub === "") return null;

  // `iat` (unix seconds) for the hard-revocation comparison. Absent/garbled → 0
  // so any non-zero denylist `min_iat` revokes the token (fail closed).
  const iat = typeof payload.iat === "number" && Number.isFinite(payload.iat) ? payload.iat : 0;

  // `aud` is normalized by jose to string|string[]; we required it == host.
  return { sub, aud: p.host, site_id: siteId, mode: mode as EdgeMode, iat };
}
