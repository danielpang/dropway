/**
 * Pure, browser-side manifest logic for folder deploy — extracted from the deploy
 * orchestration so it can be unit-tested in isolation (no server-action imports).
 *
 * The whole-deploy digest here MUST stay byte-exact with Go's internal/manifest.Digest
 * (sort by path; sha256 over `<sha256>  <path>\n` lines — two spaces, trailing
 * newline), or the server rejects every finalize with a 400. See deploy-manifest.test.ts.
 */

import type { ManifestFile } from "@/lib/api";

/** A file discovered under the dropped folder, with its site-relative POSIX path. */
export interface DroppedFile {
  /** POSIX path served from the site root, e.g. "index.html", "assets/app.js". */
  path: string;
  file: File;
}

export interface BuiltManifest {
  manifest: ManifestFile[];
  digest: string;
  /** sha256 → one File carrying that content (for the upload step). */
  byHash: Map<string, File>;
}

// ---- Content-type guess (mirrors cli/internal/api/content_type.go intent) -----
// Only affects the Content-Type the deployed site is SERVED with — it's not part
// of the digest or size verification, so exact CLI parity isn't required; a sane
// table for web assets plus the browser's own guess (file.type) is enough.
const EXT_CONTENT_TYPE: Record<string, string> = {
  html: "text/html; charset=utf-8",
  htm: "text/html; charset=utf-8",
  css: "text/css; charset=utf-8",
  js: "text/javascript; charset=utf-8",
  mjs: "text/javascript; charset=utf-8",
  json: "application/json",
  map: "application/json",
  svg: "image/svg+xml",
  wasm: "application/wasm",
  webmanifest: "application/manifest+json",
  txt: "text/plain; charset=utf-8",
  xml: "application/xml",
  ico: "image/x-icon",
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  woff: "font/woff",
  woff2: "font/woff2",
  ttf: "font/ttf",
  otf: "font/otf",
  eot: "application/vnd.ms-fontobject",
  pdf: "application/pdf",
  mp4: "video/mp4",
  webm: "video/webm",
  mp3: "audio/mpeg",
  wav: "audio/wav",
};

export function contentTypeFor(path: string, fallback?: string): string {
  const dot = path.lastIndexOf(".");
  const ext = dot >= 0 ? path.slice(dot + 1).toLowerCase() : "";
  return EXT_CONTENT_TYPE[ext] ?? (fallback || "application/octet-stream");
}

// ---- Hashing + digest -----------------------------------------------------

export async function sha256Hex(data: BufferSource): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", data);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function byPath(a: { path: string }, b: { path: string }): number {
  return a.path < b.path ? -1 : a.path > b.path ? 1 : 0;
}

/**
 * Whole-deploy content address — byte-exact match to Go's internal/manifest.Digest:
 * sort by path, then sha256 over the concatenation of `<sha256>  <path>\n` lines
 * (EXACTLY two spaces, trailing newline). The server recomputes this and 400s on
 * mismatch, so it must be identical.
 */
export async function computeDigest(
  files: { path: string; sha256: string }[],
): Promise<string> {
  const lines = [...files]
    .sort(byPath)
    .map((f) => `${f.sha256}  ${f.path}\n`)
    .join("");
  return sha256Hex(new TextEncoder().encode(lines));
}

export async function buildManifest(
  files: DroppedFile[],
  onHash?: (done: number, total: number) => void,
): Promise<BuiltManifest> {
  const manifest: ManifestFile[] = [];
  const byHash = new Map<string, File>();
  let done = 0;
  for (const { path, file } of files) {
    const sha = await sha256Hex(await file.arrayBuffer());
    manifest.push({
      path,
      sha256: sha,
      size: file.size,
      content_type: contentTypeFor(path, file.type),
    });
    if (!byHash.has(sha)) byHash.set(sha, file);
    onHash?.(++done, files.length);
  }
  manifest.sort(byPath);
  return { manifest, digest: await computeDigest(manifest), byHash };
}

// ---- Folder collection (drag-drop tree + directory picker) ----------------

/**
 * Walk the items from a drop event. Uses webkitGetAsEntry to recurse the dropped
 * directory tree. When a SINGLE folder is dropped, its CONTENTS become the site
 * root (so dropping `mysite/` yields "index.html", not "mysite/index.html") —
 * matching `dropway deploy ./dist`, which walks inside the folder.
 */
export async function collectDataTransferItems(
  items: DataTransferItemList,
): Promise<DroppedFile[]> {
  const roots: FileSystemEntry[] = [];
  for (const item of Array.from(items)) {
    if (item.kind !== "file") continue;
    const entry = item.webkitGetAsEntry?.();
    if (entry) roots.push(entry);
  }

  const out: DroppedFile[] = [];
  const only = roots.length === 1 ? roots[0] : undefined;
  if (only && only.isDirectory) {
    // Strip the single top folder: walk its children at the root.
    const children = await readDir(only as FileSystemDirectoryEntry);
    for (const child of children) await walkEntry(child, "", out);
  } else {
    for (const root of roots) await walkEntry(root, "", out);
  }
  return out;
}

async function walkEntry(
  entry: FileSystemEntry,
  prefix: string,
  out: DroppedFile[],
): Promise<void> {
  if (entry.isFile) {
    const file = await entryFile(entry as FileSystemFileEntry);
    out.push({ path: prefix + entry.name, file });
    return;
  }
  if (entry.isDirectory) {
    const children = await readDir(entry as FileSystemDirectoryEntry);
    for (const child of children) {
      await walkEntry(child, `${prefix}${entry.name}/`, out);
    }
  }
}

function entryFile(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject));
}

/** readEntries is paginated — call it until it returns an empty batch. */
function readDir(dir: FileSystemDirectoryEntry): Promise<FileSystemEntry[]> {
  const reader = dir.createReader();
  return new Promise((resolve, reject) => {
    const all: FileSystemEntry[] = [];
    const pump = () =>
      reader.readEntries((batch) => {
        if (batch.length === 0) {
          resolve(all);
          return;
        }
        all.push(...batch);
        pump();
      }, reject);
    pump();
  });
}

/**
 * Collect files from a `<input type="file" webkitdirectory>` selection. Each File
 * carries `webkitRelativePath` like "mysite/index.html"; we strip the top folder
 * segment so the site root matches the drag-drop path scheme.
 */
export function collectInputFiles(fileList: FileList): DroppedFile[] {
  const out: DroppedFile[] = [];
  for (const file of Array.from(fileList)) {
    const rel = file.webkitRelativePath || file.name;
    const parts = rel.split("/");
    const path = parts.length > 1 ? parts.slice(1).join("/") : rel;
    if (path) out.push({ path, file });
  }
  return out;
}
