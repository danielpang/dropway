/**
 * Browser-side skill upload, the drag-and-drop equivalent of
 * `dropway skills push`. Mirrors lib/deploy.ts exactly — the same
 * hash → prepare → direct presigned PUTs → finalize pipeline over the same
 * manifest builder (lib/deploy-manifest.ts is content-agnostic) — with two
 * differences: a skill requires a SKILL.md at its root (pre-checked here for a
 * friendly error before any hashing), and there is no publish step: skills are
 * latest-only, so the server flips the live pointer inside finalize.
 *
 * The JSON round-trips go through server actions (the Better Auth JWT never
 * leaves the server); only the blob BYTES upload from the browser.
 */

import {
  createSkillAction,
  finalizeSkillUploadAction,
  prepareSkillUploadAction,
} from "@/app/(app)/skills/actions";
import type { QuotaExceeded } from "@/lib/api";
import { buildManifest, type DroppedFile } from "@/lib/deploy-manifest";

export type SkillUploadProgress =
  | { phase: "hashing"; done: number; total: number }
  | { phase: "creating" }
  | { phase: "preparing" }
  | { phase: "uploading"; done: number; total: number }
  | { phase: "finalizing" };

export type SkillUploadOutcome =
  | { ok: true; skillId: string; versionNo: number; warnings: string[]; fileCount: number }
  | { ok: false; message: string; quota?: QuotaExceeded };

/** True when the dropped folder has a SKILL.md at its top level. */
export function hasRootSkillMD(files: DroppedFile[]): boolean {
  return files.some((f) => f.path === "SKILL.md");
}

// Client-side mirrors of the server's skillspec caps, for early friendly errors
// (the API re-enforces both).
export const MAX_SKILL_FILES = 200;
export const MAX_SKILL_BYTES = 5 * 1024 * 1024;

/** A cheap pre-flight so a bad folder fails before hashing/uploading. */
export function precheckSkillFolder(files: DroppedFile[]): string | null {
  if (files.length === 0) return "That folder has no files.";
  if (!hasRootSkillMD(files)) {
    return "A skill needs a SKILL.md at the top level of the folder. If your SKILL.md is inside a subfolder, upload that subfolder instead.";
  }
  if (files.length > MAX_SKILL_FILES) {
    return `A skill can have at most ${MAX_SKILL_FILES} files (this folder has ${files.length}).`;
  }
  let total = 0;
  for (const f of files) total += f.file.size;
  if (total > MAX_SKILL_BYTES) {
    return "A skill can be at most 5 MiB in total.";
  }
  return null;
}

// Same bounded-concurrency pool as lib/deploy.ts (duplicated rather than
// exported from there to keep deploy's public surface unchanged).
async function runPool<T>(items: T[], limit: number, fn: (item: T) => Promise<void>): Promise<void> {
  const queue = [...items];
  const workers = Array.from({ length: Math.min(limit, queue.length) }, async () => {
    for (;;) {
      const item = queue.shift();
      if (item === undefined) return;
      await fn(item);
    }
  });
  await Promise.all(workers);
}

/**
 * Run a full skill upload: (optionally create the skill) → hash → prepare →
 * upload missing blobs → finalize. Never throws; a failure resolves to
 * `{ ok:false, message }` (plus `quota` on a 402 — the folder cap or storage).
 */
export async function uploadSkillFolder(opts: {
  /** Existing skill to push a new version to, or null to create one. */
  skillId: string | null;
  /** Create parameters, used when skillId is null. */
  create?: { slug: string; title?: string; folders?: string[] };
  files: DroppedFile[];
  onProgress?: (p: SkillUploadProgress) => void;
}): Promise<SkillUploadOutcome> {
  const { files, onProgress } = opts;
  const precheck = precheckSkillFolder(files);
  if (precheck) return { ok: false, message: precheck };

  let skillId = opts.skillId;
  if (!skillId) {
    if (!opts.create?.slug) return { ok: false, message: "The skill needs a name." };
    onProgress?.({ phase: "creating" });
    const created = await createSkillAction(opts.create);
    if (!created.ok) return { ok: false, message: created.message, quota: created.quota };
    skillId = created.skill.id ?? null;
    if (!skillId) return { ok: false, message: "The API did not return the new skill's id." };
  }

  const { manifest, digest, byHash } = await buildManifest(files, (done, total) =>
    onProgress?.({ phase: "hashing", done, total }),
  );

  onProgress?.({ phase: "preparing" });
  const prep = await prepareSkillUploadAction({ skillId, manifest });
  if (!prep.ok) return { ok: false, message: prep.message };

  // Raw ArrayBuffer PUTs, no Content-Type / Authorization — the presigned URL
  // signs only {Bucket,Key} (see lib/deploy.ts for the SigV4 rationale).
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

  onProgress?.({ phase: "finalizing" });
  const fin = await finalizeSkillUploadAction({ skillId, manifest, digest });
  if (!fin.ok) return { ok: false, message: fin.message, quota: fin.quota };

  return {
    ok: true,
    skillId,
    versionNo: fin.result.version_no ?? 0,
    warnings: fin.result.warnings ?? [],
    fileCount: files.length,
  };
}
