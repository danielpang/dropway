// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Autoindex (directory listing) for uploads that are NOT a static website — e.g.
// a folder of documents with no index.html. When a directory request matches no
// page in the manifest, the Worker synthesizes a listing of that directory's
// immediate children straight from the manifest's path set, so the files are
// still browsable (each links to the raw file served with its own Content-Type)
// instead of returning a 404.
//
// Pure logic: no KV/R2/Response. index.ts decides WHEN to call this (only after
// normal manifest resolution misses) and wraps the HTML in a 200 content
// response, so the listing flows through the same cache / banner / header path
// as any served HTML page and is gated behind auth on protected sites.

import type { Manifest } from "./manifest";

/** One row in a directory listing: a child file or subdirectory. */
export interface ListingEntry {
  /** Display name (a subdirectory carries a trailing slash, e.g. "assets/"). */
  name: string;
  /** Absolute, URL-encoded href to the file or subdirectory. */
  href: string;
  /** True for a subdirectory row, false for a file. */
  isDir: boolean;
  /** Decoded byte size for files, when the manifest records it. */
  size?: number;
}

/**
 * The directory prefix a cleaned request path targets, as a manifest-key prefix
 * (either "" for root or a string ending in "/"). A directory request ("" or a
 * trailing-slash path) maps to itself; an extension-less "pretty" path is also
 * treated as a candidate directory (e.g. "docs" → "docs/") so `/docs` can list
 * `docs/*` when no `docs/index.html` (or `docs.html`) page matched.
 */
export function directoryPrefix(cleanRelPath: string): string {
  if (cleanRelPath === "" || cleanRelPath.endsWith("/")) return cleanRelPath;
  return `${cleanRelPath}/`;
}

/**
 * The immediate children (subdirectories + files) of `dirPrefix` within the
 * manifest, or null when the prefix has NO descendants at all. Returning null is
 * what keeps a genuine typo a 404: only a directory that actually contains files
 * gets a listing. Subdirectories are collapsed to their first segment and
 * de-duplicated; rows are ordered directories-first, then files, alphabetically.
 */
export function listDirectory(
  manifest: Manifest,
  dirPrefix: string,
): ListingEntry[] | null {
  const dirs = new Set<string>();
  const files: ListingEntry[] = [];

  for (const [key, entry] of Object.entries(manifest.files)) {
    if (dirPrefix !== "" && !key.startsWith(dirPrefix)) continue;
    const rest = key.slice(dirPrefix.length);
    if (rest === "") continue; // a key equal to the prefix is not a child

    const slash = rest.indexOf("/");
    if (slash === -1) {
      files.push({
        name: rest,
        href: encodeHref(key),
        isDir: false,
        size: entry.size,
      });
    } else {
      dirs.add(rest.slice(0, slash));
    }
  }

  if (dirs.size === 0 && files.length === 0) return null;

  const dirRows: ListingEntry[] = [...dirs].sort().map((d) => ({
    name: `${d}/`,
    href: encodeHref(`${dirPrefix}${d}/`),
    isDir: true,
  }));
  files.sort((a, b) => a.name.localeCompare(b.name));

  return [...dirRows, ...files];
}

/**
 * Render a complete, self-contained HTML listing page. Inline styles only (the
 * content CSP allows `style-src 'unsafe-inline'`), no scripts, every tenant-
 * supplied name HTML-escaped. Links are absolute so they resolve correctly
 * whether or not the request carried a trailing slash.
 */
export function renderDirectoryListing(
  dirPrefix: string,
  entries: ListingEntry[],
): string {
  const display = `/${dirPrefix}`;
  const rows: string[] = [];

  if (dirPrefix !== "") {
    rows.push(
      `<tr><td><a href="${esc(parentHref(dirPrefix))}">Parent directory</a></td><td></td></tr>`,
    );
  }
  for (const e of entries) {
    const cls = e.isDir ? ' class="dir"' : "";
    const size = e.isDir ? "" : esc(formatSize(e.size));
    rows.push(
      `<tr><td><a${cls} href="${esc(e.href)}">${esc(e.name)}</a></td><td>${size}</td></tr>`,
    );
  }

  const count = entries.length;
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Index of ${esc(display)}</title>
<style>
:root { color-scheme: light dark; }
body { font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; margin: 0; padding: 2.5rem 1.25rem; background: #fafafa; color: #1a1a1a; }
main { max-width: 760px; margin: 0 auto; }
h1 { font-size: 1.05rem; font-weight: 600; margin: 0 0 1.25rem; word-break: break-all; }
table { width: 100%; border-collapse: collapse; }
th { text-align: left; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.04em; color: #888; font-weight: 600; padding: 0 0 0.5rem; border-bottom: 1px solid #e3e3e3; }
th:last-child, td:last-child { text-align: right; color: #888; font-variant-numeric: tabular-nums; white-space: nowrap; }
td { padding: 0.45rem 0; border-bottom: 1px solid #ededed; }
a { color: #1a56db; text-decoration: none; word-break: break-all; }
a:hover { text-decoration: underline; }
a.dir { font-weight: 600; }
footer { margin-top: 1.5rem; font-size: 0.8rem; color: #999; }
@media (prefers-color-scheme: dark) {
  body { background: #16181c; color: #e6e6e6; }
  th { color: #8a8f98; border-bottom-color: #2a2d34; }
  th:last-child, td:last-child { color: #8a8f98; }
  td { border-bottom-color: #23262c; }
  a { color: #6ea8ff; }
  footer { color: #6a6f78; }
}
</style>
</head>
<body>
<main>
<h1>Index of ${esc(display)}</h1>
<table>
<thead><tr><th>Name</th><th>Size</th></tr></thead>
<tbody>
${rows.join("\n")}
</tbody>
</table>
<footer>${count} ${count === 1 ? "item" : "items"}</footer>
</main>
</body>
</html>
`;
}

/** Absolute, per-segment URL-encoded href for a manifest key (preserves any trailing slash). */
function encodeHref(key: string): string {
  const trailing = key.endsWith("/");
  const encoded = key
    .split("/")
    .filter((s) => s !== "")
    .map(encodeURIComponent)
    .join("/");
  return `/${encoded}${trailing && encoded !== "" ? "/" : ""}`;
}

/** Absolute href to the parent of a directory prefix ("docs/sub/" → "/docs/"). */
function parentHref(dirPrefix: string): string {
  const trimmed = dirPrefix.replace(/\/$/, "");
  const idx = trimmed.lastIndexOf("/");
  return idx === -1 ? "/" : encodeHref(`${trimmed.slice(0, idx + 1)}`);
}

/** Human-readable byte size; empty string when the size is unknown. */
function formatSize(n?: number): string {
  if (n === undefined) return "";
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

/** Escape the five HTML-significant characters for safe interpolation. */
function esc(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
