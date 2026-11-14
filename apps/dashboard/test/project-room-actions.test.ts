import { createHash } from "node:crypto";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ capability: vi.fn(), apply: vi.fn(), decide: vi.fn(), member: vi.fn(), thread: vi.fn(), file: vi.fn() }));
vi.mock("@/lib/api", () => ({
  ApiError: class extends Error {
    constructor(public status: number, message: string, public body: unknown) { super(message); }
  },
  api: { projectCollaborationCapabilities: mocks.capability, applyProjectRoomCandidate: mocks.apply, decideProjectRoomCandidate: mocks.decide, updateProjectRoomMember: mocks.member, getReviewThread: mocks.thread, getRoomVersionFile: mocks.file },
}));
import { ApiError } from "@/lib/api";
import { applyCandidateAction, decideAction, loadThreadRevisionsAction, loadStaticSnapshotAction } from "@/app/(app)/rooms/actions";
import type { ReviewCandidate } from "@/lib/project-room/contract";

const candidate: ReviewCandidate = {
  ID: "candidate", RoomID: "room", SiteID: "site", BaseVersionID: "base",
  BaseContentHash: "base-hash", ContentHash: "candidate-hash", DiffHash: "diff-hash",
  Summary: "Change caption", State: "approved", CreatedBy: "human", CreatedAt: "",
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.capability.mockResolvedValue({ enabled: true, execution_configured: true });
});

describe("human candidate application", () => {
  it("sends only the exact candidate identity and handles idempotent receipts", async () => {
    mocks.apply.mockResolvedValue({ candidate: { ...candidate, AppliedVersionID: "version-next" }, applied: true, changed: false, version_id: "version-next" });
    const result = await applyCandidateAction("room", candidate);
    expect(mocks.apply).toHaveBeenCalledWith("room", "candidate", {
      content_hash: "candidate-hash", base_version_id: "base", base_content_hash: "base-hash",
    });
    expect(result).toMatchObject({ ok: true, changed: false, versionId: "version-next", candidate: { AppliedVersionID: "version-next" } });
  });

  it("does not send unapproved or cross-room candidates, or any request while disabled", async () => {
    for (const State of ["pending", "rejected", "conflict", "unknown"]) {
      expect(await applyCandidateAction("room", { ...candidate, State })).toMatchObject({ ok: false });
    }
    expect(await applyCandidateAction("other-room", candidate)).toMatchObject({ ok: false });
    expect(await decideAction("other-room", candidate, "approve")).toMatchObject({ ok: false });
    mocks.capability.mockResolvedValue({ enabled: false, execution_configured: false });
    expect(await applyCandidateAction("room", candidate)).toMatchObject({ ok: false });
    expect(mocks.apply).not.toHaveBeenCalled();
    expect(mocks.decide).not.toHaveBeenCalled();
  });

  it("does not mistake a conflict or another candidate's receipt for success", async () => {
    mocks.apply.mockRejectedValue(new ApiError(409, "conflict", {}));
    expect(await applyCandidateAction("room", candidate)).toMatchObject({ ok: false, message: expect.stringContaining("Cannot apply") });
    mocks.apply.mockResolvedValue({ candidate: { ...candidate, ID: "different" }, applied: true, changed: true, version_id: "next" });
    expect(await applyCandidateAction("room", candidate)).toMatchObject({ ok: false, message: expect.stringContaining("Refresh") });
  });
});


describe("thread revision reads", () => {
  it("sends only the revision cursor and checks the requested thread and room", async () => {
    const thread = { id: "thread", room_id: "room", site_id: "site", version_id: "version", content_hash: "hash", page: "/", anchor_kind: "page", revisions: [], next_revision_cursor: "next" };
    mocks.thread.mockResolvedValue(thread);
    expect(await loadThreadRevisionsAction("room", "thread", "history-only")).toMatchObject({ ok: true, thread: { NextRevisionCursor: "next" } });
    expect(mocks.thread).toHaveBeenCalledWith("thread", undefined, "history-only");
    mocks.thread.mockResolvedValue({ ...thread, room_id: "other-room" });
    expect(await loadThreadRevisionsAction("room", "thread", "history-only")).toMatchObject({ ok: false });
    mocks.thread.mockResolvedValue({ ...thread, id: "other-thread" });
    expect(await loadThreadRevisionsAction("room", "thread", "history-only")).toMatchObject({ ok: false });
  });
});


describe("immutable static review reads", () => {
  it("checks identity and content digests before reconstructing HTML", async () => {
    const content = Buffer.from('<p onclick="alert(1)">Immutable caption</p>');
    const file = { site_id: "site", version_id: "version", path: "index.html", content_hash: "a".repeat(64), content_base64: content.toString("base64"), content_type: "text/html", sha256: createHash("sha256").update(content).digest("hex") };
    mocks.file.mockResolvedValue(file);
    const result = await loadStaticSnapshotAction("room", "site", "version", "/");
    expect(result).toMatchObject({ ok: true, snapshot: { siteId: "site", versionId: "version", contentHash: "a".repeat(64) } });
    if (result.ok) { expect(result.snapshot.html).toContain("Immutable caption"); expect(result.snapshot.html).not.toContain("onclick"); }
    mocks.file.mockResolvedValue({ ...file, sha256: "b".repeat(64) });
    expect(await loadStaticSnapshotAction("room", "site", "version", "/")).toMatchObject({ ok: false });
    mocks.file.mockResolvedValue({ ...file, version_id: "newer-version" });
    expect(await loadStaticSnapshotAction("room", "site", "version", "/")).toMatchObject({ ok: false });
  });
});
