"use server";

import { revalidatePath } from "next/cache";

import { api, type SiteComment } from "@/lib/api";
import { apiErrorMessage } from "@/lib/action-errors";

/** A feed post is a site or a skill; the social actions are keyed by kind. */
export type PostKind = "site" | "skill";

export type VoteActionResult =
  | { ok: true; score: number; myVote: number }
  | { ok: false; message: string };

/**
 * Cast the caller's vote on a feed post (PUT /v1/{sites|skills}/{id}/vote). value
 * 1 (up), -1 (down), or 0 to clear. Returns the post's new net score + the vote.
 */
export async function voteAction(input: {
  kind: PostKind;
  id: string;
  value: -1 | 0 | 1;
}): Promise<VoteActionResult> {
  try {
    const res = await api.setPostVote(input.kind, input.id, input.value);
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
 * (PUT /v1/{sites|skills}/{id}/feed-meta). Owner-or-admin only; empty clears a field.
 */
export async function setPostMetaAction(input: {
  kind: PostKind;
  id: string;
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
    const res = await api.setPostFeedMeta(input.kind, input.id, { title, description });
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

/** Post a comment to a feed post (site or skill), optionally tagging teammates. */
export async function addFeedCommentAction(input: {
  kind: PostKind;
  id: string;
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
    const comment = await api.addPostComment(input.kind, input.id, {
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
  kind: PostKind,
  id: string,
): Promise<ListCommentsActionResult> {
  try {
    const comments = await api.listPostComments(kind, id);
    return { ok: true, comments };
  } catch (err) {
    return { ok: false, message: apiErrorMessage(err, "Could not load comments. Try again.") };
  }
}
