// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Dashboard view of the project-room HTTP contract in
// services/api/internal/projectroom/API.md and the handlers that implement it.

import type { operations } from "@dropway/sdk/schema";

type VerifiedDiffBody = operations["getCandidateDiff"]["responses"][200]["content"]["application/json"];
type DiffChange = VerifiedDiffBody["files"][number]["change"];

/** Comment page shape from the regenerated SDK schema. Bodies stay untrusted. */
export type SiteCommentPage = operations["listSiteCommentPage"]["responses"][200]["content"]["application/json"];
export type RoomPageQuery = NonNullable<operations["listRoomThreads"]["parameters"]["query"]>;
// GET and POST /v1/project-rooms still use encoding/json's Go field names.
// The newer read and management responses use snake_case. Comment bodies and
// candidate summaries are untrusted and must be rendered as text.

export const ANCHOR_PRODUCT_GATE = ["point", "text", "component", "page"] as const;

/** Precise selection inside opaque sandboxed HTML is not shipped. */
export function shippedPreciseHtmlSelection(): false {
  return false;
}

export type RoomRole = "owner" | "editor" | "reviewer" | "viewer";

export interface SiteRights {
  canRead: boolean;
  canEditContent: boolean;
}

export interface ProjectRoom {
  ID: string;
  OrgID: string;
  Name: string;
  OwnerUserID: string;
  Role: string;
  CreatedAt: string;
  UpdatedAt: string;
  Unread: number;
}

export interface ReviewReply {
  ID: string;
  AuthorID: string;
  Body: string;
  CreatedAt: string;
}

export interface ReviewThread {
  NextReplyCursor?: string | null;
  NextRevisionCursor?: string | null;
  Revisions?: AnchorRevision[];
  ID: string;
  RoomID: string;
  SiteID: string;
  VersionID: string;
  ContentHash: string;
  Page: string;
  AnchorKind: string;
  Anchor: unknown;
  AnchorState: string;
  Status: string;
  CreatedBy: string;
  CreatedAt: string;
  Replies: ReviewReply[];
}

export interface VersionSignoff {
  SiteID: string;
  VersionID: string;
  ContentHash: string;
  UserID: string;
  CreatedAt: string;
}

export interface ReviewCandidate {
  AppliedVersionID?: string | null;
  ID: string;
  RoomID: string;
  SiteID: string;
  BaseVersionID: string;
  BaseContentHash: string;
  ContentHash: string;
  DiffHash: string;
  Summary: string;
  State: string;
  CreatedBy: string;
  CreatedAt: string;
}

export interface CandidateDecisionBody {
  candidate: ReviewCandidate;
  changed: boolean;
  decision: "approve_change" | "reject_change";
}

export interface ApprovalBinding {
  content_hash: string;
  base_version_id: string;
  base_content_hash: string;
}

export interface LiveBaseline {
  versionId: string | null;
  contentHash: string | null;
}

export type LiveCheck =
  | { status: "matches" }
  | { status: "stale"; reason: string }
  | { status: "unknown"; reason: string };

export const EXECUTION_BLOCKERS = [
  "credential_store_unavailable",
  "pi_sdk_unverified",
  "sprites_execution_disabled",
  "session_persistence_deferred",
] as const;

export const CANNOT_VERIFY = "Cannot verify this change.";
export const NEEDS_NEW_CANDIDATE = "This base is stale and needs a new candidate.";

export const PAGE_LIMIT_DEFAULT = 50;
export const PAGE_LIMIT_MAX = 100;

export interface RoomMemberRow {
  userId: string;
  role: string;
}

export interface RoomSiteRef {
  siteId: string;
  createdBy: string;
}

export interface RoomDetail {
  room: ProjectRoom;
  members: RoomMemberRow[];
  sites: RoomSiteRef[];
}

export interface AnchorRevision {
  id: string;
  anchorKind: string;
  page: string;
  anchorState: string;
  reason: string;
  createdAt: string;
}

export function parseRole(value: string): RoomRole | null {
  if (value === "owner" || value === "editor" || value === "reviewer" || value === "viewer") {
    return value;
  }
  return null;
}

