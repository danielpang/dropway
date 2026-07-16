"use server";

import { revalidatePath } from "next/cache";

import {
  api,
  ApiError,
  type ChatLog,
  type CreateChatInput,
  type CreateChatResult,
  type QuotaExceeded,
} from "@/lib/api";
import { apiErrorMessage } from "@/lib/action-errors";

// Server actions for the shared chat-log library ("Share This Session").
// Thin wrappers over lib/api.ts: the Go API is the authz boundary (org kill
// switch, creator-or-admin on mutations) and the quota gate — a 402 body is
// passed through as `quota` so the client can open the upgrade modal, exactly
// like the skills/sites actions.

function quotaOf(err: unknown): QuotaExceeded | undefined {
  return (err instanceof ApiError ? err.asQuotaExceeded() : null) ?? undefined;
}

export type CreateChatActionResult =
  | {
      ok: true;
      chatLog: ChatLog;
      /** Import disclosure: what was stored, pruned to the tier window, dropped. */
      appended: number;
      pruned: number;
      window: number;
      dropped: number;
    }
  | { ok: false; message: string; quota?: QuotaExceeded };

/**
 * Create a chat log (POST /v1/chats), optionally seeded with a pasted/uploaded
 * transcript. `pruned`/`dropped` are informational (the free-tier sliding
 * window keeps the newest messages); a hard cap arrives as a 402 quota body.
 */
export async function createChatAction(
  input: CreateChatInput,
): Promise<CreateChatActionResult> {
  try {
    const res: CreateChatResult = await api.createChat(input);
    revalidatePath("/chats");
    if (input.site_id) revalidatePath(`/sites/${input.site_id}`);
    return {
      ok: true,
      chatLog: res.chat_log ?? {},
      appended: res.appended ?? 0,
      pruned: res.pruned ?? 0,
      window: res.window ?? 0,
      dropped: res.dropped ?? 0,
    };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not import the conversation. Try again.", {
        404: "That site no longer exists.",
      }),
      quota: quotaOf(err),
    };
  }
}

export type SimpleChatActionResult =
  | { ok: true }
  | { ok: false; message: string; quota?: QuotaExceeded };

/** Delete a chat log and its messages (creator or admin). */
export async function deleteChatAction(chatId: string): Promise<SimpleChatActionResult> {
  try {
    await api.deleteChat(chatId);
    revalidatePath("/chats");
    return { ok: true };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not delete the chat log.", {
        404: "This chat log no longer exists.",
      }),
    };
  }
}

/** Delete one message by seq (mistakes, pasted secrets). */
export async function deleteChatMessageAction(input: {
  chatId: string;
  seq: number;
}): Promise<SimpleChatActionResult> {
  try {
    await api.deleteChatMessage(input.chatId, input.seq);
    revalidatePath(`/chats/${input.chatId}`);
    return { ok: true };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not delete the message.", {
        404: "This message no longer exists.",
      }),
    };
  }
}

export type ChatLogActionResult =
  | { ok: true; chatLog: ChatLog }
  | { ok: false; message: string };

/**
 * Attach, detach (siteId=null), or move the log's site binding. A 409 (the
 * target site already has a log) surfaces the API's message so the user knows
 * to detach the other log first.
 */
export async function setChatSiteAction(input: {
  chatId: string;
  siteId: string | null;
}): Promise<ChatLogActionResult> {
  try {
    const chatLog = await api.setChatSite(input.chatId, input.siteId);
    revalidatePath(`/chats/${input.chatId}`);
    revalidatePath("/chats");
    if (input.siteId) revalidatePath(`/sites/${input.siteId}`);
    return { ok: true, chatLog };
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      const apiMsg = (err.body as { message?: string } | null)?.message;
      return {
        ok: false,
        message:
          apiMsg ?? "That site already has a chat log. Detach it first, then try again.",
      };
    }
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not update the site attachment.", {
        404: "This chat log (or site) no longer exists.",
      }),
    };
  }
}

/** Toggle whether the attached site serves the "How this was made" panel. */
export async function setChatPanelAction(input: {
  chatId: string;
  enabled: boolean;
}): Promise<ChatLogActionResult> {
  try {
    const chatLog = await api.setChatPanel(input.chatId, input.enabled);
    revalidatePath(`/chats/${input.chatId}`);
    if (chatLog.site_id) revalidatePath(`/sites/${chatLog.site_id}`);
    return { ok: true, chatLog };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not update the panel setting.", {
        404: "This chat log no longer exists.",
      }),
    };
  }
}

/**
 * Flip the chat log's collaboration toggle (`allow_member_edits`) — whether
 * non-creators may modify its content (appends/curation/site binding/panel).
 * The Go API re-checks creator-or-admin and 403s otherwise; deletion stays
 * creator-or-admin regardless.
 */
export async function setChatCollabAction(input: {
  id: string;
  allowMemberEdits: boolean;
}): Promise<
  { ok: true; allowMemberEdits: boolean } | { ok: false; message: string }
> {
  try {
    const chatLog = await api.setChatCollab(input.id, input.allowMemberEdits);
    revalidatePath(`/chats/${input.id}`);
    return {
      ok: true,
      allowMemberEdits: chatLog.allow_member_edits ?? input.allowMemberEdits,
    };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(err, "Could not update the collaboration setting.", {
        403: "Only the creator or an admin can change this.",
        404: "This chat log no longer exists.",
      }),
    };
  }
}
