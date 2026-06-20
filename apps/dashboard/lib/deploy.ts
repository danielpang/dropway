/**
 * Browser-side folder deploy, the drag-and-drop equivalent of `dropway deploy`.
 *
 * Runs ENTIRELY in the browser (it reads the dropped files, which only exist
 * client-side) and mirrors the CLI's contract so the Go API accepts it unchanged:
 *
 *   1. walk the dropped folder → {path, file}[]   (dotfiles included; POSIX paths)
 *   2. sha256 each file + build the manifest, sorted by path
 *   3. compute the whole-deploy DIGEST byte-exactly (`<sha>  <path>\n`, two spaces)
 *   4. prepare  → which blobs are missing + a presigned PUT URL per sha   (server action)
 *   5. PUT each missing blob DIRECTLY to object storage (no JWT, no Content-Type)
 *   6. finalize → immutable version                                       (server action)
 *   7. publish  → flip the live pointer                                   (server action)
 *
 * Steps 1 to 3 live in lib/deploy-manifest.ts (pure + unit-tested). The two JSON
 * round-trips go through server actions so the Better Auth JWT never leaves the
 * server; only the blob BYTES upload from the browser, straight to the store.
 */

import {
  finalizeDeploymentAction,
  prepareDeploymentAction,
  publishVersionAction,
} from "@/app/(app)/sites/[id]/actions";
import type { QuotaExceeded } from "@/lib/api";
import {
  buildManifest,
  collectDataTransferItems,
  collectInputFiles,
  type DroppedFile,
} from "@/lib/deploy-manifest";

// Re-export the folder-collection helpers + type so the dropzone imports one module.
export { collectDataTransferItems, collectInputFiles };
export type { DroppedFile };

export type DeployProgress =
  | { phase: "hashing"; done: number; total: number }
  | { phase: "preparing" }
  | { phase: "uploading"; done: number; total: number }
  | { phase: "finalizing" }
  | { phase: "publishing" };

export type DeployOutcome =
  | { ok: true; liveUrl: string; versionId: string; fileCount: number }
  | { ok: false; message: string; quota?: QuotaExceeded };

// ---- Upload concurrency ---------------------------------------------------

async function runPool<T>(
  items: T[],
  limit: number,
  fn: (item: T) => Promise<void>,
): Promise<void> {
  let cursor = 0;
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (cursor < items.length) {
      const item = items[cursor++];
      if (item === undefined) break;
      await fn(item);
    }
  });
  await Promise.all(workers);
}

// ---- The orchestrated deploy ----------------------------------------------

/**
 * Run a full folder deploy: hash → prepare → upload missing blobs → finalize →
 * publish (drop → live). Reports progress through onProgress. Never throws, a
 * failure resolves to `{ ok: false, message }` (plus `quota` on a storage 402).
 */
export async function deployFolder(opts: {
  siteId: string;
  files: DroppedFile[];
  onProgress?: (p: DeployProgress) => void;
}): Promise<DeployOutcome> {
  const { siteId, files, onProgress } = opts;
  if (files.length === 0) {
    return { ok: false, message: "That folder has no files to deploy." };
  }

  // 1 to 3. hash + manifest + digest (the heavy local step).
  const { manifest, digest, byHash } = await buildManifest(files, (done, total) =>
    onProgress?.({ phase: "hashing", done, total }),
  );

  // 4. prepare, discover missing blobs + presigned URLs.
  onProgress?.({ phase: "preparing" });
  const prep = await prepareDeploymentAction({ siteId, manifest });
  if (!prep.ok) return { ok: false, message: prep.message };

  // 5. upload each missing blob directly to object storage. The PUT body is a raw
  // ArrayBuffer (NOT a File/Blob) so the browser sends NO Content-Type header, // the presigned URL signs only {Bucket,Key}, so a Content-Type would risk a
  // SigV4 mismatch. No Authorization either (the URL is the credential).
  const missing = prep.missing;
  onProgress?.({ phase: "uploading", done: 0, total: missing.length });
  let uploaded = 0;
  try {
    await runPool(missing, 6, async (sha) => {
      const url = prep.uploads[sha];
      const file = byHash.get(sha);
      if (!url || !file) throw new Error(`no upload target for ${sha}`);
      const res = await fetch(url, { method: "PUT", body: await file.arrayBuffer() });
      if (!res.ok) throw new Error(`upload ${res.status}`);
      onProgress?.({ phase: "uploading", done: ++uploaded, total: missing.length });
    });
  } catch {
    return {
      ok: false,
      message:
        "A file failed to upload to storage. Check your connection (and that the object store is reachable) and try again.",
    };
  }

  // 6. finalize, server re-verifies every blob + the digest, creates the version.
  onProgress?.({ phase: "finalizing" });
  const fin = await finalizeDeploymentAction({ siteId, manifest, digest });
  if (!fin.ok) return { ok: false, message: fin.message, quota: fin.quota };

  const versionId = fin.version.version_id ?? "";

  // 7. publish, drop → live.
  onProgress?.({ phase: "publishing" });
  const pub = await publishVersionAction({ siteId, versionId });
  if (!pub.ok) return { ok: false, message: pub.message };

  return {
    ok: true,
    liveUrl: pub.result.live_url ?? "",
    versionId,
    fileCount: files.length,
  };
}
