"use server";

import { isCollabReviewEnabled } from "@/lib/collab";
import { ApiError, api } from "@/lib/api";
import {
  bindApproval,
  parseCandidate,
  parseCandidateDiff,
  parseCandidatePage,
  parseRoom,
  parseRoomDetail,
  parseSiteCommentPage,
  parseThread,
  parseThreadDetail,
  parseThreadPage,
  type ApprovalBinding,
  type CandidateDecisionBody,
  type ReviewCandidate,
  type ReviewThread,
} from "@/lib/project-room/contract";

function closed(): { ok: false; message: string } {
  return { ok: false, message: "Project rooms are off." };
}

function enabledFlag(): boolean {
  return isCollabReviewEnabled({
    NEXT_PUBLIC_COLLAB_REVIEW: process.env.NEXT_PUBLIC_COLLAB_REVIEW,
  });
}

function enabled(): boolean {
  return enabledFlag();
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

export async function enableRoomsAction(enabled: boolean) {
  if (!enabledFlag()) return closed();
  try {
    const settings = await api.projectRoomSettings();
    if (settings.enabled === enabled) return { ok: true as const, enabled };
    await api.setProjectRoomsEnabled(enabled);
    return { ok: true as const, enabled };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
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

export async function loadRoomAction(roomId: string) {
  if (!enabled()) return closed();
  try {
    const [detailBody, threadBody, candidateBody] = await Promise.all([
      api.getProjectRoom(roomId),
      api.listRoomThreads(roomId),
      api.listRoomCandidates(roomId),
    ]);
    const detail = parseRoomDetail(detailBody);
    const threads = parseThreadPage(threadBody);
    const candidates = parseCandidatePage(candidateBody);
    if (!detail || !threads || !candidates) {
      return { ok: false as const, message: "The room read was not usable." };
    }
    const withReplies = await hydrateThreads(threads.threads);
    const identified = await hydrateCandidates(roomId, candidates.candidates);
    return {
      ok: true as const,
      detail,
      threads: withReplies,
      threadCursor: threads.nextCursor,
      candidates: identified,
      candidateCursor: candidates.nextCursor,
    };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

async function hydrateThreads(threads: ReviewThread[]) {
  return Promise.all(threads.map(async (thread) => {
    const full = parseThreadDetail(await api.getReviewThread(thread.ID));
    return full ?? thread;
  }));
}

async function hydrateCandidates(roomId: string, candidates: ReviewCandidate[]) {
  return Promise.all(candidates.map(async (candidate) => {
    const body = await api.getRoomCandidate(roomId, candidate.ID);
    const row = body && typeof body === "object" && "candidate" in body ? (body as { candidate: unknown }).candidate : body;
    return parseCandidate(row) ?? candidate;
  }));
}

export async function loadMoreThreadsAction(roomId: string, cursor: string) {
  if (!enabled()) return closed();
  try {
    const threads = parseThreadPage(await api.listRoomThreads(roomId, cursor));
    if (!threads) return { ok: false as const, message: "The thread page was not usable." };
    return { ok: true as const, threads: await hydrateThreads(threads.threads), nextCursor: threads.nextCursor };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function loadMoreCandidatesAction(roomId: string, cursor: string) {
  if (!enabled()) return closed();
  try {
    const candidates = parseCandidatePage(await api.listRoomCandidates(roomId, cursor));
    if (!candidates) return { ok: false as const, message: "The candidate page was not usable." };
    return {
      ok: true as const,
      candidates: await hydrateCandidates(roomId, candidates.candidates),
      nextCursor: candidates.nextCursor,
    };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function loadCandidateDiffAction(roomId: string, candidateId: string) {
  if (!enabled()) return closed();
  try {
    const parsed = parseCandidateDiff(await api.getCandidateDiff(roomId, candidateId));
    if (!parsed) return { ok: false as const, unverified: false, message: "The diff was not usable." };
    if (!("files" in parsed) || parsed.candidateId !== candidateId) {
      return { ok: false as const, unverified: true, message: "Cannot verify this change." };
    }
    return { ok: true as const, diff: parsed };
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      return { ok: false as const, unverified: true, message: "Cannot verify this change." };
    }
    return { ok: false as const, unverified: false, message: messageOf(error) };
  }
}

export async function loadSiteCommentsAction(siteId: string, cursor?: string) {
  if (!enabled()) return closed();
  try {
    const page = parseSiteCommentPage(await api.listSiteCommentPages(siteId, cursor));
    if (!page) return { ok: false as const, message: "The site comment page was not usable." };
    return { ok: true as const, items: page.items, nextCursor: page.nextCursor };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function addMemberAction(roomId: string, userId: string, role: "editor" | "reviewer" | "viewer") {
  if (!enabled()) return closed();
  try {
    await api.addProjectRoomMember(roomId, userId, role);
    return { ok: true as const };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function attachSiteAction(roomId: string, siteId: string) {
  if (!enabled()) return closed();
  try {
    await api.addProjectRoomSite(roomId, siteId);
    return { ok: true as const };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function detachSiteAction(roomId: string, siteId: string) {
  if (!enabled()) return closed();
  try {
    await api.removeProjectRoomSite(roomId, siteId);
    return { ok: true as const };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function resolveAction(roomId: string, threadId: string) {
  if (!enabled()) return closed();
  try {
    const thread = parseThreadDetail(await api.resolveReviewThread(roomId, threadId));
    if (!thread) return { ok: false as const, message: "The resolved thread was not usable." };
    return { ok: true as const, thread };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function reanchorAction(roomId: string, threadId: string, reason: string) {
  if (!enabled()) return closed();
  try {
    const thread = parseThreadDetail(await api.reanchorReviewThread(roomId, threadId, {
      kind: "page",
      anchor: {},
      anchorState: "anchored",
      reason,
    }));
    if (!thread) return { ok: false as const, message: "The reanchored thread was not usable." };
    return { ok: true as const, thread };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function submitCandidateAction(input: {
  roomId: string;
  siteId: string;
  baseVersionId: string;
  contentHash: string;
  diffHash: string;
  summary: string;
}) {
  if (!enabled()) return closed();
  try {
    const body = await api.submitProjectRoomCandidate(input);
    const row = body && typeof body === "object" && "candidate" in body ? (body as { candidate: unknown }).candidate : body;
    const candidate = parseCandidate(row);
    if (!candidate) return { ok: false as const, message: "The candidate response was not usable." };
    return { ok: true as const, candidate };
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
