/**
 * Client-safe helpers for the site "download as zip" affordance. lib/api.ts is
 * `server-only`, so the decode/path-safety logic a client component needs lives
 * here. completeTruncatedSiteDownload is also here so the server action and its
 * unit tests share one implementation.
 */

import { isSafeSkillPath } from "@/lib/skills-shared";
import type { ZipEntry } from "@/lib/zip";

/** One file's inline bytes (utf8 / base64), matching GET /v1/sites/{id}/download. */
export type SiteFilePayload = {
  path?: string;
  content?: string;
  encoding?: "utf8" | "base64";
  content_type?: string;
  size?: number;
};

/** Successful body of GET /v1/sites/{id}/download. */
export type SiteDownloadPayload = {
  slug?: string;
  site_id?: string;
  truncated?: boolean;
  files?: SiteFilePayload[];
};

/** One manifest entry from GET /v1/sites/{id}/files. */
export type SiteFileMeta = {
  path?: string;
  size?: number;
  content_type?: string;
  sha256?: string;
};

/**
 * When the bulk download hit its size budget, fetch every listed path that was
 * omitted. Files that still fail (e.g. a single file over the per-read cap)
 * are reported in `skipped` rather than failing the whole download.
 */
export async function completeTruncatedSiteDownload(
  download: SiteDownloadPayload,
  listed: SiteFileMeta[],
  readFile: (path: string) => Promise<SiteFilePayload>,
): Promise<{ files: SiteFilePayload[]; skipped: string[] }> {
  const files = [...(download.files ?? [])];
  if (!download.truncated) return { files, skipped: [] };

  const have = new Set(
    files.map((f) => f.path).filter((p): p is string => Boolean(p)),
  );
  const missing = listed.filter((m) => m.path && !have.has(m.path));
  const skipped: string[] = [];
  const extras = await Promise.all(
    missing.map(async (m) => {
      const path = m.path!;
      try {
        return await readFile(path);
      } catch {
        skipped.push(path);
        return null;
      }
    }),
  );
  for (const extra of extras) {
    if (extra) files.push(extra);
  }
  return { files, skipped };
}

/**
 * Turn inline download files into zip entries under `prefix` (typically
 * "<slug>/"). Unsafe paths are skipped, not written — a filename that merely
 * contains ".." is allowed (isSafeSkillPath).
 */
export function decodeSiteFiles(
  files: SiteFilePayload[],
  prefix: string,
): { entries: ZipEntry[]; skipped: string[] } {
  const entries: ZipEntry[] = [];
  const skipped: string[] = [];
  for (const f of files) {
    if (!f.path) continue;
    if (!isSafeSkillPath(f.path)) {
      skipped.push(f.path);
      continue;
    }
    const data =
      f.encoding === "base64"
        ? Uint8Array.from(atob(f.content ?? ""), (c) => c.charCodeAt(0))
        : new TextEncoder().encode(f.content ?? "");
    entries.push({ path: `${prefix}${f.path}`, data });
  }
  return { entries, skipped };
}
