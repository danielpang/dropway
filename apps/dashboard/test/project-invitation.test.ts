import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ inspect: vi.fn(), request: vi.fn(), get: vi.fn(), set: vi.fn(), remove: vi.fn() }));
vi.mock("next/headers", () => ({ cookies: async () => ({ get: mocks.get, set: mocks.set, delete: mocks.remove }) }));
vi.mock("@/lib/session", () => ({ getCurrentSession: async () => null }));
vi.mock("@/lib/env", () => ({ betterAuthUrl: () => "https://app.example.test" }));
vi.mock("@/lib/email", () => ({ sendEmailStrict: vi.fn() }));
vi.mock("@/lib/api", () => ({
  ApiError: class extends Error { constructor(public status: number, message: string, public body?: unknown) { super(message); } },
  api: { inspectProjectInvitation: mocks.inspect, sharedProjectRequest: mocks.request },
}));
import { ApiError } from "@/lib/api";
import { loadProjectInvitation } from "@/lib/project-room/invitation";
import { acceptSharedProjectAction, rememberProjectInvitationAction } from "@/app/shared-projects/actions";

import AcceptInvitationPage from "@/app/shared-projects/accept/page";

const token = "A".repeat(43);
beforeEach(() => {
  vi.resetAllMocks();
  mocks.get.mockReturnValue({ value: token });
  mocks.inspect.mockResolvedValue({ email: "invited@example.test", project_name: "Studio launch", has_account: false });
});

describe("project invitation authentication context", () => {
  it("uses a bearer invitation to identify new and existing accounts without accepting it", async () => {
    expect(await loadProjectInvitation(token)).toEqual({ status: "available", invitation: { email: "invited@example.test", projectName: "Studio launch", hasAccount: false } });
    mocks.inspect.mockResolvedValue({ email: "invited@example.test", project_name: "Studio launch", has_account: true });
    expect(await loadProjectInvitation(token)).toMatchObject({ status: "available", invitation: { hasAccount: true } });
    expect(mocks.request).not.toHaveBeenCalled();
  });
  it("does not disclose context for malformed, expired or rejected invitations", async () => {
    expect(await loadProjectInvitation("bad")).toEqual({ status: "unavailable" });
    expect(mocks.inspect).not.toHaveBeenCalled();
    mocks.inspect.mockRejectedValue(new ApiError(403, "Forbidden", { error: "forbidden" }));
    expect(await loadProjectInvitation(token)).toEqual({ status: "unavailable" });
  });
  it("retains the token in a scoped HTTP-only cookie while returning only sign-in details", async () => {
    const result = await rememberProjectInvitationAction(token);
    expect(result).toMatchObject({ ok: true, lookup: { status: "available", invitation: { email: "invited@example.test" } } });
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
    mocks.request.mockRejectedValue(new ApiError(403, "Forbidden", { error: "forbidden" }));
    expect(await acceptSharedProjectAction()).toMatchObject({ ok: false });
    expect(mocks.remove).not.toHaveBeenCalled();
  });
});

describe("invitation lookup recovery", () => {
  it.each([
    new Error("network unavailable"),
    new ApiError(404, "endpoint not deployed", null),
    new ApiError(429, "rate limited", { error: "rate_limited" }),
    new ApiError(500, "database unavailable", { error: "internal_error" }),
    new ApiError(503, "service unavailable", {}),
    new ApiError(403, "proxy rejection", null),
  ])("retries a temporary failure from the retained cookie on page reload: %s", async (error) => {
    let retained: string | undefined;
    mocks.get.mockImplementation(() => retained ? { value: retained } : undefined);
    mocks.set.mockImplementation((_name, value) => { retained = value; });
    mocks.inspect.mockRejectedValue(error);
    expect(await rememberProjectInvitationAction(token)).toEqual({ ok: true, lookup: { status: "temporary-error" } });
    const failed = renderToStaticMarkup(await AcceptInvitationPage());
    expect(failed).toContain("Retry</button>");
    expect(failed).toContain('action="/shared-projects/accept" method="get"');
    expect(failed).not.toContain("fresh invitation");
    expect(failed).not.toContain(token);
    mocks.inspect.mockResolvedValue({ email: "invited@example.test", project_name: "Studio launch", has_account: false });
    // Retry reloads this dynamic page without a fragment or token argument.
    const recovered = renderToStaticMarkup(await AcceptInvitationPage());
    expect(recovered).toContain("Studio launch");
    expect(recovered).not.toContain("Retry</button>");
    expect(recovered).not.toContain(token);
    expect(mocks.inspect).toHaveBeenLastCalledWith(token);
    expect(mocks.set).toHaveBeenCalledTimes(1);
    expect(mocks.remove).not.toHaveBeenCalled();
    expect(mocks.request).not.toHaveBeenCalled();
  });
  it("treats malformed success responses as retryable", async () => {
    mocks.inspect.mockResolvedValue({});
    expect(await loadProjectInvitation(token)).toEqual({ status: "temporary-error" });
  });
  it("keeps the definitive rejection UI for invalid, revoked and expired invitations", async () => {
    // The lookup contract deliberately uses the same response for all three.
    mocks.inspect.mockRejectedValue(new ApiError(403, "Forbidden", { error: "forbidden" }));
    const html = renderToStaticMarkup(await AcceptInvitationPage());
    expect(html).toContain("unavailable or has expired");
    expect(html).toContain("fresh invitation");
    expect(html).not.toContain("Retry</button>");
    expect(html).not.toContain("invited@example.test");
  });
});
