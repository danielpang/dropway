// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for removeMemberAction (members/actions.ts) — the M5 fix. The
// security property under test: the privileged session-kill
// (deleteUserSessions) + edge denylist write run ONLY for a user that Better
// Auth's authorization boundary (auth.api.removeMember) actually removed from
// the caller's org, never for a raw client-supplied id. The previous version
// killed sessions for any client-supplied userId with no authz check.
//
// The action is a "use server" module; under node that directive is an inert
// top-of-file string. We mock its four dependencies so the function logic runs
// in isolation.

import { afterEach, describe, expect, it, vi } from "vitest";

// vi.mock factories are hoisted above top-level consts, so the shared spies must
// live in a vi.hoisted block to be referenceable inside the factories below.
const { removeMember, deleteUserSessions, revokeAccess } = vi.hoisted(() => ({
  removeMember: vi.fn(),
  deleteUserSessions: vi.fn(),
  revokeAccess: vi.fn(),
}));

vi.mock("next/cache", () => ({ revalidatePath: vi.fn() }));
vi.mock("next/headers", () => ({ headers: async () => new Headers() }));
vi.mock("@/lib/auth", () => ({
  auth: {
    api: { removeMember },
    $context: Promise.resolve({ internalAdapter: { deleteUserSessions } }),
  },
}));
vi.mock("@/lib/api", () => ({
  api: { revokeAccess },
  // ApiError is referenced by revokeAccessAction's catch; a minimal class suffices.
  ApiError: class ApiError extends Error {
    status: number;
    body: unknown;
    constructor(status: number, message: string, body: unknown) {
      super(message);
      this.status = status;
      this.body = body;
    }
  },
}));

import { removeMemberAction } from "@/app/(app)/members/actions";
// The mocked @/lib/api above exports this minimal ApiError; the action's catch
// branches on `instanceof ApiError` + `.status`, so the test uses the same class.
import { ApiError } from "@/lib/api";

afterEach(() => {
  vi.clearAllMocks();
});

describe("removeMemberAction", () => {
  it("revokes ONLY the user Better Auth confirms it removed", async () => {
    removeMember.mockResolvedValueOnce({ member: { userId: "user-removed" } });
    revokeAccess.mockResolvedValueOnce({ min_iat: 123 });

    const res = await removeMemberAction({ memberId: "member-row-1" });

    // The member was removed and the follow-up revocation ran.
    expect(res).toEqual({ removed: true, revoke: { ok: true, minIat: 123 } });
    // The session-kill + denylist target the REMOVED user's id, not the input.
    expect(deleteUserSessions).toHaveBeenCalledWith("user-removed");
    expect(revokeAccess).toHaveBeenCalledWith({ kind: "user", id: "user-removed" });
    // The action let Better Auth bind the org (no client-supplied org id).
    expect(removeMember).toHaveBeenCalledWith({
      body: { memberIdOrEmail: "member-row-1" },
      headers: expect.any(Headers),
    });
  });

  it("does NOT kill any sessions when the removal is refused (unauthorized)", async () => {
    // Better Auth rejects an unauthorized caller / cross-org target.
    removeMember.mockRejectedValueOnce({
      body: { message: "You are not allowed to delete this member" },
    });

    const res = await removeMemberAction({ memberId: "victim-in-other-org" });

    expect(res).toEqual({
      removed: false,
      message: "You are not allowed to delete this member",
    });
    // The critical assertion: the privileged session-kill never ran.
    expect(deleteUserSessions).not.toHaveBeenCalled();
    expect(revokeAccess).not.toHaveBeenCalled();
  });

  it("rejects an empty member id without touching any privileged call", async () => {
    const res = await removeMemberAction({ memberId: "" });
    expect(res).toEqual({ removed: false, message: "Missing member." });
    expect(removeMember).not.toHaveBeenCalled();
    expect(deleteUserSessions).not.toHaveBeenCalled();
  });

  it("reports a removal that returns no member id as not-removed", async () => {
    removeMember.mockResolvedValueOnce({ member: {} });
    const res = await removeMemberAction({ memberId: "member-row-2" });
    expect(res).toEqual({
      removed: false,
      message: "Could not remove the member. Try again.",
    });
    expect(deleteUserSessions).not.toHaveBeenCalled();
  });

  // OSS build: the member is removed but the Go revoke endpoint is absent (404).
  // revokeAccessAction maps that to { unavailable: true } (no `message`), so the
  // member list shows NO error and still refreshes. Proves removed:true is
  // returned with the unavailable shape.
  it("returns removed:true with the unavailable shape when the revoke endpoint is absent", async () => {
    removeMember.mockResolvedValueOnce({ member: { userId: "user-removed" } });
    revokeAccess.mockRejectedValueOnce(new ApiError(404, "not found", null));

    const res = await removeMemberAction({ memberId: "member-row-3" });

    expect(res).toEqual({ removed: true, revoke: { ok: false, unavailable: true } });
    // The session kill is best-effort and still runs for the removed user.
    expect(deleteUserSessions).toHaveBeenCalledWith("user-removed");
  });

  // The member row is gone but the denylist write fails with a message: the
  // contract is removed:true with the failure surfaced, so the UI can warn
  // "removed, but revoking access failed" rather than claim full success.
  it("returns removed:true and surfaces a revoke failure message", async () => {
    removeMember.mockResolvedValueOnce({ member: { userId: "user-removed" } });
    revokeAccess.mockRejectedValueOnce(
      new ApiError(500, "boom", { message: "denylist write failed" }),
    );

    const res = await removeMemberAction({ memberId: "member-row-4" });

    expect(res).toEqual({
      removed: true,
      revoke: { ok: false, message: "denylist write failed" },
    });
    expect(deleteUserSessions).toHaveBeenCalledWith("user-removed");
  });

  // A non-APIError throw (e.g. a network failure with only `.message`) still
  // yields a clean not-removed result via the `.message` fallback, and never
  // kills sessions.
  it("falls back to .message when removeMember throws a plain Error", async () => {
    removeMember.mockRejectedValueOnce(new Error("network down"));

    const res = await removeMemberAction({ memberId: "member-row-5" });

    expect(res).toEqual({ removed: false, message: "network down" });
    expect(deleteUserSessions).not.toHaveBeenCalled();
    expect(revokeAccess).not.toHaveBeenCalled();
  });
});
