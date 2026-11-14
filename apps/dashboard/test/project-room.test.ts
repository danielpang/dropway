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
  appendThreadRevisions,
  parseThreadDetail,
  canComment,
  canEditRoomSite,
  parseRoomActivity,
  canDecideChange,
  canSignOffVersion,
  compareLive,
  creatableAnchorKinds,
  imageCommentAnchor,
  pageCommentAnchor,
  pageQuery,
  parseCandidate,
  parseCandidateDiff,
  parseSiteCommentPage,
  shippedPreciseHtmlSelection,
  terminalCopy,
  threadPageUrl,
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
    const diff = parseCandidateDiff({
      candidate_id: "cand-1",
      site_id: "site-1",
      base_version_id: "ver-1",
      base_content_hash: "hash-base",
      content_hash: "hash-next",
      verified: true,
      truncated: true,
      omitted_files: 3,
      comment_content: "untrusted_data",
      files: [{
        path: "index.html",
        change: "modified",
        binary: false,
        before: "old",
        after: "<script>alert(1)</script>",
        before_truncated: false,
        after_truncated: true,
        untrusted: true,
      }],
    });
    expect(diff && "files" in diff ? diff.files[0]?.after : "").toBe("<script>alert(1)</script>");
    expect(parseCandidateDiff({ verified: false, files: [{ before: "secret" }] })).toEqual({ verified: false });
    expect(parseCandidateDiff({
      verified: true,
      comment_content: "untrusted_data",
      candidate_id: "cand-1",
      base_version_id: "ver-1",
      base_content_hash: "hash-base",
      content_hash: "hash-next",
      files: [{ path: "index.html", change: "modified", binary: false, before: "x", after: "y", untrusted: false }],
    })).toEqual({ verified: false });
  });
});

function buttonDisabled(html: string, label: string): boolean {
  const match = html.match(new RegExp(`<button([^>]*)>${label}</button>`));
  return /(?:^|\s)disabled(?:=""|=|\s|$)/.test(match?.[1] ?? "disabled");
}

