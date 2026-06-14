// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// HTTP response concerns for the public serve path: Content-Type derivation and
// Cache-Control policy. Pure functions, unit-tested in test/serve.test.ts.
//
// Cache policy (docs/ARCHITECTURE.md §6, "Cache API for public only"):
//  - Content-addressed / hashed assets are immutable → cache hard + immutable.
//  - HTML (and other non-hashed entry docs) get a SHORT TTL so a pointer flip
//    (publish/rollback) is visible quickly while still cutting Class-B R2 ops.
// Every public response also carries content-security hardening headers; the
// public path NEVER sets `private`/`no-store` (that is reserved for gated tiers
// in Phase 2 — the cache-key-isolation invariant in §10).
//
// The security headers themselves (CSP/COOP/CORP/nosniff/no-referrer/frame +
// service-worker block) live in ./security and are shared with the platform
// pages; `securityHeaders()` here re-exports the CONTENT set so existing call
// sites keep their import surface.

import { contentSecurityHeaders } from "./security";

const MIME: Record<string, string> = {
  // Documents
  html: "text/html; charset=utf-8",
  htm: "text/html; charset=utf-8",
  xml: "application/xml; charset=utf-8",
  txt: "text/plain; charset=utf-8",
  md: "text/markdown; charset=utf-8",
  // Styles / scripts
  css: "text/css; charset=utf-8",
  js: "text/javascript; charset=utf-8",
  mjs: "text/javascript; charset=utf-8",
  map: "application/json; charset=utf-8",
  json: "application/json; charset=utf-8",
  // Images
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  svg: "image/svg+xml",
  ico: "image/x-icon",
  // Fonts
  woff: "font/woff",
  woff2: "font/woff2",
  ttf: "font/ttf",
  otf: "font/otf",
  eot: "application/vnd.ms-fontobject",
  // Media / misc
  wasm: "application/wasm",
  pdf: "application/pdf",
  webmanifest: "application/manifest+json",
};

const DEFAULT_CONTENT_TYPE = "application/octet-stream";

/** Lowercased file extension of a key/path, or "" if none. */
export function extensionOf(key: string): string {
  const last = key.split("/").pop() ?? "";
  const dot = last.lastIndexOf(".");
  if (dot <= 0 || dot === last.length - 1) return "";
  return last.slice(dot + 1).toLowerCase();
}

/** Map an object key to a Content-Type, defaulting to octet-stream. */
export function contentTypeFor(key: string): string {
  return MIME[extensionOf(key)] ?? DEFAULT_CONTENT_TYPE;
}

/** HTML is the short-TTL "entry document" class; everything else is an asset. */
export function isHtml(key: string): boolean {
  const ext = extensionOf(key);
  return ext === "html" || ext === "htm";
}

/**
 * Heuristic: does this asset name look content-hash-fingerprinted, so it is
 * safe to cache immutably forever? Matches the common bundler patterns:
 *   app.4f3a9c2b.js · main-9Hs2Kd.css · chunk.abcdef0123456789.mjs
 * i.e. a >=8-char hex/base62 token delimited by `.` or `-` before the ext.
 */
export function isHashedAsset(key: string): boolean {
  if (isHtml(key)) return false; // entry docs are never treated as immutable
  const last = key.split("/").pop() ?? "";
  return /[.\-_][0-9a-zA-Z]{8,}\.[0-9a-zA-Z]+$/.test(last);
}

/** Short TTL (seconds) for HTML and non-fingerprinted assets. */
export const SHORT_TTL_SECONDS = 60;

/**
 * Cache-Control for a public asset, keyed off the SERVED request path (the
 * manifest key, e.g. "assets/app.4f3a9c2b.js" or "index.html"):
 *  - hashed/immutable asset → 1 year, immutable.
 *  - HTML / non-hashed asset → short TTL, revalidatable.
 *
 * Keying off the served path (not the content-addressed blob key) is what lets
 * a pointer flip (publish/rollback) become visible quickly for HTML while
 * fingerprinted assets stay cacheable forever.
 */
export function cacheControlFor(servedPath: string): string {
  if (isHashedAsset(servedPath)) {
    return "public, max-age=31536000, immutable";
  }
  return `public, max-age=${SHORT_TTL_SECONDS}, must-revalidate`;
}

/**
 * Security headers applied to every served TENANT content response (public +
 * gated). Delegates to ./security so the CSP/COOP/CORP/nosniff/no-referrer/frame
 * + service-worker-block policy is defined in exactly one place and shared with
 * the platform pages. CSP is explicitly NOT the isolation control here
 * (domain/PSL separation is — §10); these are defense-in-depth.
 *
 * Kept as a named export with the same signature so existing call sites
 * (index.ts, authz.ts) and tests need no change.
 */
export function securityHeaders(): Record<string, string> {
  return contentSecurityHeaders();
}

/**
 * Build the full header set for a successful public object response.
 *
 *  - `servedPath` is the manifest key (request path) — it drives Cache-Control.
 *  - `contentType` is the authoritative type the Go API recorded in the
 *    manifest at publish; we serve it verbatim and do NOT re-sniff untrusted
 *    tenant bytes. (If absent we fall back to extension-derived typing.)
 *  - `etag`/`lastModified`/`contentLength` come from the R2 blob when available.
 */
export function publicResponseHeaders(
  servedPath: string,
  opts: {
    contentType?: string;
    etag?: string;
    lastModified?: Date;
    contentLength?: number;
  } = {},
): Headers {
  const h = new Headers();
  h.set("Content-Type", opts.contentType ?? contentTypeFor(servedPath));
  h.set("Cache-Control", cacheControlFor(servedPath));
  for (const [k, v] of Object.entries(securityHeaders())) {
    if (v !== "") h.set(k, v);
  }
  if (opts.etag) h.set("ETag", opts.etag);
  if (opts.lastModified) h.set("Last-Modified", opts.lastModified.toUTCString());
  if (typeof opts.contentLength === "number") {
    h.set("Content-Length", String(opts.contentLength));
  }
  return h;
}
