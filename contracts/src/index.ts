// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// @shipped/contracts — the single cross-language data contract.
//
// The KV route value is written ONLY by the Go API (on publish) and read by the
// Cloudflare serving Worker. This module is the TypeScript side of that contract;
// the authoritative shape lives in `kv-route.schema.json` and the Go side mirrors
// it in services/api. A CI round-trip test asserts the three stay in lock-step
// (ARCHITECTURE.md §8, §13 row 11).
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
 * Current version of the KV route contract shape. MUST equal `schema_version` on
 * every value the Go API writes and the Worker reads. Bump on any breaking change
 * to `KVRouteValue` / kv-route.schema.json, and update the Go constant in tandem.
 */
export const SCHEMA_VERSION = 1 as const;

/**
 * The value stored at KV key `route:<host>`. Keep field names and types in exact
 * sync with kv-route.schema.json and the Go struct (json tags) in services/api.
 */
export interface KVRouteValue {
  /** Owning org (auth.organization.id / app.org_meta.id). */
  org_id: string;
  /** Target site (app.sites.id). */
  site_id: string;
  /** Live immutable version to serve (app.site_versions.id). */
  version_id: string;
  /** How the Worker gates this host. */
  access_mode: AccessMode;
  /** Version of this contract shape; see SCHEMA_VERSION. */
  schema_version: number;
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

  if (obj.schema_version !== SCHEMA_VERSION) {
    throw new KVRouteValidationError(
      `unsupported schema_version ${obj.schema_version}; this build expects ${SCHEMA_VERSION}`,
    );
  }

  return {
    org_id: obj.org_id as string,
    site_id: obj.site_id as string,
    version_id: obj.version_id as string,
    access_mode: obj.access_mode,
    schema_version: obj.schema_version,
  };
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
  return JSON.stringify({
    org_id: validated.org_id,
    site_id: validated.site_id,
    version_id: validated.version_id,
    access_mode: validated.access_mode,
    schema_version: validated.schema_version,
  });
}
