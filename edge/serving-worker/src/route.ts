// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Host → route resolution + request-path sanitization — the pure logic of the
// serving Worker. Everything here is side-effect-light and unit-testable
// without a live edge (see test/serve.test.ts): KV/R2 are injected as minimal
// interfaces in index.ts.
//
// Path → object-key resolution now lives in ./manifest: under the
// content-addressed layout the Worker resolves a request path to a sha256 via
// the deploy manifest (manifests/<org>/<site>/<version>.json), then streams the
// blob (blobs/<org>/<sha256>). This module only normalizes the host and the
// request path; the manifest does the rest.

// The KV route value is the one cross-language data contract. It is owned by the
// shared `@dropway/contracts` package (JSON Schema → Go struct + TS type with a
// CI round-trip test) and is the ONLY writer→reader contract between the Go API
// (the sole KV writer) and this Worker (a read-only consumer). We re-export it
// under the Worker's local vocabulary (`RouteValue`/`SUPPORTED_SCHEMA_VERSION`)
// so the rest of the Worker and its tests read naturally.
import {
  type AccessMode,
  type KVRouteValue,
  KVRouteValidationError,
  MIN_SCHEMA_VERSION,
  SCHEMA_VERSION,
  isRouteExpired as contractsIsRouteExpired,
  parseKVRouteValue,
  safeParseKVRouteValue,
} from "@dropway/contracts";

/** The KV route value, named locally for the Worker. */
export type RouteValue = KVRouteValue;
/** The newest route-value schema version this Worker understands. */
export const SUPPORTED_SCHEMA_VERSION = SCHEMA_VERSION;
/** The oldest route-value schema version this Worker still accepts (back-compat). */
export const MIN_SUPPORTED_SCHEMA_VERSION = MIN_SCHEMA_VERSION;
export { type AccessMode };

/**
 * True when a parsed route has expired as of `now` (default: current time). A
 * route with no `expires_at` (v1 values, or non-expiring v2 links) never expires.
 * Re-exported from the shared contract so the Worker enforces the SAME edge
 * expiry semantics the Go API serializes (public/unlisted link expiry).
 */
export function isRouteExpired(value: RouteValue, now?: Date): boolean {
  return contractsIsRouteExpired(value, now);
}

/**
 * The KV key under which the Go API publishes a host's route value.
 * One key per content host (e.g. `route:acme.dropwaycontent.com`).
 */
export function routeKey(host: string): string {
  return `route:${normalizeHost(host)}`;
}

/**
 * Normalize a request Host header into a stable KV lookup key:
 *  - strip any `:port` suffix (dev / non-443 origins),
 *  - lowercase (hostnames are case-insensitive),
 *  - drop a single trailing dot (FQDN root) and surrounding whitespace.
 */
export function normalizeHost(rawHost: string): string {
  let host = rawHost.trim().toLowerCase();
  // Strip port. IPv6 literals are not valid content hosts here, so a plain
  // split on the last colon is safe for the hostnames we serve.
  const colon = host.lastIndexOf(":");
  if (colon !== -1) host = host.slice(0, colon);
  if (host.endsWith(".")) host = host.slice(0, -1);
  return host;
}

/**
 * Validate an untrusted KV value into a typed RouteValue. Returns null on any
 * shape/version mismatch so callers fail closed (404) rather than serving from
 * a malformed projection. Delegates to the shared contract validator, which
 * also pins `schema_version` and rejects non-UUID identifiers / unknown fields.
 */
export function parseRouteValue(raw: unknown): RouteValue | null {
  return safeParseKVRouteValue(raw);
}

/** Why a NON-NULL KV route value failed validation — for observability only. */
export interface RouteParseFailure {
  /** Human-/machine-readable reason from the contract validator. */
  reason: string;
  /** The value's `schema_version` when it is a readable finite number. */
  schemaVersion?: number;
  /**
   * True when `schema_version` is NEWER than this Worker accepts — the deploy-skew
   * signal that the Go API writer was rolled out AHEAD of this reader, so every
   * freshly published site is projected at a version this Worker rejects.
   */
  schemaTooNew: boolean;
}

/**
 * Explain why a NON-NULL KV route value failed to parse. A `null` raw is an
 * ordinary unknown-host miss (not drift), so callers MUST only invoke this when
 * `raw !== null`. For error reporting only — it re-runs the throwing validator to
 * recover the failure message, which only happens on the (rare) rejection path,
 * never on the hot success path.
 */
export function diagnoseRouteParseFailure(raw: unknown): RouteParseFailure {
  let reason = "route value failed validation";
  try {
    parseKVRouteValue(raw);
    // Parsed on retry — non-deterministic input; treat as transient.
    reason = "route value parsed on retry (transient)";
  } catch (err) {
    if (err instanceof KVRouteValidationError) reason = err.message;
  }

  let schemaVersion: number | undefined;
  if (typeof raw === "object" && raw !== null && !Array.isArray(raw)) {
    const sv = (raw as Record<string, unknown>).schema_version;
    if (typeof sv === "number" && Number.isFinite(sv)) schemaVersion = sv;
  }

  return {
    reason,
    schemaVersion,
    schemaTooNew:
      schemaVersion !== undefined && schemaVersion > SUPPORTED_SCHEMA_VERSION,
  };
}

/**
 * Decode + sanitize a URL path into a clean, prefix-relative key segment that
 * is also a manifest-lookup key. Returns null if the path is unsafe (traversal,
 * bad encoding, or absolute escape). The leading slash is stripped; the result
 * never starts with `/`. Even though manifest resolution can only match keys
 * the Go API published (so traversal cannot escape an org/site), we still reject
 * unsafe paths so a malformed request can never produce a surprising key.
 */
export function cleanPath(rawPath: string): string | null {
  let path = rawPath;

  // Strip query/hash defensively (callers pass URL.pathname, but be safe).
  const q = path.search(/[?#]/);
  if (q !== -1) path = path.slice(0, q);

  // Decode percent-encoding once; reject malformed encodings.
  let decoded: string;
  try {
    decoded = decodeURIComponent(path);
  } catch {
    return null;
  }

  // A decoded NUL or backslash is never legitimate in a static asset path.
  if (decoded.includes("\0") || decoded.includes("\\")) return null;

  // Strip the leading slash; remember whether the request ended in a slash.
  const endedWithSlash = decoded.length > 1 && decoded.endsWith("/");
  let rel = decoded.replace(/^\/+/, "");

  // Resolve `.` / `..` segments without ever escaping the root.
  const out: string[] = [];
  for (const seg of rel.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      // Traversal above the version prefix is forbidden — fail closed.
      return null;
    }
    out.push(seg);
  }

  rel = out.join("/");
  if (endedWithSlash && rel !== "") rel += "/";
  return rel;
}
