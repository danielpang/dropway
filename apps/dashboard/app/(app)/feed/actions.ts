"use server";

import { revalidatePath } from "next/cache";

import { api, type SiteComment } from "@/lib/api";
import { apiErrorMessage } from "@/lib/action-errors";

export type VoteActionResult =
  | { ok: true; score: number; myVote: number }
  | { ok: false; message: string };

/**
 * Cast the caller's vote on a feed post (PUT /v1/sites/{id}/vote). value 1 (up),
 * -1 (down), or 0 to clear. Returns the post's new net score + the caller's vote.
 */
export async function voteAction(input: {
  siteId: string;
  value: -1 | 0 | 1;
}): Promise<VoteActionResult> {
  try {
    const res = await api.setSiteVote(input.siteId, input.value);
    return { ok: true, score: res.score ?? 0, myVote: res.my_vote ?? 0 };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not record your vote. Try again.") };
  }
}

export type PostMetaActionResult =
  | { ok: true; title: string; description: string }
  | { ok: false; message: string };

/**
 * Set a feed post's title + description inline from the feed
 * (PUT /v1/sites/{id}/feed-meta). Owner-or-admin only; empty strings clear a field.
 */
export async function setPostMetaAction(input: {
  siteId: string;
  title: string;
  description: string;
}): Promise<PostMetaActionResult> {
  const title = input.title.trim();
  const description = input.description.trim();
  if (title.length > 120) {
    return { ok: false, message: "Title must be at most 120 characters." };
  }
  if (description.length > 500) {
    return { ok: false, message: "Description must be at most 500 characters." };
  }
  try {
    const res = await api.setSiteFeedMeta(input.siteId, { title, description });
    revalidatePath("/feed");
    return {
      ok: true,
      title: res.title ?? title,
      description: res.description ?? description,
    };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not update the post. Try again.") };
  }
}

export type AddCommentActionResult =
  | { ok: true; comment: SiteComment }
  | { ok: false; message: string };

/** Post a comment to a feed post (POST /v1/sites/{id}/comments), optionally tagging teammates. */
export async function addFeedCommentAction(input: {
  siteId: string;
  body: string;
  mentionedUserIds: string[];
}): Promise<AddCommentActionResult> {
  const body = input.body.trim();
  if (!body) {
    return { ok: false, message: "Write something before posting." };
  }
  if (body.length > 4000) {
    return { ok: false, message: "Comment is too long (max 4000 characters)." };
  }
  try {
    const comment = await api.addComment(input.siteId, {
      body,
      mentioned_user_ids: input.mentionedUserIds,
    });
    revalidatePath("/feed");
    return { ok: true, comment };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not post your comment. Try again.") };
  }
}

export type ListCommentsActionResult =
  | { ok: true; comments: SiteComment[] }
  | { ok: false; message: string };

/** Load a feed post's comment thread on demand (when a user expands comments). */
export async function listFeedCommentsAction(
  siteId: string,
): Promise<ListCommentsActionResult> {
  try {
    const comments = await api.listComments(siteId);
    return { ok: true, comments };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not load comments. Try again.") };
  }
}
