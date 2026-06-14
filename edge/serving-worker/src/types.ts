// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Local mirror of the one genuine cross-language data contract: the KV route
// value shape. Per docs/ARCHITECTURE.md §3/§8 this is owned by `contracts/`
// (JSON Schema → Go struct + TS type with a CI round-trip test) and is the
// ONLY writer→reader contract between the Go API (the sole KV writer) and this
// Worker (a read-only consumer).
//
// TODO(contracts): once the infra agent publishes `@shipped/contracts`, delete
// this file and import { RouteValue, AccessMode } from "@shipped/contracts".
// `src/route.ts` already imports from "@shipped/contracts" with a fallback to
// this mirror, so switching over is a one-line change there.

/**
 * Access modes a site may be served under. Phase 1 implements ONLY `public`;
 * the identity-gated modes are Phase-2 stubs (see docs/ARCHITECTURE.md §6).
 */
export type AccessMode = "public" | "password" | "allowlist" | "org_only";

/**
 * The value stored at KV key `route:${host}`. Written exclusively by the Go API
 * on publish; fully rebuildable from Postgres (carry `schema_version` so the
 * Worker can refuse shapes it does not understand).
 *
 * Wire shape (snake_case — it crosses into Go):
 *   { org_id, site_id, version_id, access_mode, schema_version }
 */
export interface RouteValue {
  /** Owning organization (also the R2 per-org prefix). */
  org_id: string;
  /** Site identifier within the org. */
  site_id: string;
  /** Immutable, content-addressed deploy version currently published. */
  version_id: string;
  /** How this host is served. */
  access_mode: AccessMode;
  /** Contract version of this KV value; the Worker pins what it accepts. */
  schema_version: number;
}

/** The route-value schema version this Worker understands. */
export const SUPPORTED_SCHEMA_VERSION = 1 as const;
