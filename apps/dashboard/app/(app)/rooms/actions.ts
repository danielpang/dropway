"use server";

import { isCollabReviewEnabled } from "@/lib/collab";
import { ApiError, api } from "@/lib/api";
import {
  bindApproval,
  parseCandidate,
  parseRoom,
  parseThread,
  type ApprovalBinding,
  type CandidateDecisionBody,
  type ReviewCandidate,
} from "@/lib/project-room/contract";

function closed(): { ok: false; message: string } {
  return { ok: false, message: "Project rooms are off." };
}

function enabled(): boolean {
  return isCollabReviewEnabled({
    NEXT_PUBLIC_COLLAB_REVIEW: process.env.NEXT_PUBLIC_COLLAB_REVIEW,
  });
}

function messageOf(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return "The room request failed.";
}

function decisionFrom(error: ApiError): CandidateDecisionBody | null {
  const body = error.body;
  if (body === null || typeof body !== "object") return null;
  const row = body as { candidate?: unknown; changed?: unknown; decision?: unknown };
  const candidate = parseCandidate(row.candidate);
  if (!candidate || typeof row.changed !== "boolean") return null;
  if (row.decision !== "approve_change" && row.decision !== "reject_change") return null;
  return { candidate, changed: row.changed, decision: row.decision };
}

export async function createRoomAction(name: string) {
  if (!enabled()) return closed();
  try {
    const room = parseRoom(await api.createProjectRoom(name));
    if (!room) return { ok: false as const, message: "The room response was not usable." };
    return { ok: true as const, room };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function createThreadAction(input: {
  roomId: string;
  siteId: string;
  versionId: string;
  page: string;
  kind: "page" | "point";
  anchor: unknown;
  body: string;
}) {
  if (!enabled()) return closed();
  if (input.kind !== "page" && input.kind !== "point") {
    return { ok: false as const, message: "That anchor is not available." };
  }
  try {
    const thread = parseThread(await api.createReviewThread(input));
    if (!thread) return { ok: false as const, message: "The thread response was not usable." };
    const reply = input.body.trim()
      ? await api.addReviewReply(input.roomId, thread.ID, input.body.trim())
      : null;
    if (reply) thread.Replies = [...thread.Replies, reply];
    return { ok: true as const, thread };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function replyAction(roomId: string, threadId: string, body: string) {
  if (!enabled()) return closed();
  try {
    const reply = await api.addReviewReply(roomId, threadId, body);
    return { ok: true as const, reply };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function signoffAction(roomId: string, siteId: string, versionId: string) {
  if (!enabled()) return closed();
  try {
    const signoff = await api.signoffProjectRoomVersion(roomId, siteId, versionId);
    return { ok: true as const, signoff };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function decideAction(
  roomId: string,
  candidate: ReviewCandidate,
  decision: "approve" | "reject",
) {
  if (!enabled()) return closed();
  const binding: ApprovalBinding | null = bindApproval(candidate);
  if (!binding) return { ok: false as const, message: "The candidate identity is incomplete." };
  try {
    const result = await api.decideProjectRoomCandidate(roomId, candidate.ID, decision, binding);
    const parsed = parseCandidate(result.candidate);
    if (!parsed) return { ok: false as const, message: "The decision response was not usable." };
    return { ok: true as const, candidate: parsed, changed: result.changed, decision: result.decision };
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      const conflict = decisionFrom(error);
      if (conflict) {
        return {
          ok: false as const,
          message: "Conflict. The live baseline did not match, so the change was not applied.",
          candidate: conflict.candidate,
        };
      }
    }
    return { ok: false as const, message: messageOf(error) };
  }
}
