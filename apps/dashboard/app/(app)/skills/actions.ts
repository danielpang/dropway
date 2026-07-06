"use server";

import { revalidatePath } from "next/cache";

import {
  api,
  ApiError,
  type ManifestFile,
  type QuotaExceeded,
  type Skill,
  type SkillDownload,
  type SkillFolder,
  type SkillFolderDownload,
  type SkillUploadResult,
} from "@/lib/api";
import { apiErrorMessage } from "@/lib/action-errors";

// Skill uploads mirror the site-deploy actions: the browser hashes + PUTs the
// bytes to presigned URLs itself; only the JSON round-trips run here so the
// Better Auth JWT never leaves the server.

function quotaOf(err: unknown): QuotaExceeded | undefined {
  return (err instanceof ApiError ? err.asQuotaExceeded() : null) ?? undefined;
}

export type CreateSkillActionResult =
  | { ok: true; skill: Skill }
  | { ok: false; message: string; quota?: QuotaExceeded };

/** Register a skill (POST /v1/skills). 402 carries the folder-cap quota body. */
export async function createSkillAction(input: {
  slug: string;
  title?: string;
  folders?: string[];
}): Promise<CreateSkillActionResult> {
  const slug = input.slug.trim().toLowerCase();
  if (!slug) return { ok: false, message: "The skill needs a name." };
  try {
    const skill = await api.createSkill({ slug, title: input.title?.trim() || undefined, folders: input.folders });
    revalidatePath("/skills");
    return { ok: true, skill };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not create the skill. Try again."),
      quota: quotaOf(err),
    };
  }
}

export type PrepareSkillUploadActionResult =
  | { ok: true; missing: string[]; uploads: Record<string, string> }
  | { ok: false; message: string };

/** Upload step 1: validate the manifest + get presigned PUT URLs. */
export async function prepareSkillUploadAction(input: {
  skillId: string;
  manifest: ManifestFile[];
}): Promise<PrepareSkillUploadActionResult> {
  try {
    const res = await api.prepareSkillUpload(input.skillId, input.manifest);
    return { ok: true, missing: res.missing ?? [], uploads: (res.uploads ?? {}) as Record<string, string> };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not prepare the upload.") };
  }
}

export type FinalizeSkillUploadActionResult =
  | { ok: true; result: SkillUploadResult }
  | { ok: false; message: string; quota?: QuotaExceeded };

/** Upload step 3: finalize (server-verifies blobs; finalize publishes). */
export async function finalizeSkillUploadAction(input: {
  skillId: string;
  manifest: ManifestFile[];
  digest: string;
}): Promise<FinalizeSkillUploadActionResult> {
  try {
    const result = await api.finalizeSkillUpload(input.skillId, input.manifest, input.digest);
    revalidatePath("/skills");
    return { ok: true, result };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not finalize the upload."),
      quota: quotaOf(err),
    };
  }
}

export type SimpleActionResult =
  | { ok: true }
  | { ok: false; message: string; quota?: QuotaExceeded };

/** Delete a skill (owner or org admin). */
export async function deleteSkillAction(skillId: string): Promise<SimpleActionResult> {
  try {
    await api.deleteSkill(skillId);
    revalidatePath("/skills");
    return { ok: true };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not delete the skill.") };
  }
}

/** Replace a skill's folder memberships (owner or admin). */
export async function setSkillFoldersAction(input: {
  skillId: string;
  folders: string[];
}): Promise<SimpleActionResult> {
  try {
    await api.setSkillFolders(input.skillId, input.folders);
    revalidatePath("/skills");
    return { ok: true };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not update the skill's folders."),
      quota: quotaOf(err),
    };
  }
}

export type DownloadSkillActionResult =
  | { ok: true; download: SkillDownload }
  | { ok: false; message: string };

/** Fetch one skill's files inline (the client zips + saves them). */
export async function downloadSkillAction(skillId: string): Promise<DownloadSkillActionResult> {
  try {
    const download = await api.downloadSkill(skillId);
    return { ok: true, download };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not download the skill.") };
  }
}

export type DownloadFolderActionResult =
  | { ok: true; download: SkillFolderDownload }
  | { ok: false; message: string };

/**
 * Bulk-fetch a folder's skills. Truncated stubs (response budget / read error)
 * are re-fetched individually here so the client always gets complete files.
 */
export async function downloadSkillFolderAction(folderId: string): Promise<DownloadFolderActionResult> {
  try {
    const download = await api.downloadSkillFolder(folderId);
    const skills = await Promise.all(
      (download.skills ?? []).map(async (s) => {
        if (!s.truncated || !s.skill_id) return s;
        try {
          return await api.downloadSkill(s.skill_id);
        } catch {
          return s; // keep the stub; the client reports it
        }
      }),
    );
    return { ok: true, download: { ...download, skills } };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not download the folder.") };
  }
}

// ---- Folder curation (admin/owner; owners may add their own skill) ---------

export type FolderActionResult =
  | { ok: true; folder?: SkillFolder }
  | { ok: false; message: string; quota?: QuotaExceeded };

export async function createSkillFolderAction(input: {
  slug: string;
  title?: string;
}): Promise<FolderActionResult> {
  const slug = input.slug.trim().toLowerCase();
  if (!slug) return { ok: false, message: "The folder needs a slug." };
  try {
    const folder = await api.createSkillFolder({ slug, title: input.title?.trim() || undefined });
    revalidatePath("/skills");
    return { ok: true, folder };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not create the folder.") };
  }
}

export async function renameSkillFolderAction(input: {
  folderId: string;
  title: string;
}): Promise<FolderActionResult> {
  const title = input.title.trim();
  if (!title) return { ok: false, message: "The folder needs a title." };
  try {
    const folder = await api.renameSkillFolder(input.folderId, title);
    revalidatePath("/skills");
    return { ok: true, folder };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not rename the folder.") };
  }
}

export async function deleteSkillFolderAction(folderId: string): Promise<FolderActionResult> {
  try {
    await api.deleteSkillFolder(folderId);
    revalidatePath("/skills");
    return { ok: true };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not delete the folder.") };
  }
}

export async function addSkillToFolderAction(input: {
  folderId: string;
  skillId: string;
  isPreset?: boolean;
}): Promise<SimpleActionResult> {
  try {
    await api.addSkillFolderItem(input.folderId, {
      skill_id: input.skillId,
      is_preset: input.isPreset ?? false,
    });
    revalidatePath("/skills");
    return { ok: true };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not add the skill to the folder."),
      quota: quotaOf(err),
    };
  }
}

export async function removeSkillFromFolderAction(input: {
  folderId: string;
  skillId: string;
}): Promise<SimpleActionResult> {
  try {
    await api.removeSkillFolderItem(input.folderId, input.skillId);
    revalidatePath("/skills");
    return { ok: true };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not remove the skill from the folder.") };
  }
}

export async function setPresetFlagAction(input: {
  folderId: string;
  skillId: string;
  isPreset: boolean;
}): Promise<SimpleActionResult> {
  try {
    await api.setSkillFolderItemPreset(input.folderId, input.skillId, input.isPreset);
    revalidatePath("/skills");
    return { ok: true };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not update the preset flag.") };
  }
}
