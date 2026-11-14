import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { RoomWorkspace } from "@/components/project-room/room-workspace";
vi.mock("@/components/project-room/new-project-site", () => ({
  NewProjectSite: () => createElement("button", null, "Create a new site"),
}));
const noop = () => undefined;
const props: Parameters<typeof RoomWorkspace>[0] = {
  rooms: [],
  sites: [
    {
      id: "editable",
      slug: "launch-site",
      canEditContent: true,
      liveUrl: null,
      currentVersionId: "v1",
      currentContentHash: "h1",
    },
    {
      id: "readonly",
      slug: "restricted-site",
      canEditContent: false,
      liveUrl: null,
      currentVersionId: "v1",
      currentContentHash: "h1",
    },
  ],
  threads: [],
  candidates: [],
  diffs: {},
  members: [],
  attachedSites: [],
  siteComments: [],
  commentCursor: null,
  threadCursor: null,
  candidateCursor: null,
  parentOrigin: "https://app.dropway.dev",
  notice: null,
  orgEnabled: true,
  onEnableRooms: noop,
  onCreateRoom: noop,
  onSelectRoom: noop,
  onSelectSite: noop,
  onLoadMoreComments: noop,
  onLoadMoreThreads: noop,
  onLoadMoreCandidates: noop,
  onCreateThread: noop,
  onReply: noop,
  onSignoff: noop,
  onDecide: noop,
  onSubmitCandidate: noop,
  onAddMember: noop,
  onAttachSite: noop,
  onDetachSite: noop,
  onResolve: noop,
  onReanchor: noop,
};
const room = {
  ID: "room",
  OrgID: "org",
  Name: "Launch",
  OwnerUserID: "owner",
  Role: "editor",
  CreatedAt: "",
  UpdatedAt: "",
  Unread: 0,
};
describe("project workspace states", () => {
  it("offers existing or new sites without rendering empty review tools", () => {
    const html = renderToStaticMarkup(createElement(RoomWorkspace, props));
    expect(html).toContain("Create a project");
    expect(html).toContain("Create a new site");
    expect(html).toContain("launch-site");
    expect(html).not.toContain("restricted-site");
    expect(html).not.toContain('aria-label="Comment on this page"');
    expect(html).not.toContain('aria-label="Candidate content hash"');
    expect(html).not.toContain("iframe");
    expect(html).not.toContain("Re-enable projects");
  });
  it("preserves explicit organization opt-out", () => {
    const html = renderToStaticMarkup(
      createElement(RoomWorkspace, { ...props, orgEnabled: false }),
    );
    expect(html).toContain("Re-enable projects");
    expect(html).not.toContain('aria-label="Create project"');
  });
  it("keeps old room controls hidden until the selected room has loaded", () => {
    const html = renderToStaticMarkup(
      createElement(RoomWorkspace, {
        ...props,
        rooms: [room],
        detailsRoomId: null,
        attachedSites: [{ siteId: "editable", createdBy: "owner" }],
      }),
    );
    expect(html).toContain("Loading project");
    expect(html).not.toContain('aria-label="Comment on this page"');
  });
  it("shows retry after a failed load instead of an endless spinner", () => {
    const html = renderToStaticMarkup(
      createElement(RoomWorkspace, {
        ...props,
        rooms: [room],
        detailsRoomId: null,
        roomLoadError: { roomId: "room", message: "Request failed" },
      }),
    );
    expect(html).toContain("Retry loading project");
    expect(html).not.toContain("Loading project…");
  });
  it("does not allow comments on an unpublished site", () => {
    const html = renderToStaticMarkup(
      createElement(RoomWorkspace, {
        ...props,
        rooms: [room],
        sites: [
          {
            ...props.sites[0]!,
            currentVersionId: null,
            currentContentHash: null,
          },
        ],
        attachedSites: [{ siteId: "editable", createdBy: "owner" }],
      }),
    );
    expect(html).toContain("first upload");
    expect(html).toMatch(
      /<button[^>]*disabled=""[^>]*>Comment on this page<\/button>/,
    );
  });
});
