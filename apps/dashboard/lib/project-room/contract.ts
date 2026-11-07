// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Dashboard view of the project-room HTTP contract in
// services/api/internal/projectroom/API.md and the handlers that implement it.
// Response objects use encoding/json's default Go field names. Request bodies
// use the snake_case fields those handlers decode. Routes that are not
// registered are listed here and are not called.

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

export const UNROUTED = [
  "GET /v1/project-rooms/{roomID}",
  "GET /v1/project-rooms/{roomID}/threads",
  "GET /v1/project-rooms/{roomID}/candidates",
  "POST /v1/project-rooms/{roomID}/members",
  "POST /v1/project-rooms/{roomID}/sites",
  "POST reanchor",
  "POST resolve",
] as const;

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

export function parseRoom(value: unknown): ProjectRoom | null {
  const row = record(value);
  if (!row) return null;
  const id = str(row.ID);
  const org = str(row.OrgID);
  const name = str(row.Name);
  const owner = str(row.OwnerUserID);
  const role = str(row.Role);
  if (!id || !org || !name || !owner || !role) return null;
  return {
    ID: id,
    OrgID: org,
    Name: name,
    OwnerUserID: owner,
    Role: role,
    CreatedAt: typeof row.CreatedAt === "string" ? row.CreatedAt : "",
    UpdatedAt: typeof row.UpdatedAt === "string" ? row.UpdatedAt : "",
    Unread: typeof row.Unread === "number" ? row.Unread : 0,
  };
}

export function parseCandidate(value: unknown): ReviewCandidate | null {
  const row = record(value);
  if (!row) return null;
  const id = str(row.ID);
  const room = str(row.RoomID);
  const site = str(row.SiteID);
  const baseVersion = str(row.BaseVersionID);
  const baseHash = str(row.BaseContentHash);
  const hash = str(row.ContentHash);
  const diff = str(row.DiffHash);
  const summary = typeof row.Summary === "string" ? row.Summary : null;
  const state = str(row.State);
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
    Summary: summary,
    State: state,
    CreatedBy: typeof row.CreatedBy === "string" ? row.CreatedBy : "",
    CreatedAt: typeof row.CreatedAt === "string" ? row.CreatedAt : "",
  };
}

export function parseThread(value: unknown): ReviewThread | null {
  const row = record(value);
  if (!row) return null;
  const id = str(row.ID);
  const room = str(row.RoomID);
  const site = str(row.SiteID);
  const version = str(row.VersionID);
  const hash = str(row.ContentHash);
  const page = str(row.Page);
  const kind = str(row.AnchorKind);
  if (!id || !room || !site || !version || !hash || !page || !kind) return null;
  const replies = Array.isArray(row.Replies)
    ? row.Replies.flatMap((item) => {
        const reply = record(item);
        if (!reply || !str(reply.ID) || typeof reply.Body !== "string") return [];
        return [{
          ID: String(reply.ID),
          AuthorID: typeof reply.AuthorID === "string" ? reply.AuthorID : "",
          Body: reply.Body,
          CreatedAt: typeof reply.CreatedAt === "string" ? reply.CreatedAt : "",
        }];
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
    Anchor: row.Anchor ?? null,
    AnchorState: typeof row.AnchorState === "string" ? row.AnchorState : "",
    Status: typeof row.Status === "string" ? row.Status : "",
    CreatedBy: typeof row.CreatedBy === "string" ? row.CreatedBy : "",
    CreatedAt: typeof row.CreatedAt === "string" ? row.CreatedAt : "",
    Replies: replies,
  };
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
    return { status: "stale", reason: "The live version is not this candidate's base version." };
  }
  if (!live.contentHash) {
    return { status: "unknown", reason: "The live content hash was not in the site list. The request still sends this candidate's base hash." };
  }
  if (live.contentHash !== candidate.BaseContentHash) {
    return { status: "stale", reason: "The live content hash is not this candidate's base hash." };
  }
  return { status: "matches" };
}

export function decisionPath(roomId: string, candidateId: string, decision: "approve" | "reject"): string {
  return `/v1/project-rooms/${encodeURIComponent(roomId)}/candidates/${encodeURIComponent(candidateId)}/${decision}`;
}

export function terminalCopy(state: string): string | null {
  if (state === "rejected") return "Rejected. That decision is terminal and was not applied.";
  if (state === "conflict") return "Conflict. The live baseline did not match, so the change was not applied.";
  if (state === "approved") return "Approved. Application is still disabled, and repeating approval does not write a second effect.";
  return null;
}
