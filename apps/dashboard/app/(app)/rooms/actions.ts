"use server";

import { createHash } from "node:crypto";
import { prepareStaticSnapshot, verifiedRasterDataURL } from "@/lib/collab/static-renderer";
import { snapshotPagePath } from "@/lib/collab/static-snapshot";
import { isCollabReviewEnabled } from "@/lib/collab";
import { ApiError, api } from "@/lib/api";
import {
  bindApproval,
  canEditRoomSite,
  parseCandidate,
  parseCandidateDiff,
  parseCandidatePage,
  parseRoom,
  parseRoomDetail,
  parseRoomActivity,
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
  kind: "page" | "point" | "text" | "component";
  anchor: unknown;
  body: string;
}) {
  if (!enabled()) return closed();
  if (!["page", "point", "text", "component"].includes(input.kind)) {
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

export async function reanchorAction(roomId: string, threadId: string, reason: string, selection?: { kind: "point" | "text" | "component"; anchor: Record<string, unknown> }) {
  if (!enabled()) return closed();
  try {
    const thread = parseThreadDetail(await api.reanchorReviewThread(roomId, threadId, {
      kind: selection?.kind ?? "page",
      anchor: selection?.anchor ?? {},
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
  if (!binding || candidate.RoomID !== roomId) return { ok: false as const, message: "The candidate identity is incomplete." };
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

export async function updateMemberAction(roomId: string, userId: string, role: "editor" | "reviewer" | "viewer") {
  if (!enabled()) return closed();
  try {
    await api.updateProjectRoomMember(roomId, userId, role);
    return { ok: true as const };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function removeMemberAction(roomId: string, userId: string) {
  if (!enabled()) return closed();
  try {
    await api.removeProjectRoomMember(roomId, userId);
    return { ok: true as const };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function loadRoomSitesAction() {
  if (!enabled()) return closed();
  try {
    const [sites, me] = await Promise.all([api.listSites(), api.me()]);
    return { ok: true as const, sites: sites.flatMap((site) => site.id && site.slug ? [{
      canEditContent: canEditRoomSite(site, me),
      id: site.id, slug: site.slug, liveUrl: site.live_url ?? null,
      currentVersionId: site.current_version_id ?? null,
      currentContentHash: (site as { current_content_hash?: string }).current_content_hash ?? null,
    }] : []) };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function applyCandidateAction(roomId: string, candidate: ReviewCandidate) {
  if (!enabled()) return closed();
  const binding = bindApproval(candidate);
  if (!binding || candidate.RoomID !== roomId || candidate.State !== "approved") {
    return { ok: false as const, message: "An approved candidate with a complete identity is required." };
  }
  try {
    const body = await api.applyProjectRoomCandidate(roomId, candidate.ID, binding);
    const result = body as { candidate?: unknown; applied?: unknown; changed?: unknown; version_id?: unknown; live_url?: unknown } | null;
    const parsed = parseCandidate(result?.candidate);
    if (!parsed || parsed.ID !== candidate.ID || parsed.RoomID !== roomId || parsed.ContentHash !== candidate.ContentHash || parsed.SiteID !== candidate.SiteID || parsed.BaseVersionID !== candidate.BaseVersionID || parsed.BaseContentHash !== candidate.BaseContentHash || result?.applied !== true || typeof result.changed !== "boolean" || typeof result.version_id !== "string") {
      return { ok: false as const, message: "The apply response was not usable. Refresh the room before retrying." };
    }
    return { ok: true as const, candidate: parsed, changed: result.changed, versionId: result.version_id };
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      return { ok: false as const, message: "Cannot apply this change. The candidate or live base changed, or its content could not be verified. Refresh and review the current candidate." };
    }
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function loadRoomActivityAction(roomId: string, cursor?: string) {
  if (!enabled()) return closed();
  try {
    const activity = parseRoomActivity(await api.listRoomActivity(roomId, cursor));
    if (!activity) return { ok: false as const, message: "The activity response was not usable." };
    return { ok: true as const, ...activity };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function markRoomReadAction(roomId: string) {
  if (!enabled()) return closed();
  try {
    await api.markRoomRead(roomId);
    return { ok: true as const };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function loadThreadLinkAction(roomId: string, threadId: string, cursor?: string) {
  if (!enabled()) return closed();
  try {
    const thread = parseThreadDetail(await api.getReviewThread(threadId, cursor));
    if (!thread || thread.RoomID !== roomId) return { ok: false as const, message: "This thread is not available in this room." };
    return { ok: true as const, thread };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}

export async function loadThreadRevisionsAction(roomId: string, threadId: string, revisionsCursor: string) {
  if (!enabled()) return closed();
  try {
    const thread = parseThreadDetail(await api.getReviewThread(threadId, undefined, revisionsCursor));
    if (!thread || thread.ID !== threadId || thread.RoomID !== roomId) return { ok: false as const, message: "This anchor history is not available in this room." };
    return { ok: true as const, thread };
  } catch (error) {
    return { ok: false as const, message: messageOf(error) };
  }
}


function verifiedSnapshotFile(value: unknown, siteId: string, versionId: string, path: string) {
  if (!value || typeof value !== "object") throw new Error("Invalid snapshot file.");
  const row = value as Record<string, unknown>;
  if (row.site_id !== siteId || row.version_id !== versionId || row.path !== path || typeof row.content_base64 !== "string"
    || typeof row.content_type !== "string" || typeof row.sha256 !== "string" || typeof row.content_hash !== "string"
    || !/^[0-9a-f]{64}$/.test(row.content_hash) || row.content_base64.length > 2800000) throw new Error("Invalid snapshot identity.");
  const bytes = Buffer.from(row.content_base64, "base64");
  if (bytes.toString("base64") !== row.content_base64 || createHash("sha256").update(bytes).digest("hex") !== row.sha256) throw new Error("Snapshot verification failed.");
  return { bytes, contentType: row.content_type, contentHash: row.content_hash };
}

export async function loadStaticSnapshotAction(roomId: string, siteId: string, versionId: string, page: string) {
  if (!enabled()) return closed();
  const path = snapshotPagePath(page);
  if (!path) return { ok: false as const, message: "Choose a file path inside this version." };
  try {
    const file = verifiedSnapshotFile(await api.getRoomVersionFile(roomId, siteId, versionId, path), siteId, versionId, path);
    const mime = file.contentType.split(";")[0]!.trim().toLowerCase();
    if (mime !== "text/html" && mime !== "application/xhtml+xml") return { ok: false as const, message: "Static review currently supports HTML pages. Other files keep page comments." };
    const html = new TextDecoder("utf-8", { fatal: true }).decode(file.bytes);
    const prepared = prepareStaticSnapshot(html, `/${path}`);
    const images = new Map<string, string>();
    await Promise.all(prepared.imagePaths.slice(0, 8).map(async (assetPath) => {
      try {
        const asset = verifiedSnapshotFile(await api.getRoomVersionFile(roomId, siteId, versionId, assetPath), siteId, versionId, assetPath);
        if (asset.contentHash !== file.contentHash) return;
        const url = verifiedRasterDataURL(asset.bytes, asset.contentType);
        if (url) images.set(assetPath, url);
      } catch { /* Missing, oversized or unsupported assets stay visibly omitted. */ }
    }));
    return { ok: true as const, snapshot: prepared.render(images, { siteId, versionId, contentHash: file.contentHash, page }) };
  } catch {
    // Never return source bytes, object-store details, or raw parser errors.
    return { ok: false as const, message: "This immutable page could not be verified or is too large for static review. Page comments remain available." };
  }
}
