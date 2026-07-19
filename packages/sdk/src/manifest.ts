// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { createHash } from "node:crypto";

/**
 * One file in a deploy manifest. `sha256` is the lowercase-hex SHA-256 of the raw
 * bytes; `size` is the byte length; `contentType` is inferred from the path when
 * not supplied.
 */
export interface ManifestFile {
  path: string;
  sha256: string;
  size: number;
  contentType: string;
}

/** sha256Hex returns the lowercase-hex SHA-256 of bytes. */
export function sha256Hex(data: Uint8Array): string {
  return createHash("sha256").update(data).digest("hex");
}

/**
 * digest reproduces the server's `internal/manifest.Digest` EXACTLY so a
 * client-computed digest matches what the API recomputes and verifies at finalize.
 * The contract: sort files by path, then hash the concatenation of
 * `"<sha256>  <path>\n"` (two spaces) lines. `content_type`/`size` are NOT part of
 * the digest — only path + sha. A shared test vector pins parity with the Go side.
 */
export function digest(
  files: ReadonlyArray<{ path: string; sha256: string }>,
): string {
  // Sort by UTF-8 BYTES, not JS string order. Go compares paths bytewise
  // (`a < b` on Go strings), which equals UTF-8 byte order; JS `<` compares UTF-16
  // code units, which DIVERGES for supplementary-plane characters (a filename with
  // an emoji would sort differently, producing a mismatched digest the server
  // rejects at finalize). Buffer.compare gives us the exact byte order Go uses.
  const sorted = [...files].sort((a, b) =>
    Buffer.compare(Buffer.from(a.path, "utf8"), Buffer.from(b.path, "utf8")),
  );
  const h = createHash("sha256");
  for (const f of sorted) {
    h.update(`${f.sha256}  ${f.path}\n`);
  }
  return h.digest("hex");
}

/**
 * contentTypeForPath mirrors the server's `internal/contenttype.ForPath`: a small
 * extension → MIME map, defaulting to application/octet-stream. Kept in sync so a
 * client-inferred content type matches what the platform would serve.
 */
export function contentTypeForPath(path: string): string {
  const dot = path.lastIndexOf(".");
  const ext = dot >= 0 ? path.slice(dot).toLowerCase() : "";
  return BY_EXT[ext] ?? "application/octet-stream";
}

const BY_EXT: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".htm": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".json": "application/json",
  ".map": "application/json",
  ".svg": "image/svg+xml",
  ".wasm": "application/wasm",
  ".webmanifest": "application/manifest+json",
  ".txt": "text/plain; charset=utf-8",
  ".xml": "application/xml",
  ".ico": "image/x-icon",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".avif": "image/avif",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
  ".otf": "font/otf",
  ".eot": "application/vnd.ms-fontobject",
  ".pdf": "application/pdf",
  ".mp4": "video/mp4",
  ".webm": "video/webm",
  ".mp3": "audio/mpeg",
  ".wav": "audio/wav",
};

/**
 * normalizePath cleans a file's served path: forward slashes, no leading `./` or
 * `/`, and rejects `..` traversal. The server validates paths too; normalizing here
 * gives a clearer client-side error and a stable manifest.
 */
export function normalizePath(path: string): string {
  let p = path.replace(/\\/g, "/");
  while (p.startsWith("./")) p = p.slice(2);
  while (p.startsWith("/")) p = p.slice(1);
  if (p === "" || p.split("/").some((seg) => seg === "..")) {
    throw new Error(`invalid deploy path: ${JSON.stringify(path)}`);
  }
  return p;
}

/** toBytes coerces string | Uint8Array file content to bytes (UTF-8 for strings). */
export function toBytes(data: string | Uint8Array): Uint8Array {
  return typeof data === "string" ? new TextEncoder().encode(data) : data;
}

/**
 * buildManifest turns a `{ path: content }` map into the sorted manifest + a
 * sha→bytes lookup (for the upload step) + the deploy digest.
 */
export function buildManifest(files: Record<string, string | Uint8Array>): {
  manifest: ManifestFile[];
  bytesBySha: Map<string, Uint8Array>;
  digest: string;
} {
  const manifest: ManifestFile[] = [];
  const bytesBySha = new Map<string, Uint8Array>();
  for (const [rawPath, rawData] of Object.entries(files)) {
    const path = normalizePath(rawPath);
    const bytes = toBytes(rawData);
    const sha = sha256Hex(bytes);
    manifest.push({
      path,
      sha256: sha,
      size: bytes.byteLength,
      contentType: contentTypeForPath(path),
    });
    bytesBySha.set(sha, bytes);
  }
  return { manifest, bytesBySha, digest: digest(manifest) };
}
