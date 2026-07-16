// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// @dropway/contracts — the single cross-language data contract.
//
// The KV route value is written ONLY by the Go API (on publish) and read by the
// Cloudflare serving Worker. This module is the TypeScript side of that contract;
// the authoritative shape lives in `kv-route.schema.json` and the Go side mirrors
// it in services/api. A CI round-trip test asserts the three stay in lock-step.
//
// Deliberately dependency-free: the validator is hand-written so the Worker (which
// runs on the Cloudflare runtime and wants a tiny bundle) and the Go-side test
// harness can both rely on it without pulling in a JSON-Schema engine.

/**
 * Access mode for a served host. Phase 1 implements `public` only; `password`,
 * `allowlist` and `org_only` are Phase 2. Mirrors app.sites.access_mode and the
 * `enum` in kv-route.schema.json.
 */
export const ACCESS_MODES = ["public", "password", "allowlist", "org_only"] as const;

export type AccessMode = (typeof ACCESS_MODES)[number];

/**
 * Current version of the KV route contract shape. MUST equal the `schema_version`
 * the Go API WRITES on every value (the Worker reads it back). Bump on any change
 * to `KVRouteValue` / kv-route.schema.json, and update the Go constant in tandem.
 *
 * v1 → v2 (Phase 2): added the optional `expires_at` field.
 * v2 → v3: added the optional `plan_tier` field (the owning org's plan, used to
 * gate the free-tier "Deployed with Dropway" attribution banner).
 * v3 → v4: added the optional `chat_id` field — the id of the site's attached,
 * panel-enabled chat log (Share This Session). When present, the Worker injects
 * the "How this was made" pill into served HTML and serves the transcript page
 * at the reserved /__dropway/chat path. The parser stays backward compatible —
 * it accepts any version in [MIN_SCHEMA_VERSION, SCHEMA_VERSION] — so a stored
 * v1 value (no expires_at) is read as "never expires", a v2 value (no
 * plan_tier) as "tier unknown", and a v3 value (no chat_id) as "no chat
 * surface"; the Go API only ever writes SCHEMA_VERSION.
 */
export const SCHEMA_VERSION = 4 as const;

/**
 * The oldest contract shape the parser still accepts. A v1 value carries no
 * `expires_at` (treated as non-expiring); a v2 value carries no `plan_tier`
 * (treated as tier unknown); a v3 value carries no `chat_id` (no chat surface).
 */
export const MIN_SCHEMA_VERSION = 1 as const;

/**
 * The value stored at KV key `route:<host>`. Keep field names and types in exact
 * sync with kv-route.schema.json and the Go struct (json tags) in services/api.
 */
export interface KVRouteValue {
  /** Owning org (identity.organization.id / app.org_meta.id). */
  org_id: string;
  /** Target site (app.sites.id). */
  site_id: string;
  /** Live immutable version to serve (app.site_versions.id). */
  version_id: string;
  /** How the Worker gates this host. */
  access_mode: AccessMode;
  /** Version of this contract shape; see SCHEMA_VERSION. */
  schema_version: number;
  /**
   * OPTIONAL (v2+). RFC3339 timestamp after which the Worker must refuse to serve
   * this host (public/unlisted link expiry, enforced at the edge). Absent → no
   * edge expiry. Identity-gated expiry is refused at mint time in the Go API.
   */
  expires_at?: string;
  /**
   * OPTIONAL (v3+). The owning org's plan tier (app.org_meta.plan_tier, e.g.
   * "free"/"pro"). The Worker uses it to gate the free-tier "Deployed with
   * Dropway" attribution banner. Absent → tier unknown → no banner.
   */
  plan_tier?: string;
  /**
   * OPTIONAL (v4+). The id (UUID) of the site's attached, panel-enabled chat
   * log (Share This Session). When present, the Worker injects the "How this
   * was made" pill into served HTML and serves the compiled transcript at the
   * reserved /__dropway/chat path (from chat-transcripts/<org_id>/<chat_id>.json
   * in the content bucket). Absent → no chat surface.
   */
  chat_id?: string;
}

const UUID_RE =
  /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

/** Type guard for the access_mode enum. */
export function isAccessMode(value: unknown): value is AccessMode {
  return typeof value === "string" && (ACCESS_MODES as readonly string[]).includes(value);
}

/** A validation failure with a stable, machine-readable reason. */
export class KVRouteValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "KVRouteValidationError";
  }
}

/**
 * Validate an arbitrary value against the KV route contract and return it as a
 * well-typed `KVRouteValue`. Throws `KVRouteValidationError` on any mismatch:
 * unknown/missing fields, bad UUIDs, bad enum, or an unsupported schema_version.
 *
 * The Worker calls this on every KV read so a malformed/stale projection fails
 * closed rather than serving the wrong bytes.
 */