export function canComment(role: RoomRole, site: SiteRights): boolean {
  if (!site.canRead) return false;
  return role === "owner" || role === "editor" || role === "reviewer";
}

export function canSignOffVersion(role: RoomRole, site: SiteRights): boolean {
  return canComment(role, site);
}

export function canDecideChange(role: RoomRole, site: SiteRights): boolean {
  if (!site.canEditContent) return false;
  return role === "owner" || role === "editor";
}

export function canManageMembers(role: RoomRole): boolean {
  return role === "owner";
}

export function creatableAnchorKinds(surface: "sandboxed-html" | "image"): readonly ("page" | "point")[] {
  if (surface === "image") return ["point", "page"];
  return ["page"];
}

export function pageCommentAnchor(): { kind: "page"; anchor: Record<string, never> } {
  return { kind: "page", anchor: {} };
}

export interface ImagePinInput {
  normalizedX: number;
  normalizedY: number;
  viewportWidth: number;
  viewportHeight: number;
  renditionWidth: number;
  renditionHeight: number;
  scrollX: number;
  scrollY: number;
  intrinsicWidth: number;
  intrinsicHeight: number;
  imageX: number;
  imageY: number;
}

function unit(value: number): boolean {
  return Number.isFinite(value) && value >= 0 && value <= 1;
}

function box(value: number): boolean {
  return Number.isInteger(value) && value > 0 && value <= 100000;
}

export function imageCommentAnchor(
  input: ImagePinInput,
):
  | { ok: true; kind: "point"; anchor: Record<string, unknown> }
  | { ok: false; reason: string } {
  if (!unit(input.normalizedX) || !unit(input.normalizedY)) {
    return { ok: false, reason: "normalized coordinates" };
  }
  if (!box(input.viewportWidth) || !box(input.viewportHeight) || !box(input.renditionWidth) || !box(input.renditionHeight)) {
    return { ok: false, reason: "viewport" };
  }
  if (!Number.isInteger(input.scrollX) || !Number.isInteger(input.scrollY) || input.scrollX < 0 || input.scrollY < 0) {
    return { ok: false, reason: "scroll" };
  }
  if (
    !box(input.intrinsicWidth) ||
    !box(input.intrinsicHeight) ||
    !Number.isInteger(input.imageX) ||
    !Number.isInteger(input.imageY) ||
    input.imageX < 0 ||
    input.imageY < 0 ||
    input.imageX > input.intrinsicWidth ||
    input.imageY > input.intrinsicHeight
  ) {
    return { ok: false, reason: "image coordinates" };
  }
  return {
    ok: true,
    kind: "point",
    anchor: {
      x: input.normalizedX,
      y: input.normalizedY,
      viewport: { width: input.viewportWidth, height: input.viewportHeight },
      rendition: { width: input.renditionWidth, height: input.renditionHeight },
      scroll: { x: input.scrollX, y: input.scrollY },
      image: {
        intrinsic_width: input.intrinsicWidth,
        intrinsic_height: input.intrinsicHeight,
        x: input.imageX,
        y: input.imageY,
      },
    },
  };
}

