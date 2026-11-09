// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Room UI contract: role gates, the real anchor JSON, approval binding, and
// the markup that must stay honest about what this phase cannot do.

import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { CandidateReview } from "@/components/project-room/candidate-review";
import { RoomWorkspace } from "@/components/project-room/room-workspace";
import {
  bindApproval,
  canComment,
  canDecideChange,
  canSignOffVersion,
  compareLive,
  creatableAnchorKinds,
  imageCommentAnchor,
  pageCommentAnchor,
  pageQuery,
  parseCandidate,
  parseSiteCommentPage,
  shippedPreciseHtmlSelection,
  terminalCopy,
  type ReviewCandidate,
} from "@/lib/project-room/contract";

const candidate: ReviewCandidate = {
  ID: "cand-1",
  RoomID: "room-1",
  SiteID: "site-1",
  BaseVersionID: "ver-1",
  BaseContentHash: "hash-base",
  ContentHash: "hash-next",
  DiffHash: "hash-diff",
  Summary: "Replace the hero caption on the report page.",
  State: "pending",
  CreatedBy: "user-1",
  CreatedAt: "2026-11-07T00:00:00Z",
};

describe("project room contract", () => {
  it("lets reviewers comment and sign off, and only editing owners or editors decide a change", () => {
    const read = { canRead: true, canEditContent: false };
    const edit = { canRead: true, canEditContent: true };
    expect(canComment("reviewer", read)).toBe(true);
    expect(canComment("viewer", read)).toBe(false);
    expect(canSignOffVersion("reviewer", read)).toBe(true);
    expect(canDecideChange("reviewer", edit)).toBe(false);
    expect(canDecideChange("editor", edit)).toBe(true);
    expect(canDecideChange("editor", read)).toBe(false);
  });

  it("creates a page anchor with no position and an image pin in the API shape", () => {
    expect(pageCommentAnchor()).toEqual({ kind: "page", anchor: {} });
    expect(creatableAnchorKinds("sandboxed-html")).toEqual(["page"]);
    expect(creatableAnchorKinds("image")).toEqual(["point", "page"]);
    expect(shippedPreciseHtmlSelection()).toBe(false);
    const pin = imageCommentAnchor({
      normalizedX: 0.5,
      normalizedY: 0.25,
      viewportWidth: 200,
      viewportHeight: 100,
      renditionWidth: 200,
      renditionHeight: 100,
      scrollX: 0,
      scrollY: 0,
      intrinsicWidth: 400,
      intrinsicHeight: 200,
      imageX: 200,
      imageY: 50,
    });
    expect(pin.ok).toBe(true);
    if (!pin.ok) return;
    expect(pin.anchor).toMatchObject({
      x: 0.5,
      y: 0.25,
      image: { intrinsic_width: 400, intrinsic_height: 200, x: 200, y: 50 },
    });
    expect(imageCommentAnchor({ ...{
      normalizedX: 0.5, normalizedY: 0.25, viewportWidth: 10, viewportHeight: 10,
      renditionWidth: 10, renditionHeight: 10, scrollX: 0, scrollY: 0,
      intrinsicWidth: 10, intrinsicHeight: 10, imageX: 11, imageY: 0,
    } }).ok).toBe(false);
  });

  it("binds approval to the candidate and does not copy a newer live baseline", () => {
    expect(bindApproval(candidate)).toEqual({
      content_hash: "hash-next",
      base_version_id: "ver-1",
      base_content_hash: "hash-base",
    });
    expect(compareLive(candidate, { versionId: "ver-2", contentHash: "other" })).toMatchObject({
      status: "stale",
    });
    expect(compareLive(candidate, { versionId: "ver-1", contentHash: null }).status).toBe("unknown");
    expect(terminalCopy("rejected")).toMatch(/terminal/i);
    expect(terminalCopy("conflict")).toMatch(/not applied/i);
    const parsed = parseCandidate({
      ID: "cand-1",
      RoomID: "room-1",
      SiteID: "site-1",
      BaseVersionID: "ver-1",
      BaseContentHash: "hash-base",
      ContentHash: "hash-next",
      DiffHash: "hash-diff",
      Summary: "Replace the hero caption on the report page.",
      State: "conflict",
    });
    expect(parsed?.State).toBe("conflict");
    const snake = parseCandidate({
      id: "cand-2",
      room_id: "room-1",
      site_id: "site-1",
      base_version_id: "ver-9",
      base_content_hash: "base",
      content_hash: "next",
      diff_hash: "diff",
      summary: "<img src=x onerror=alert(1)>",
      state: "pending",
    });
    expect(snake?.ID).toBe("cand-2");
    expect(snake?.BaseVersionID).toBe("ver-9");
    expect(snake?.Summary).toBe("<img src=x onerror=alert(1)>");
    expect(pageQuery(undefined, 500)).toBe("limit=100");
    expect(pageQuery("abc", 0)).toBe("limit=50&cursor=abc");
    const comments = parseSiteCommentPage({
      comment_content: "untrusted_data",
      items: [{ source: "room", id: "c-1", created_at: "2026-11-09T00:00:00Z", body: "<b>caption</b>", untrusted: true }],
      next_cursor: "next",
    });
    expect(comments?.items[0]?.body).toBe("<b>caption</b>");
    expect(comments?.nextCursor).toBe("next");
    expect(parseSiteCommentPage({ items: [] })).toBeNull();
  });
});