export function parseKVRouteValue(input: unknown): KVRouteValue {
  if (typeof input !== "object" || input === null || Array.isArray(input)) {
    throw new KVRouteValidationError("kv route value must be a JSON object");
  }

  const obj = input as Record<string, unknown>;

  // Reject unknown keys (mirrors "additionalProperties": false) so drift surfaces.
  const allowed = new Set([
    "org_id",
    "site_id",
    "version_id",
    "access_mode",
    "schema_version",
    "expires_at",
    "plan_tier",
    "chat_id",
  ]);
  for (const key of Object.keys(obj)) {
    if (!allowed.has(key)) {
      throw new KVRouteValidationError(`unexpected field: ${key}`);
    }
  }

  for (const key of ["org_id", "site_id", "version_id"] as const) {
    const v = obj[key];
    if (typeof v !== "string" || !UUID_RE.test(v)) {
      throw new KVRouteValidationError(`${key} must be a UUID string`);
    }
  }

  if (!isAccessMode(obj.access_mode)) {
    throw new KVRouteValidationError(
      `access_mode must be one of ${ACCESS_MODES.join(", ")}`,
    );
  }

  if (
    typeof obj.schema_version !== "number" ||
    !Number.isInteger(obj.schema_version) ||
    obj.schema_version < 1
  ) {
    throw new KVRouteValidationError("schema_version must be a positive integer");
  }

  // Accept any version in [MIN_SCHEMA_VERSION, SCHEMA_VERSION] (backward-compatible
  // parse): a stored v1 value lacks expires_at and is read as non-expiring.
  if (
    obj.schema_version < MIN_SCHEMA_VERSION ||
    obj.schema_version > SCHEMA_VERSION
  ) {
    throw new KVRouteValidationError(
      `unsupported schema_version ${obj.schema_version}; this build accepts ${MIN_SCHEMA_VERSION}..${SCHEMA_VERSION}`,
    );
  }

  // expires_at is optional (v2+). When present it must be a valid RFC3339/ISO-8601
  // timestamp the Worker can compare against now() to enforce edge expiry.
  let expiresAt: string | undefined;
  if (obj.expires_at !== undefined && obj.expires_at !== null) {
    if (
      typeof obj.expires_at !== "string" ||
      Number.isNaN(Date.parse(obj.expires_at))
    ) {
      throw new KVRouteValidationError("expires_at must be an RFC3339 timestamp");
    }
    expiresAt = obj.expires_at;
  }

  // plan_tier is optional (v3+). When present it must be a non-empty string (the
  // org's plan tier, e.g. "free"/"pro"); an empty string is treated as absent.
  let planTier: string | undefined;
  if (obj.plan_tier !== undefined && obj.plan_tier !== null) {
    if (typeof obj.plan_tier !== "string") {
      throw new KVRouteValidationError("plan_tier must be a string");
    }
    if (obj.plan_tier !== "") {
      planTier = obj.plan_tier;
    }
  }

  // chat_id is optional (v4+). Unlike plan_tier (a free-form tier string) it is
  // an identifier used to build an R2 object key, so — like org_id — it must be
  // a UUID string when present; anything else is rejected (fail closed rather
  // than let a malformed projection shape a bucket key).
  let chatId: string | undefined;
  if (obj.chat_id !== undefined && obj.chat_id !== null) {
    if (typeof obj.chat_id !== "string" || !UUID_RE.test(obj.chat_id)) {
      throw new KVRouteValidationError("chat_id must be a UUID string");
    }
    chatId = obj.chat_id;
  }

  const out: KVRouteValue = {
    org_id: obj.org_id as string,
    site_id: obj.site_id as string,
    version_id: obj.version_id as string,
    access_mode: obj.access_mode,
    schema_version: obj.schema_version,
  };
  if (expiresAt !== undefined) {
    out.expires_at = expiresAt;
  }
  if (planTier !== undefined) {
    out.plan_tier = planTier;
  }
  if (chatId !== undefined) {
    out.chat_id = chatId;
  }
  return out;
}

/**
 * Reports whether a parsed route has expired as of `now` (default: the current
 * time). A route with no `expires_at` never expires. The Worker calls this to
 * fail closed on an expired public/unlisted link.
 */
export function isRouteExpired(value: KVRouteValue, now: Date = new Date()): boolean {
  if (!value.expires_at) {
    return false;
  }
  const exp = Date.parse(value.expires_at);
  if (Number.isNaN(exp)) {
    // A malformed timestamp that somehow slipped past parse → fail closed.
    return true;
  }
  return now.getTime() >= exp;
}

/**
 * Non-throwing variant: returns the typed value or `null` on any validation
 * failure. Convenient at the edge where a miss/typed-null is handled the same.
 */
export function safeParseKVRouteValue(input: unknown): KVRouteValue | null {
  try {
    return parseKVRouteValue(input);
  } catch {
    return null;
  }
}

/**
 * Serialize a value to the canonical JSON string stored in KV. Validates first so
 * an invalid value can never be written. (The Go API is the real writer; this
 * exists for tests and any TS-side tooling.)
 */
export function serializeKVRouteValue(value: KVRouteValue): string {
  const validated = parseKVRouteValue(value);
  // Stable key order matches the schema for byte-for-byte round-trip tests.
  // expires_at is omitted entirely when absent (mirrors the Go `omitempty` tag),
  // so a non-expiring route serializes identically across v1↔v2.
  const out: Record<string, unknown> = {
    org_id: validated.org_id,
    site_id: validated.site_id,
    version_id: validated.version_id,
    access_mode: validated.access_mode,
    schema_version: validated.schema_version,
  };
  if (validated.expires_at !== undefined) {
    out.expires_at = validated.expires_at;
  }
  if (validated.plan_tier !== undefined) {
    out.plan_tier = validated.plan_tier;
  }
  if (validated.chat_id !== undefined) {
    out.chat_id = validated.chat_id;
  }
  return JSON.stringify(out);
}