describe("project room markup", () => {
  it("keeps Approve and Reject visible and does not offer precise HTML selection or a secret field", () => {
    const verified = {
      candidateId: "cand-1",
      siteId: "site-1",
      baseVersionId: "ver-1",
      baseContentHash: "hash-base",
      contentHash: "hash-next",
      verified: true as const,
      truncated: true,
      omittedFiles: 2,
      files: [
        { path: "index.html", change: "modified" as const, binary: false, beforeSHA256: "a", afterSHA256: "b", beforeSize: 3, afterSize: 3, before: "old", after: "<script>alert(1)</script>", beforeTruncated: false, afterTruncated: true },
        { path: "new.css", change: "added" as const, binary: false, beforeSHA256: "", afterSHA256: "c", beforeSize: null, afterSize: 4, before: "", after: "body{}", beforeTruncated: false, afterTruncated: false },
        { path: "old.js", change: "removed" as const, binary: false, beforeSHA256: "d", afterSHA256: "", beforeSize: 2, afterSize: null, before: "go", after: "", beforeTruncated: false, afterTruncated: false },
        { path: "keep.txt", change: "unchanged" as const, binary: false, beforeSHA256: "e", afterSHA256: "e", beforeSize: 1, afterSize: 1, before: "", after: "", beforeTruncated: false, afterTruncated: false },
        { path: "hero.png", change: "modified" as const, binary: true, beforeSHA256: "f", afterSHA256: "g", beforeSize: 90000, afterSize: 90000, before: "<script>hidden</script>", after: "<script>hidden</script>", beforeTruncated: false, afterTruncated: false },
      ],
    };
    const html = renderToStaticMarkup(
      createElement(CandidateReview, {
        candidate,
        live: { versionId: "ver-1", contentHash: "hash-base" },
        diff: { status: "verified", diff: verified },
        canDecide: true,
        onDecide: () => undefined,
      }),
    );
    expect(html).toContain("Accept change");
    expect(html).toContain("Reject change");
    expect(html).toContain("Apply");
    expect(html).toContain("Apply publishes this exact approved change");
    expect(buttonDisabled(html, "Apply")).toBe(true);
    expect(buttonDisabled(html, "Accept change")).toBe(false);
    expect(buttonDisabled(html, "Reject change")).toBe(false);
    expect(html).toContain("modified");
    expect(html).toContain("added");
    expect(html).toContain("removed");
    expect(html).toContain("unchanged");
    expect(html).toContain("2 files omitted");
    expect(html).toContain("Text truncated");
    expect(html).toContain("Binary or oversized file. Metadata only.");
    expect(html).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    expect(html).not.toContain("<script>alert");
    expect(html).not.toContain("<script>hidden");
    expect(html).not.toContain("type=\"password\"");
    expect(html).toContain("Replace the hero caption");

    const stale = renderToStaticMarkup(
      createElement(CandidateReview, {
        candidate,
        live: { versionId: "ver-1", contentHash: "other" },
        diff: { status: "verified", diff: verified },
        canDecide: true,
        onDecide: () => undefined,
      }),
    );
    expect(stale).toContain("needs a new candidate");
    expect(buttonDisabled(stale, "Accept change")).toBe(true);
    expect(buttonDisabled(stale, "Reject change")).toBe(false);

    const mismatch = renderToStaticMarkup(
      createElement(CandidateReview, {
        candidate,
        live: { versionId: "ver-1", contentHash: "hash-base" },
        diff: { status: "unverified" },
        canDecide: true,
        onDecide: () => undefined,
      }),
    );
    expect(mismatch).toContain("Cannot verify this change.");
    expect(buttonDisabled(mismatch, "Accept change")).toBe(true);
    expect(buttonDisabled(mismatch, "Reject change")).toBe(false);

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
        sites: [{ id: "site-1", slug: "report", liveUrl: "https://docs.example.com", currentVersionId: "ver-1", currentContentHash: "hash-base" }],
        candidates: [candidate],
        diffs: {
          "cand-1": {
            status: "verified",
            diff: {
              candidateId: "cand-1",
              siteId: "site-1",
              baseVersionId: "ver-1",
              baseContentHash: "hash-base",
              contentHash: "hash-next",
              verified: true,
              truncated: false,
              omittedFiles: 0,
              files: [{
                path: "index.html",
                change: "modified",
                binary: false,
                beforeSHA256: "a",
                afterSHA256: "b",
                beforeSize: 3,
                afterSize: 28,
                before: "old",
                after: "<script>alert(1)</script>",
                beforeTruncated: false,
                afterTruncated: false,
              }],
            },
          },
        },
        parentOrigin: "https://app.dropway.dev",
        orgEnabled: true,
        onEnableRooms: () => undefined,
        threads: [{
          ID: "thread-1",
          Revisions: [{ id: "rev-1", anchorKind: "page", page: "/historical.html", anchorState: "orphaned", reason: "<script>history</script>", createdAt: "2026-11-10T00:00:00Z" }],
          NextRevisionCursor: "revision-cursor",
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
    expect(room).toContain("Changes (1)");
    expect(room).not.toContain("Candidate content hash");
    expect(room).not.toContain("Diff unavailable");
    expect(room).toContain("Expand reply");
    expect(room).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    expect(room).not.toContain("<script>alert");
    expect(room).toContain("Site comment page");
    expect(room).toContain("did not include a body");
    expect(room).toContain("Load older threads");
    expect(room).toContain("Resolve");
    expect(room).toContain("Anchor history");
    expect(room).toContain("Load older anchor revisions");
    expect(room).toContain("/historical.html");
    expect(room).toContain("orphaned");
    expect(room).toContain("&lt;script&gt;history&lt;/script&gt;");
    expect(room).not.toContain("<script>history</script>");
    expect(room).toContain("Settings &amp; sharing");
    expect(room).toContain("All versions");
    expect(room).toContain("View or review");
    expect(room).toContain("Comment on this page");
    expect(room).not.toContain("Highlight text");
    expect(room).not.toContain("credential_store_unavailable");
    expect(room).not.toContain("type=\"password\"");
    expect(room).toContain("Safe preview of this version");
    expect(room).toContain("Record version sign-off");
    expect(room).toContain("does not approve a change");
  });
});


describe("application review controls", () => {
  const diff = { candidateId: candidate.ID, siteId: candidate.SiteID, baseVersionId: candidate.BaseVersionID,
    baseContentHash: candidate.BaseContentHash, contentHash: candidate.ContentHash, verified: true as const,
    truncated: false, omittedFiles: 0, files: [] };
  function markup(overrides: Partial<Parameters<typeof CandidateReview>[0]> = {}) {
    return renderToStaticMarkup(createElement(CandidateReview, {
      candidate: { ...candidate, State: "approved" }, live: { versionId: "ver-1", contentHash: "hash-base" },
      diff: { status: "verified", diff }, canDecide: true, onDecide: () => undefined, onApply: () => undefined,
      ...overrides,
    }));
  }
  it("requires approval, a verified exact diff, matching baseline and content rights before applying", () => {
    expect(buttonDisabled(markup(), "Apply")).toBe(false);
    expect(buttonDisabled(markup(), "Reject change")).toBe(true);
    expect(buttonDisabled(markup(), "Accept change")).toBe(true);
    for (const change of [
      { candidate }, { candidate: { ...candidate, State: "rejected" } },
      { candidate: { ...candidate, State: "approved", AppliedVersionID: "next" } },
      { live: null }, { live: { versionId: "ver-1", contentHash: null } },
      { live: { versionId: "newer", contentHash: "other" } }, { canDecide: false }, { busy: true },
      { diff: { status: "unverified" as const } },
      { diff: { status: "verified" as const, diff: { ...diff, siteId: "other-site" } } },
    ]) expect(buttonDisabled(markup(change), "Apply")).toBe(true);
    expect(buttonDisabled(markup({ busy: true }), "Reject change")).toBe(true);
    const applied = markup({ candidate: { ...candidate, State: "approved", AppliedVersionID: "next" }, live: { versionId: "next", contentHash: "hash-next" } });
    expect(applied).toContain("Applied as version next");
    expect(applied).not.toContain("needs a new candidate");
  });

  it("keeps pinned page navigation on the approved content origin", () => {
    expect(threadPageUrl("https://report.dropwaycontent.com/", "/docs/page.html")).toBe("https://report.dropwaycontent.com/docs/page.html");
    for (const page of ["//evil.example/path", "https://evil.example/", "/\\evil.example/path"]) expect(threadPageUrl("https://report.dropwaycontent.com/", page)).toBeNull();
  });

  it("keeps independent site permissions and parses activity as text", () => {
    expect(canEditRoomSite({ owner_id: "owner", allow_member_edits: false }, { user_id: "reviewer", role: "member" })).toBe(false);
    expect(canEditRoomSite({ owner_id: "owner" }, { user_id: "owner", role: "member" })).toBe(true);
    expect(canEditRoomSite({ allow_member_edits: true }, {})).toBe(false);
    expect(parseRoomActivity({ items: [{ id: "activity", kind: "<script>bad</script>", actor_user_id: "user", created_at: "now", thread_id: "thread" }], unread: 3, next_cursor: "next" })).toMatchObject({ unread: 3, nextCursor: "next", items: [{ threadId: "thread", kind: "<script>bad</script>" }] });
    expect(parseRoomActivity({ items: [{ id: "missing-kind" }] })).toBeNull();
  });
});


describe("independent anchor history pagination", () => {
  const body = {
    id: "thread", room_id: "room", site_id: "site", version_id: "original-version", content_hash: "original-hash",
    page: "/original.html", anchor_kind: "page", anchor_state: "anchored", status: "open",
    replies: [{ id: "reply", author_id: "user", body: "Keep this reply", created_at: "now" }], next_cursor: "reply-cursor",
    revisions: [{ id: "revision-1", anchor_kind: "page", page: "/revision.html", anchor_state: "orphaned", reason: "<b>Lost target</b>", created_at: "now" }],
    next_revision_cursor: "history-cursor",
  };
  it("parses history without substituting revision coordinates for the original pin", () => {
    const thread = parseThreadDetail(body)!;
    expect(thread.Page).toBe("/original.html");
    expect(thread.VersionID).toBe("original-version");
    expect(thread.NextReplyCursor).toBe("reply-cursor");
    expect(thread.NextRevisionCursor).toBe("history-cursor");
    expect(thread.Revisions?.[0]).toMatchObject({ page: "/revision.html", anchorState: "orphaned", reason: "<b>Lost target</b>" });
  });
  it("merges history pages independently and refuses a changed pin", () => {
    const thread = parseThreadDetail(body)!;
    const page = parseThreadDetail({ ...body, replies: [], next_cursor: "different-reply-cursor", next_revision_cursor: "next-history",
      revisions: [...body.revisions, { ...body.revisions[0], id: "revision-2" }] })!;
    const merged = appendThreadRevisions(thread, page);
    expect(merged.Revisions?.map((item) => item.id)).toEqual(["revision-1", "revision-2"]);
    expect(merged.Replies).toEqual(thread.Replies);
    expect(merged.NextReplyCursor).toBe("reply-cursor");
    expect(merged.NextRevisionCursor).toBe("next-history");
    expect(merged.Page).toBe("/original.html");
    expect(appendThreadRevisions(thread, { ...page, VersionID: "different-version" })).toBe(thread);
    expect(appendThreadRevisions(thread, { ...page, RoomID: "other-room" })).toBe(thread);
  });
});