describe("project room markup", () => {
  it("keeps Approve and Reject visible and does not offer precise HTML selection or a secret field", () => {
    const html = renderToStaticMarkup(
      createElement(CandidateReview, {
        candidate,
        live: { versionId: "ver-2", contentHash: "other" },
        canDecide: true,
        onDecide: () => undefined,
      }),
    );
    expect(html).toContain("Approve");
    expect(html).toContain("Reject");
    expect(html).toContain("Apply");
    expect(html).toContain("not enabled");
    expect(html).toContain("not this candidate");
    expect(html).not.toContain("type=\"password\"");
    expect(html).toContain("Replace the hero caption");

    const room = renderToStaticMarkup(
      createElement(RoomWorkspace, {
        rooms: [{
          ID: "room-1",
          OrgID: "org-1",
          Name: "Review",
          OwnerUserID: "user-1",
          Role: "reviewer",
          CreatedAt: "",
          UpdatedAt: "",
          Unread: 0,
        }],
        sites: [{ id: "site-1", slug: "report", liveUrl: "https://docs.example.com", currentVersionId: "ver-1" }],
        candidates: [candidate],
        parentOrigin: "https://app.dropway.dev",
        orgEnabled: true,
        onEnableRooms: () => undefined,
        threads: [{
          ID: "thread-1",
          RoomID: "room-1",
          SiteID: "site-1",
          VersionID: "ver-1",
          ContentHash: "hash-1",
          Page: "/index.html",
          AnchorKind: "page",
          Anchor: {},
          AnchorState: "anchored",
          Status: "open",
          CreatedBy: "user-1",
          CreatedAt: "",
          Replies: [{
            ID: "reply-1",
            AuthorID: "user-1",
            Body: `<script>alert(1)</script> ${"x".repeat(300)}`,
            CreatedAt: "",
          }],
        }],
        notice: null,
        members: [{ userId: "user-2", role: "reviewer" }],
        attachedSites: [{ siteId: "site-1", createdBy: "user-1" }],
        siteComments: [{
          source: "room",
          id: "c-1",
          createdAt: "2026-11-09T00:00:00Z",
          body: "<script>alert(1)</script>",
          page: "/index.html",
          anchorKind: "page",
          status: "open",
          versionId: "ver-1",
          contentHash: "hash-1",
        }, {
          source: "room",
          id: "c-2",
          createdAt: "2026-11-09T00:00:01Z",
          body: "",
          page: "/index.html",
          anchorKind: "page",
          status: "open",
          versionId: "ver-1",
          contentHash: "hash-1",
        }],
        commentCursor: null,
        threadCursor: "thread-cursor",
        candidateCursor: null,
        onCreateRoom: () => undefined,
        onSelectRoom: () => undefined,
        onSelectSite: () => undefined,
        onLoadMoreComments: () => undefined,
        onLoadMoreThreads: () => undefined,
        onLoadMoreCandidates: () => undefined,
        onCreateThread: () => undefined,
        onReply: () => undefined,
        onSignoff: () => undefined,
        onDecide: () => undefined,
        onSubmitCandidate: () => undefined,
        onAddMember: () => undefined,
        onAttachSite: () => undefined,
        onDetachSite: () => undefined,
        onResolve: () => undefined,
        onReanchor: () => undefined,
      }),
    );
    expect(room).toContain("Diff unavailable");
    expect(room).toContain("Expand reply");
    expect(room).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    expect(room).not.toContain("<script>alert");
    expect(room).toContain("Site comment page");
    expect(room).toContain("did not include a body");
    expect(room).toContain("Load older threads");
    expect(room).toContain("Resolve");
    expect(room).toContain("Add member");
    expect(room).toContain("Text highlight: not available");
    expect(room).toContain("Component id: not available");
    expect(room).toContain("Comment on this page");
    expect(room).not.toContain("Highlight text");
    expect(room).toContain("credential_store_unavailable");
    expect(room).not.toContain("type=\"password\"");
    expect(room).toContain("not an allowed https content origin");
    expect(room).toContain("Record version sign-off");
    expect(room).toContain("does not approve a change");
  });
});
