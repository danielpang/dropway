import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ apply: vi.fn(), decide: vi.fn(), member: vi.fn(), thread: vi.fn() }));
vi.mock("@/lib/api", () => ({
  ApiError: class extends Error {
    constructor(public status: number, message: string, public body: unknown) { super(message); }
  },
  api: { applyProjectRoomCandidate: mocks.apply, decideProjectRoomCandidate: mocks.decide, updateProjectRoomMember: mocks.member, getReviewThread: mocks.thread },
}));
import { ApiError } from "@/lib/api";
import { applyCandidateAction, decideAction, loadThreadRevisionsAction } from "@/app/(app)/rooms/actions";
import type { ReviewCandidate } from "@/lib/project-room/contract";

const candidate: ReviewCandidate = {
  ID: "candidate", RoomID: "room", SiteID: "site", BaseVersionID: "base",
  BaseContentHash: "base-hash", ContentHash: "candidate-hash", DiffHash: "diff-hash",
  Summary: "Change caption", State: "approved", CreatedBy: "human", CreatedAt: "",
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubEnv("NEXT_PUBLIC_COLLAB_REVIEW", "1");
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
    vi.stubEnv("NEXT_PUBLIC_COLLAB_REVIEW", "0");
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
