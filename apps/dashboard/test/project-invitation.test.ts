import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ inspect: vi.fn(), request: vi.fn(), get: vi.fn(), set: vi.fn(), remove: vi.fn() }));
vi.mock("next/headers", () => ({ cookies: async () => ({ get: mocks.get, set: mocks.set, delete: mocks.remove }) }));
vi.mock("@/lib/env", () => ({ betterAuthUrl: () => "https://app.example.test" }));
vi.mock("@/lib/email", () => ({ sendEmailStrict: vi.fn() }));
vi.mock("@/lib/api", () => ({
  ApiError: class extends Error { constructor(public status: number, message: string, public body?: unknown) { super(message); } },
  api: { inspectProjectInvitation: mocks.inspect, sharedProjectRequest: mocks.request },
}));
import { ApiError } from "@/lib/api";
import { loadProjectInvitation } from "@/lib/project-room/invitation";
import { acceptSharedProjectAction, rememberProjectInvitationAction } from "@/app/shared-projects/actions";

const token = "A".repeat(43);
beforeEach(() => {
  vi.resetAllMocks();
  mocks.get.mockReturnValue({ value: token });
  mocks.inspect.mockResolvedValue({ email: "invited@example.test", project_name: "Studio launch", has_account: false });
});

describe("project invitation authentication context", () => {
  it("uses a bearer invitation to identify new and existing accounts without accepting it", async () => {
    expect(await loadProjectInvitation(token)).toEqual({ email: "invited@example.test", projectName: "Studio launch", hasAccount: false });
    mocks.inspect.mockResolvedValue({ email: "invited@example.test", project_name: "Studio launch", has_account: true });
    expect(await loadProjectInvitation(token)).toMatchObject({ hasAccount: true });
    expect(mocks.request).not.toHaveBeenCalled();
  });
  it("does not disclose context for malformed, expired or rejected invitations", async () => {
    expect(await loadProjectInvitation("bad")).toBeNull();
    expect(mocks.inspect).not.toHaveBeenCalled();
    mocks.inspect.mockRejectedValue(new ApiError(403, "Forbidden", {}));
    expect(await loadProjectInvitation(token)).toBeNull();
  });
  it("retains the token in a scoped HTTP-only cookie while returning only sign-in details", async () => {
    const result = await rememberProjectInvitationAction(token);
    expect(result).toMatchObject({ ok: true, invitation: { email: "invited@example.test" } });
    expect(JSON.stringify(result)).not.toContain(token);
    expect(mocks.set).toHaveBeenCalledWith("dropway-project-invitation", token, expect.objectContaining({ httpOnly: true, secure: true, sameSite: "lax", path: "/shared-projects", maxAge: 1800 }));
  });
  it("accepts only via the authenticated backend and clears the pending invitation after success", async () => {
    mocks.request.mockResolvedValue({ project: { id: "room", name: "Studio launch", role: "reviewer", sites: [] } });
    expect(await acceptSharedProjectAction()).toMatchObject({ ok: true, project: { id: "room" } });
    expect(mocks.request).toHaveBeenCalledWith("/accept", "POST", { token });
    expect(mocks.remove).toHaveBeenCalledWith({ name: "dropway-project-invitation", path: "/shared-projects" });
  });
  it("keeps the pending invitation when the account must be switched or verified", async () => {
    mocks.request.mockRejectedValue(new ApiError(403, "Forbidden", {}));
    expect(await acceptSharedProjectAction()).toMatchObject({ ok: false });
    expect(mocks.remove).not.toHaveBeenCalled();
  });
});
