import { beforeEach, describe, expect, it, vi } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { invitationToken, parseGuestProject } from "@/lib/project-room/sharing";
const mocks = vi.hoisted(() => ({
  request: vi.fn(),
  invite: vi.fn(),
  room: vi.fn(),
  send: vi.fn(),
  set: vi.fn(),
  get: vi.fn(),
  remove: vi.fn(),
}));
vi.mock("@/lib/api", () => ({
  ApiError: class extends Error {
    constructor(public status: number) {
      super("upstream");
    }
  },
  api: {
    sharedProjectRequest: mocks.request,
    inviteProjectGuest: mocks.invite,
    getProjectRoom: mocks.room,
  },
}));
vi.mock("@/lib/email", () => ({ sendEmailStrict: mocks.send }));
vi.mock("@/lib/env", () => ({
  betterAuthUrl: () => "https://app.example.com",
}));
vi.mock("next/headers", () => ({
  cookies: async () => ({
    set: mocks.set,
    get: mocks.get,
    delete: mocks.remove,
  }),
}));
import {
  acceptSharedProjectAction,
  rememberProjectInvitationAction,
  inviteProjectGuestAction,
  sharedCommentAction,
} from "@/app/shared-projects/actions";
import { visibleNavLinks } from "@/components/main-nav";
import { GuestProjectView } from "@/components/shared-projects/project-view";
const project = {
  id: "project",
  name: "<script>hostile</script>",
  role: "viewer" as const,
  sites: [
    {
      id: "site",
      slug: "site",
      title: "Site",
      current_version_id: "version",
      current_content_hash: "hash",
    },
  ],
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.room.mockResolvedValue({
    room: {
      id: "project",
      org_id: "org",
      name: "<script>Project</script>",
      owner_user_id: "owner",
      role: "owner",
    },
  });
});
describe("project-only sharing", () => {
  it("recognizes bounded opaque invitation fragments only", () => {
    expect(invitationToken(`#invite=${"a".repeat(43)}`)).toBe("a".repeat(43));
    for (const hash of [
      "#invite=short",
      "#invite=https://evil.test",
      "#other=token",
    ])
      expect(invitationToken(hash)).toBeNull();
  });
  it("stores a short-lived secure HttpOnly cookie and accepts only via its POST action", async () => {
    const token = "a".repeat(43);
    expect(await rememberProjectInvitationAction(token)).toEqual({ ok: true });
    expect(mocks.set).toHaveBeenCalledWith(
      "dropway-project-invitation",
      token,
      {
        httpOnly: true,
        secure: true,
        sameSite: "lax",
        path: "/shared-projects",
        maxAge: 1800,
      },
    );
    expect(mocks.request).not.toHaveBeenCalled();
    mocks.get.mockReturnValue({ value: token });
    mocks.request.mockResolvedValue({ project });
    expect(await acceptSharedProjectAction()).toMatchObject({
      ok: true,
      project: { id: "project" },
    });
    expect(mocks.request).toHaveBeenCalledWith("/accept", "POST", { token });
    expect(mocks.remove).toHaveBeenCalledWith({
      name: "dropway-project-invitation",
      path: "/shared-projects",
    });
  });
  it("does not report failed mail as delivered or return bearer material", async () => {
    const token = "b".repeat(43);
    mocks.invite.mockResolvedValue({
      token,
      share: {
        id: "share",
        email: "invited@example.com",
        role: "reviewer",
        expires_at: "2030-01-01",
      },
    });
    mocks.send.mockRejectedValueOnce(new Error("SMTP credentials secret"));
    const failed = await inviteProjectGuestAction(
      "project",
      "invited@example.com",
      "reviewer",
    );
    expect(failed).toMatchObject({ ok: true, delivered: false });
    expect(JSON.stringify(failed)).not.toContain(token);
    expect(JSON.stringify(failed)).not.toContain("SMTP credentials");
    mocks.send.mockResolvedValue(undefined);
    const delivered = await inviteProjectGuestAction(
      "project",
      "invited@example.com",
      "reviewer",
    );
    expect(delivered).toMatchObject({ delivered: true });
    const email = mocks.send.mock.calls.at(-1)![0];
    expect(email.text).toContain(`/shared-projects/accept#invite=${token}`);
    expect(email.html).not.toContain("<script>Project</script>");
  });
  it("projects only guest fields and offers no reviewer or organization controls to viewers", () => {
    expect(
      parseGuestProject({
        ...project,
        provider_credentials: ["secret"],
        members: ["internal"],
      }),
    ).toEqual(project);
    const html = renderToStaticMarkup(
      createElement(GuestProjectView, { project }),
    );
    expect(html).toContain("&lt;script&gt;hostile&lt;/script&gt;");
    for (const forbidden of [
      "Post feedback",
      "Sign off current version",
      "Run hosted",
      "Provider API key",
      "Accept change",
      'href="/settings"',
      'href="/members"',
    ])
      expect(html).not.toContain(forbidden);
    const reviewer = renderToStaticMarkup(
      createElement(GuestProjectView, {
        project: { ...project, role: "reviewer" },
      }),
    );
    expect(reviewer).toContain("Post feedback");
    expect(reviewer).toContain("Sign off current version");
    expect(reviewer).not.toContain("Run hosted");
  });
  it("does not forward hidden execution or admin payloads through guest feedback", async () => {
    mocks.request.mockResolvedValue({
      thread: { id: "thread", site_id: "site", version_id: "version" },
    });
    await sharedCommentAction("project", {
      site_id: "site",
      version_id: "version",
      content_hash: "hash",
      page: "/",
      body: "@dropway feedback only",
      execution: { credential_ref: "secret" },
    } as never);
    expect(mocks.request).toHaveBeenCalledWith("/project/threads", "POST", {
      site_id: "site",
      version_id: "version",
      content_hash: "hash",
      page: "/",
      body: "@dropway feedback only",
      anchor_kind: "page",
      anchor: {},
    });
  });
});

it("keeps shared-project navigation hidden until the authenticated API returns accepted projects", () => {
  expect(visibleNavLinks().some((link) => link.shared)).toBe(false);
  expect(visibleNavLinks(true, false).some((link) => link.shared)).toBe(false);
  expect(
    visibleNavLinks(false, true).some(
      (link) => link.href === "/shared-projects",
    ),
  ).toBe(true);
});

it("rejects UTF-8 feedback above the server byte limit before making an API request", async () => {
  const result = await sharedCommentAction("project", {
    site_id: "site",
    version_id: "version",
    content_hash: "hash",
    page: "/",
    body: "é".repeat(2001),
  });
  expect(result).toMatchObject({
    ok: false,
    message: expect.stringContaining("4,000"),
  });
  expect(mocks.request).not.toHaveBeenCalled();
});
