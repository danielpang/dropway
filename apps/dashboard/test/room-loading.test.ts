import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ enabled: vi.fn(), detail: vi.fn(), threads: vi.fn(), candidates: vi.fn(), org: vi.fn() }));
vi.mock("@/lib/project-room/capabilities", () => ({ collaborationEnabled: mocks.enabled }));
vi.mock("@/lib/org", () => ({ loadActiveOrg: mocks.org }));
vi.mock("@/lib/api", () => ({ ApiError: class extends Error {}, api: { getProjectRoom: mocks.detail, listRoomThreads: mocks.threads, listRoomCandidates: mocks.candidates } }));
import { loadRoomAction, loadRoomDiscussionAction } from "@/app/(app)/rooms/actions";

const room = { ID: "room", OrgID: "org", Name: "Launch", OwnerUserID: "owner", Role: "owner" };
beforeEach(() => {
  vi.clearAllMocks();
  mocks.enabled.mockResolvedValue(true);
  mocks.detail.mockResolvedValue({ room, sites: [{ site_id: "site", created_by: "owner" }], members: [{ user_id: "owner", role: "owner" }] });
  mocks.org.mockResolvedValue({ organizationId: "org", members: [{ userId: "owner", name: "Mina Rao" }, { userId: "unrelated", name: "Other member" }] });
  mocks.threads.mockResolvedValue({ threads: [] });
  mocks.candidates.mockResolvedValue({ candidates: [] });
});

describe("progressive room reads", () => {
  it("returns authorized resources without waiting for discussion or identity hydration", async () => {
    expect(await loadRoomAction("room")).toMatchObject({ ok: true, detail: { room: { ID: "room" }, sites: [{ siteId: "site" }] } });
    expect(mocks.threads).not.toHaveBeenCalled();
    expect(mocks.candidates).not.toHaveBeenCalled();
    expect(mocks.org).not.toHaveBeenCalled();
  });

  it("still checks rollout and rejects a mismatched room", async () => {
    mocks.enabled.mockResolvedValueOnce(false);
    expect(await loadRoomAction("room")).toMatchObject({ ok: false });
    expect(mocks.detail).not.toHaveBeenCalled();
    expect(await loadRoomAction("another-room")).toMatchObject({ ok: false });
  });

  it("loads discussions separately and resolves names only for this room's members", async () => {
    expect(await loadRoomDiscussionAction("room")).toMatchObject({ ok: true, members: [{ userId: "owner", name: "Mina Rao" }], threads: [], candidates: [] });
    expect(mocks.threads).toHaveBeenCalledWith("room");
    expect(mocks.candidates).toHaveBeenCalledWith("room");
  });
});
