"use server";

import { revalidatePath } from "next/cache";

import { api, ApiError, type Memory, type MemoryKind } from "@/lib/api";

/**
 * Map an API error to a friendly message, preferring the server's own detail.
 * The memory routes have two extra states beyond the usual 403: a 422 with body
 * `{error:"quota"}` when the org is at its memory cap, and a 403 that (for
 * member-allowed writes) means the org flag is off rather than a role problem.
 */
function messageFor(err: unknown, fallback: string, forbidden: string): string {
  if (err instanceof ApiError) {
    const apiMsg = (err.body as { message?: string } | null)?.message;
    if (apiMsg) return apiMsg;
    if (err.status === 403) return forbidden;
    if (err.status === 404) return "That memory no longer exists.";
    if (isQuota(err)) {
      return "Your organization is at its memory limit. Delete or disable some memories to make room.";
    }
    return fallback;
  }
  return "Could not reach the API. Try again.";
}

/** True for the 422 `{error:"quota"}` body the create route returns at the cap. */
function isQuota(err: ApiError): boolean {
  return (
    err.status === 422 &&
    (err.body as { error?: string } | null)?.error === "quota"
  );
}

export type CreateMemoryActionResult =
  | { ok: true; memory: Memory; created: boolean }
  | { ok: false; message: string };

/**
 * Record a memory by hand (any member). Deduplicated server-side (`created` is
 * false when an equivalent memory already existed); 422 at the org's cap. A 403
 * here means the org flag is off — creating is member-allowed.
 */
export async function createMemoryAction(input: {
  content: string;
  kind?: MemoryKind;
}): Promise<CreateMemoryActionResult> {
  const content = input.content.trim();
  if (!content) return { ok: false, message: "Write something to remember." };
  try {
    const result = await api.createMemory({ content, kind: input.kind });
    revalidatePath("/settings/memory");
    revalidatePath("/settings");
    return { ok: true, memory: result.memory, created: result.created };
  } catch (err) {
    return {
      ok: false,
      message: messageFor(
        err,
        "Could not save the memory. Try again.",
        "Company memory is turned off for your organization.",
      ),
    };
  }
}

export type PatchMemoryActionResult =
  | { ok: true; memory: Memory }
  | { ok: false; message: string };

/**
 * Edit a memory: content/kind rewrites, pin/unpin, disable/enable (any subset
 * of fields). Admin-only — the Go API re-checks the role and 403s otherwise.
 */
export async function patchMemoryAction(input: {
  id: string;
  content?: string;
  kind?: string;
  pinned?: boolean;
  disabled?: boolean;
}): Promise<PatchMemoryActionResult> {
  const { id, ...patch } = input;
  if (patch.content !== undefined && !patch.content.trim()) {
    return { ok: false, message: "A memory can't be empty." };
  }
  try {
    const memory = await api.patchMemory(id, patch);
    revalidatePath("/settings/memory");
    return { ok: true, memory };
  } catch (err) {
    return {
      ok: false,
      message: messageFor(
        err,
        "Could not update the memory. Try again.",
        "Only owners and admins can edit memories.",
      ),
    };
  }
}

export type DeleteMemoryActionResult =
  | { ok: true }
  | { ok: false; message: string };

/** Permanently delete a memory. Admin-only → 403 otherwise. */
export async function deleteMemoryAction(input: {
  id: string;
}): Promise<DeleteMemoryActionResult> {
  try {
    await api.deleteMemory(input.id);
    revalidatePath("/settings/memory");
    revalidatePath("/settings");
    return { ok: true };
  } catch (err) {
    return {
      ok: false,
      message: messageFor(
        err,
        "Could not delete the memory. Try again.",
        "Only owners and admins can delete memories.",
      ),
    };
  }
}

export type SearchMemoriesActionResult =
  | { ok: true; memories: Memory[] }
  | { ok: false; message: string };

/**
 * Semantic search over the org's memories (any member): pinned memories first
 * (no distance), then the k nearest by embedding distance. The management view
 * filters the list by URL instead, but this powers ad-hoc recall checks.
 */
export async function searchMemoriesAction(input: {
  query: string;
  k?: number;
}): Promise<SearchMemoriesActionResult> {
  const query = input.query.trim();
  if (!query) return { ok: false, message: "Type something to search for." };
  try {
    const memories = await api.searchMemories(query, input.k);
    return { ok: true, memories };
  } catch (err) {
    return {
      ok: false,
      message: messageFor(
        err,
        "Could not search memories. Try again.",
        "Company memory is turned off for your organization.",
      ),
    };
  }
}