function record(value: unknown): Record<string, unknown> | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function str(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function field(row: Record<string, unknown>, goName: string, snake: string): unknown {
  if (row[snake] !== undefined && row[snake] !== null) return row[snake];
  return row[goName];
}

/** Default 50, maximum 100, matching the room page clamp. */
export function pageQuery(cursor?: string, limit = PAGE_LIMIT_DEFAULT): string {
  const bounded = Math.min(PAGE_LIMIT_MAX, Math.max(1, Math.trunc(limit) || PAGE_LIMIT_DEFAULT));
  const params = new URLSearchParams({ limit: String(bounded) });
  if (cursor) params.set("cursor", cursor);
  return params.toString();
}

/** Comment and summary strings are data. Callers render the returned string as text. */
export function untrustedText(value: unknown): string {
  return typeof value === "string" ? value : "";
}

export function parseRoom(value: unknown): ProjectRoom | null {
  const row = record(value);
  if (!row) return null;
  const id = str(field(row, "ID", "id"));
  const org = str(field(row, "OrgID", "org_id"));
  const name = str(field(row, "Name", "name"));
  const owner = str(field(row, "OwnerUserID", "owner_user_id"));
  const role = str(field(row, "Role", "role"));
  if (!id || !org || !name || !owner || !role) return null;
  const created = field(row, "CreatedAt", "created_at");
  const updated = field(row, "UpdatedAt", "updated_at");
  const unread = field(row, "Unread", "unread");
  return {
    ID: id,
    OrgID: org,
    Name: name,
    OwnerUserID: owner,
    Role: role,
    CreatedAt: typeof created === "string" ? created : "",
    UpdatedAt: typeof updated === "string" ? updated : "",
    Unread: typeof unread === "number" ? unread : 0,
  };
}

export function parseRoomDetail(value: unknown): RoomDetail | null {
  const row = record(value);
  if (!row) return null;
  const room = parseRoom(row.room);
  if (!room) return null;
  const members = Array.isArray(row.members)
    ? row.members.flatMap((item) => {
        const member = record(item);
        const userId = member ? str(member.user_id) : null;
        const role = member ? str(member.role) : null;
        return userId && role ? [{ userId, role }] : [];
      })
    : [];
  const sites = Array.isArray(row.sites)
    ? row.sites.flatMap((item) => {
        const site = record(item);
        const siteId = site ? str(site.site_id) : null;
        if (!siteId) return [];
        return [{ siteId, createdBy: site && typeof site.created_by === "string" ? site.created_by : "" }];
      })
    : [];
  return { room, members, sites };
}

export function parseCandidate(value: unknown): ReviewCandidate | null {
  const row = record(value);
  if (!row) return null;
  const id = str(field(row, "ID", "id"));
  const room = str(field(row, "RoomID", "room_id"));
  const site = str(field(row, "SiteID", "site_id"));
  const baseVersion = str(field(row, "BaseVersionID", "base_version_id"));
  const baseHash = str(field(row, "BaseContentHash", "base_content_hash"));
  const hash = str(field(row, "ContentHash", "content_hash"));
  const diff = str(field(row, "DiffHash", "diff_hash"));
  const summaryRaw = field(row, "Summary", "summary");
  const summary = typeof summaryRaw === "string" ? summaryRaw : null;
  const state = str(field(row, "State", "state"));
  if (!id || !room || !site || !baseVersion || !baseHash || !hash || !diff || summary === null || !state) {
    return null;
  }
  return {
    ID: id,
    RoomID: room,
    SiteID: site,
    BaseVersionID: baseVersion,
    BaseContentHash: baseHash,
    ContentHash: hash,
    DiffHash: diff,
    AppliedVersionID: str(field(row, "AppliedVersionID", "applied_version_id")),
    Summary: summary,
    State: state,
    CreatedBy: typeof field(row, "CreatedBy", "created_by") === "string" ? String(field(row, "CreatedBy", "created_by")) : "",
    CreatedAt: typeof field(row, "CreatedAt", "created_at") === "string" ? String(field(row, "CreatedAt", "created_at")) : "",
  };
}

export function parseThread(value: unknown): ReviewThread | null {
  const row = record(value);
  if (!row) return null;
  const id = str(field(row, "ID", "id"));
  const room = str(field(row, "RoomID", "room_id"));
  const site = str(field(row, "SiteID", "site_id"));
  const version = str(field(row, "VersionID", "version_id"));
  const hash = str(field(row, "ContentHash", "content_hash"));
  const page = str(field(row, "Page", "page"));
  const kind = str(field(row, "AnchorKind", "anchor_kind"));
  if (!id || !room || !site || !version || !hash || !page || !kind) return null;
  const replyRows = Array.isArray(row.Replies) ? row.Replies : row.replies;
  const replies = Array.isArray(replyRows)
    ? replyRows.flatMap((item) => {
        const reply = parseReply(item);
        return reply ? [reply] : [];
      })
    : [];
  return {
    ID: id,
    RoomID: room,
    SiteID: site,
    VersionID: version,
    ContentHash: hash,
    Page: page,
    AnchorKind: kind,
    Anchor: field(row, "Anchor", "anchor") ?? null,
    AnchorState: typeof field(row, "AnchorState", "anchor_state") === "string" ? String(field(row, "AnchorState", "anchor_state")) : "",
    Status: typeof field(row, "Status", "status") === "string" ? String(field(row, "Status", "status")) : "",
    CreatedBy: typeof field(row, "CreatedBy", "created_by") === "string" ? String(field(row, "CreatedBy", "created_by")) : "",
    CreatedAt: typeof field(row, "CreatedAt", "created_at") === "string" ? String(field(row, "CreatedAt", "created_at")) : "",
    Replies: replies,
    NextReplyCursor: str(row.next_cursor),
    NextRevisionCursor: str(row.next_revision_cursor),
    Revisions: Array.isArray(row.revisions) ? row.revisions.flatMap((item) => {
      const revision = record(item);
      if (!revision || !str(revision.id) || !str(revision.anchor_kind) || !str(revision.page) || !str(revision.anchor_state) || typeof revision.reason !== "string" || !str(revision.created_at)) return [];
      return [{ id: String(revision.id), anchorKind: String(revision.anchor_kind), page: String(revision.page),
        anchorState: String(revision.anchor_state), reason: revision.reason, createdAt: String(revision.created_at) }];
    }) : [],
  };
}

export function parseReply(value: unknown): ReviewReply | null {
  const reply = record(value);
  if (!reply) return null;
  const id = str(field(reply, "ID", "id"));
  const body = field(reply, "Body", "body");
  if (!id || typeof body !== "string") return null;
  const author = field(reply, "AuthorID", "author_id");
  const created = field(reply, "CreatedAt", "created_at");
  return {
    ID: id,
    AuthorID: typeof author === "string" ? author : "",
    Body: body,
    CreatedAt: typeof created === "string" ? created : "",
  };
}

export function parseThreadPage(value: unknown): { threads: ReviewThread[]; nextCursor: string | null } | null {
  const row = record(value);
  if (!row || !Array.isArray(row.threads)) return null;
  return {
    threads: row.threads.flatMap((item) => {
      const thread = parseThread(item);
      return thread ? [thread] : [];
    }),
    nextCursor: typeof row.next_cursor === "string" && row.next_cursor.length > 0 ? row.next_cursor : null,
  };
}

export interface SiteCommentItem {
  source: string;
  id: string;
  createdAt: string;
  body: string;
  page: string;
  anchorKind: string;
  status: string;
  versionId: string;
  contentHash: string;
}

/** Site comment page. comment_content must mark the bodies as untrusted data. */
export function parseSiteCommentPage(value: unknown): { items: SiteCommentItem[]; nextCursor: string | null } | null {
  const row = record(value);
  if (!row || !Array.isArray(row.items) || row.comment_content !== "untrusted_data") return null;
  return {
    items: row.items.flatMap((item) => {
      const entry = record(item);
      if (!entry) return [];
      const id = str(entry.id);
      const source = str(entry.source);
      const created = str(entry.created_at);
      if (!id || !source || !created) return [];
      return [{
        source,
        id,
        createdAt: created,
        body: typeof entry.body === "string" ? entry.body : "",
        page: typeof entry.page === "string" ? entry.page : "",
        anchorKind: typeof entry.anchor_kind === "string" ? entry.anchor_kind : "",
        status: typeof entry.status === "string" ? entry.status : "",
        versionId: typeof entry.version_id === "string" ? entry.version_id : "",
        contentHash: typeof entry.content_hash === "string" ? entry.content_hash : "",
      }];
    }),
    nextCursor: typeof row.next_cursor === "string" && row.next_cursor.length > 0 ? row.next_cursor : null,
  };
}

export function parseCandidatePage(value: unknown): { candidates: ReviewCandidate[]; nextCursor: string | null } | null {
  const row = record(value);
  if (!row || !Array.isArray(row.candidates)) return null;
  return {
    candidates: row.candidates.flatMap((item) => {
      const candidate = parseCandidate(item);
      return candidate ? [candidate] : [];
    }),
    nextCursor: typeof row.next_cursor === "string" && row.next_cursor.length > 0 ? row.next_cursor : null,
  };
}

export function parseThreadDetail(value: unknown): ReviewThread | null {
  const row = record(value);
  if (!row) return null;
  return parseThread(row.thread ?? row);
}

export interface CandidateDiffFile {
  path: string;
  change: DiffChange;
  binary: boolean;
  beforeSHA256: string;
  afterSHA256: string;
  beforeSize: number | null;
  afterSize: number | null;
  before: string;
  after: string;
  beforeTruncated: boolean;
  afterTruncated: boolean;
}

export interface CandidateDiff {
  candidateId: string;
  siteId: string;
  baseVersionId: string;
  baseContentHash: string;
  contentHash: string;
  verified: true;
  truncated: boolean;
  omittedFiles: number;
  files: CandidateDiffFile[];
}

export type CandidateDiffView =
  | { status: "loading" }
  | { status: "verified"; diff: CandidateDiff }
  | { status: "unverified" }
  | { status: "unavailable" };

const diffChanges = new Set<DiffChange>(["added", "removed", "modified", "unchanged"]);

function diffChange(value: unknown): DiffChange | null {
  return typeof value === "string" && diffChanges.has(value as DiffChange) ? value as DiffChange : null;
}

function optionalSize(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

/** A verified diff, an explicit verification failure, or null when the body is not a diff. */
export function parseCandidateDiff(value: unknown): CandidateDiff | { verified: false } | null {
  const row = record(value);
  if (!row) return null;
  if (row.verified === false) return { verified: false };
  if (row.verified !== true || row.comment_content !== "untrusted_data" || !Array.isArray(row.files)) return null;
  const candidateId = str(row.candidate_id);
  const baseVersionId = str(row.base_version_id);
  const baseContentHash = str(row.base_content_hash);
  const contentHash = str(row.content_hash);
  if (!candidateId || !baseVersionId || !baseContentHash || !contentHash) return { verified: false };
  const files: CandidateDiffFile[] = [];
  for (const item of row.files) {
    const file = record(item);
    const change = file ? diffChange(file.change) : null;
    const path = file ? str(file.path) : null;
    const binary = file?.binary;
    if (!file || !change || !path || (binary !== true && binary !== false) || file.untrusted !== true) {
      return { verified: false };
    }
    files.push({
      path,
      change,
      binary,
      beforeSHA256: typeof file.before_sha256 === "string" ? file.before_sha256 : "",
      afterSHA256: typeof file.after_sha256 === "string" ? file.after_sha256 : "",
      beforeSize: optionalSize(file.before_size),
      afterSize: optionalSize(file.after_size),
      before: file.binary || typeof file.before !== "string" ? "" : file.before,
      after: file.binary || typeof file.after !== "string" ? "" : file.after,
      beforeTruncated: file.before_truncated === true,
      afterTruncated: file.after_truncated === true,
    });
  }
  return {
    candidateId,
    siteId: typeof row.site_id === "string" ? row.site_id : "",
    baseVersionId,
    baseContentHash,
    contentHash,
    verified: true,
    truncated: row.truncated === true,
    omittedFiles: typeof row.omitted_files === "number" ? row.omitted_files : 0,
    files,
  };
}

export function diffMatchesCandidate(diff: CandidateDiff, candidate: ReviewCandidate): boolean {
  return diff.verified
    && diff.siteId === candidate.SiteID
    && diff.candidateId === candidate.ID
    && diff.baseVersionId === candidate.BaseVersionID
    && diff.baseContentHash === candidate.BaseContentHash
    && diff.contentHash === candidate.ContentHash;
}

/** The approve/reject body. It copies the candidate the API returned. It does not take the live baseline. */
export function bindApproval(candidate: ReviewCandidate): ApprovalBinding | null {
  if (!candidate.ID || !candidate.ContentHash || !candidate.BaseVersionID || !candidate.BaseContentHash) {
    return null;
  }
  return {
    content_hash: candidate.ContentHash,
    base_version_id: candidate.BaseVersionID,
    base_content_hash: candidate.BaseContentHash,
  };
}

export function compareLive(candidate: ReviewCandidate, live: LiveBaseline | null): LiveCheck {
  if (!live || !live.versionId) {
    return { status: "unknown", reason: "The site list did not include a live version id." };
  }
  if (live.versionId !== candidate.BaseVersionID) {
    return { status: "stale", reason: `The live version is not this candidate's base version. ${NEEDS_NEW_CANDIDATE}` };
  }
  if (!live.contentHash) {
    return { status: "unknown", reason: "The live content hash was not in the site list. The request still sends this candidate's base hash." };
  }
  if (live.contentHash !== candidate.BaseContentHash) {
    return { status: "stale", reason: `The live content hash is not this candidate's base hash. ${NEEDS_NEW_CANDIDATE}` };
  }
  return { status: "matches" };
}

export function decisionPath(roomId: string, candidateId: string, decision: "approve" | "reject"): string {
  return `/v1/project-rooms/${encodeURIComponent(roomId)}/candidates/${encodeURIComponent(candidateId)}/${decision}`;
}

export function terminalCopy(state: string): string | null {
  if (state === "rejected") return "Rejected. That decision is terminal and was not applied.";
  if (state === "conflict") return "Conflict. The live baseline did not match, so the change was not applied.";
  if (state === "applied") return "Applied. This approved change was published.";
  if (state === "approved") return "Approved. Ready to apply after verifying the diff and live base.";
  return null;
}

export interface RoomActivity {
  id: string;
  actorUserId: string;
  kind: string;
  threadId: string | null;
  siteId: string | null;
  versionId: string | null;
  createdAt: string;
}

export function parseRoomActivity(value: unknown): { items: RoomActivity[]; nextCursor: string | null; unread: number } | null {
  const row = record(value);
  if (!row || !Array.isArray(row.items)) return null;
  const items: RoomActivity[] = [];
  for (const item of row.items) {
    const entry = record(item);
    if (!entry || !str(entry.id) || !str(entry.kind) || !str(entry.created_at)) return null;
    items.push({ id: String(entry.id), actorUserId: typeof entry.actor_user_id === "string" ? entry.actor_user_id : "",
      kind: String(entry.kind), threadId: str(entry.thread_id), siteId: str(entry.site_id), versionId: str(entry.version_id), createdAt: String(entry.created_at) });
  }
  return { items, nextCursor: str(row.next_cursor), unread: typeof row.unread === "number" ? row.unread : 0 };
}

/** Room membership never grants the site's independent content-edit permission. */
export function canEditRoomSite(site: { owner_id?: string; allow_member_edits?: boolean }, me: { user_id?: string; role?: string }): boolean {
  return Boolean(me.user_id) && (site.allow_member_edits === true || site.owner_id === me.user_id || me.role === "admin" || me.role === "owner");
}

/** Resolve an immutable thread's page only within the already-approved content origin. */
export function threadPageUrl(liveUrl: string, page: string): string | null {
  if (!page.startsWith("/") || page.startsWith("//") || page.includes("\\")) return null;
  try {
    const base = new URL(liveUrl);
    const target = new URL(page, base);
    return target.origin === base.origin ? target.href : null;
  } catch { return null; }
}

/** Historical revisions never replace the immutable thread pin or its independently paged replies. */
export function appendThreadRevisions(thread: ReviewThread, page: ReviewThread): ReviewThread {
  if (thread.ID !== page.ID || thread.RoomID !== page.RoomID || thread.SiteID !== page.SiteID
    || thread.VersionID !== page.VersionID || thread.ContentHash !== page.ContentHash || thread.Page !== page.Page) return thread;
  const existing = thread.Revisions ?? [];
  const seen = new Set(existing.map((revision) => revision.id));
  return { ...thread, NextRevisionCursor: page.NextRevisionCursor,
    Revisions: [...existing, ...(page.Revisions ?? []).filter((revision) => !seen.has(revision.id))] };
}
