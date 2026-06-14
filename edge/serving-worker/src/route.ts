// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Route resolution + R2 path resolution — the pure logic of the serving Worker.
// Everything here is side-effect-light and unit-testable without a live edge
// (see test/serve.test.ts): KV/R2 are passed in as minimal interfaces.

// The KV route value is the one cross-language contract. Prefer the shared
// `@shipped/contracts` package; until the infra agent publishes it, fall back
// to the local mirror in ./types. The shapes are identical by construction
// (the contract round-trip test in CI enforces it).
// TODO(contracts): switch this import to "@shipped/contracts" once resolvable
// and delete ./types. The rest of this module is unaffected.
import {
  type AccessMode,
  type RouteValue,
  SUPPORTED_SCHEMA_VERSION,
} from "./types";

export { type AccessMode, type RouteValue, SUPPORTED_SCHEMA_VERSION };

/**
 * The KV key under which the Go API publishes a host's route value.
 * One key per content host (e.g. `route:acme.shippedusercontent.com`).
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
 * a malformed projection. The Worker pins `schema_version`.
 */
export function parseRouteValue(raw: unknown): RouteValue | null {
  if (raw === null || typeof raw !== "object") return null;
  const v = raw as Record<string, unknown>;

  if (typeof v.org_id !== "string" || v.org_id.length === 0) return null;
  if (typeof v.site_id !== "string" || v.site_id.length === 0) return null;
  if (typeof v.version_id !== "string" || v.version_id.length === 0) return null;
  if (!isAccessMode(v.access_mode)) return null;
  if (v.schema_version !== SUPPORTED_SCHEMA_VERSION) return null;

  return {
    org_id: v.org_id,
    site_id: v.site_id,
    version_id: v.version_id,
    access_mode: v.access_mode,
    schema_version: v.schema_version,
  };
}

function isAccessMode(x: unknown): x is AccessMode {
  return (
    x === "public" ||
    x === "password" ||
    x === "allowlist" ||
    x === "org_only"
  );
}

/**
 * The immutable manifest prefix for a published version. All of a version's
 * assets live under this prefix in the single private R2 bucket, namespaced by
 * org → site → version (so a version is one immutable origin; see §3/§10).
 *
 *   sites/${org_id}/${site_id}/${version_id}/
 */
export function versionPrefix(route: RouteValue): string {
  return `sites/${route.org_id}/${route.site_id}/${route.version_id}/`;
}

/**
 * Resolve a request URL path to the R2 object key under a version's prefix.
 *
 * Rules (static-site semantics, mirroring Quick's no-build ethos):
 *  - Normalize the path: decode, collapse to a POSIX-clean relative path,
 *    and reject traversal (`..`) so a request can never escape the version
 *    prefix into another org/site/version. On any unsafe path, return null
 *    → the caller serves the 404 page.
 *  - A directory-style path (empty, or ending in `/`) maps to its
 *    `index.html` (directory index fallback).
 *  - An extension-less path also gets an `index.html` directory fallback
 *    candidate AND a `.html` candidate, so `/about` resolves to either
 *    `about/index.html` or `about.html` — the caller tries them in order.
 *
 * Returns the ordered list of candidate keys to attempt against R2; the first
 * that exists wins. Empty array means "unsafe path → 404".
 */
export function resolveObjectKeys(route: RouteValue, rawPath: string): string[] {
  const prefix = versionPrefix(route);
  const clean = cleanPath(rawPath);
  if (clean === null) return [];

  // Directory request (root or trailing slash) → index.html only.
  if (clean === "" || clean.endsWith("/")) {
    return [`${prefix}${clean}index.html`];
  }

  const candidates: string[] = [`${prefix}${clean}`];

  // Extension-less "pretty" path → also try directory index and `.html`.
  if (!hasExtension(clean)) {
    candidates.push(`${prefix}${clean}/index.html`);
    candidates.push(`${prefix}${clean}.html`);
  }

  return candidates;
}

/** The R2 key for a version's custom 404 page, if the site ships one. */
export function notFoundKey(route: RouteValue): string {
  return `${versionPrefix(route)}404.html`;
}

/**
 * Decode + sanitize a URL path into a clean, prefix-relative key segment.
 * Returns null if the path is unsafe (traversal, bad encoding, or absolute
 * escape). The leading slash is stripped; the result never starts with `/`.
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

/** True if the final path segment carries a file extension (e.g. `.css`). */
function hasExtension(cleanRelPath: string): boolean {
  const last = cleanRelPath.split("/").pop() ?? "";
  const dot = last.lastIndexOf(".");
  return dot > 0 && dot < last.length - 1;
}
