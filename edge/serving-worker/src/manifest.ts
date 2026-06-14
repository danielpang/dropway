// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Deploy-manifest model + path resolution — the pure logic that turns a request
// path into a content-addressed blob key. Side-effect-light and unit-testable
// without a live edge (KV/R2 are injected as minimal interfaces in index.ts).
//
// Object layout (content-addressed, dedup — docs/ARCHITECTURE.md §3):
//   - per-deploy manifest JSON at  manifests/<org_id>/<site_id>/<version_id>.json
//     mapping each served request-path → { sha256, content_type }.
//   - blobs at                      blobs/<org_id>/<sha256>
// Serving resolves path → sha256 via the manifest, then streams the blob. A
// version is one immutable manifest; the blobs it references are shared across
// deploys (and orgs are isolated by the per-org prefix).

import type { RouteValue } from "./route";

/**
 * One manifest entry: the content-addressed identity of a served file plus the
 * Content-Type the Go API recorded at publish time (authoritative — we do NOT
 * re-sniff untrusted tenant bytes at the edge).
 */
export interface ManifestEntry {
  /** Lowercase hex SHA-256 of the blob (also its R2 key suffix). */
  sha256: string;
  /** Content-Type recorded at publish; served verbatim. */
  content_type: string;
  /** Decoded byte length, when the Go API records it (optional). */
  size?: number;
}

/**
 * A published deploy manifest. `files` maps a normalized, prefix-relative
 * request path (no leading slash) to its entry — e.g. "index.html",
 * "assets/app.4f3a9c2b.js", "blog/index.html". Carries `schema_version` so the
 * Worker can refuse a shape it does not understand (mirrors the KV contract).
 */
export interface Manifest {
  /** Contract version of this manifest shape; the Worker pins what it accepts. */
  schema_version: number;
  /** Normalized request path → content-addressed entry. */
  files: Record<string, ManifestEntry>;
}

/** The manifest-shape version this Worker understands. */
export const SUPPORTED_MANIFEST_SCHEMA_VERSION = 1 as const;

/** Lowercase-hex SHA-256, exactly 64 chars (R2 blob-key safety). */
const SHA256_RE = /^[0-9a-f]{64}$/;

/**
 * The R2 key of a version's deploy manifest. One immutable JSON per published
 * version, namespaced org → site → version.
 *
 *   manifests/<org_id>/<site_id>/<version_id>.json
 */
export function manifestKey(route: RouteValue): string {
  return `manifests/${route.org_id}/${route.site_id}/${route.version_id}.json`;
}

/**
 * The R2 key of a content-addressed blob for the route's org. Blobs are shared
 * (dedup) within an org; the org prefix is the isolation boundary.
 *
 *   blobs/<org_id>/<sha256>
 */
export function blobKey(orgId: string, sha256: string): string {
  return `blobs/${orgId}/${sha256}`;
}

/**
 * Validate an untrusted manifest JSON into a typed `Manifest`. Returns null on
 * any shape/version mismatch (or a malformed entry) so callers fail closed
 * (404) rather than serving from a corrupt projection. The Worker pins
 * `schema_version` exactly.
 */
export function parseManifest(raw: unknown): Manifest | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const v = raw as Record<string, unknown>;

  if (v.schema_version !== SUPPORTED_MANIFEST_SCHEMA_VERSION) return null;
  if (v.files === null || typeof v.files !== "object" || Array.isArray(v.files)) {
    return null;
  }

  const files: Record<string, ManifestEntry> = {};
  for (const [path, rawEntry] of Object.entries(v.files as Record<string, unknown>)) {
    const entry = parseEntry(rawEntry);
    if (entry === null) return null; // any bad entry → reject the whole manifest
    files[path] = entry;
  }

  return { schema_version: v.schema_version, files };
}

function parseEntry(raw: unknown): ManifestEntry | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const e = raw as Record<string, unknown>;

  if (typeof e.sha256 !== "string" || !SHA256_RE.test(e.sha256)) return null;
  if (typeof e.content_type !== "string" || e.content_type.length === 0) return null;

  const entry: ManifestEntry = { sha256: e.sha256, content_type: e.content_type };
  if (typeof e.size === "number" && Number.isFinite(e.size) && e.size >= 0) {
    entry.size = e.size;
  }
  return entry;
}

/**
 * Resolve a (already cleaned, prefix-relative) request path to its manifest
 * entry, applying static-site fallbacks IN ORDER:
 *
 *  - Directory request (empty path, or trailing "/") → "<path>index.html".
 *  - Exact match → that entry.
 *  - Extension-less "pretty" path "/about" → "about/index.html", then "about.html".
 *
 * The matched key is returned alongside its entry so the caller can apply the
 * Cache-Control policy by the served path's extension (HTML short, hashed
 * assets immutable). Returns null when nothing in the manifest matches.
 */
export function resolveManifestEntry(
  manifest: Manifest,
  cleanRelPath: string,
): { path: string; entry: ManifestEntry } | null {
  for (const candidate of candidatePaths(cleanRelPath)) {
    const entry = manifest.files[candidate];
    if (entry !== undefined) return { path: candidate, entry };
  }
  return null;
}

/**
 * Ordered manifest-path candidates for a cleaned request path. Mirrors the
 * directory-index + pretty-URL semantics of the old key resolution, but now
 * resolved against the manifest's path set rather than probed against R2.
 */
export function candidatePaths(cleanRelPath: string): string[] {
  // Directory request (root or trailing slash) → index.html only.
  if (cleanRelPath === "" || cleanRelPath.endsWith("/")) {
    return [`${cleanRelPath}index.html`];
  }

  const candidates: string[] = [cleanRelPath];

  // Extension-less "pretty" path → also try directory index and `.html`.
  if (!hasExtension(cleanRelPath)) {
    candidates.push(`${cleanRelPath}/index.html`);
    candidates.push(`${cleanRelPath}.html`);
  }

  return candidates;
}

/** The manifest path for a version's custom 404 page, if it ships one. */
export const NOT_FOUND_PATH = "404.html";

/** True if the final path segment carries a file extension (e.g. `.css`). */
function hasExtension(cleanRelPath: string): boolean {
  const last = cleanRelPath.split("/").pop() ?? "";
  const dot = last.lastIndexOf(".");
  return dot > 0 && dot < last.length - 1;
}
