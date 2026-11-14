"use client";
import * as React from "react";
import {
  RoomWorkspace,
  type RoomSiteOption,
} from "@/components/project-room/room-workspace";
import { MainNav } from "@/components/main-nav";
import { BrandMark } from "@/components/brand-mark";
import { Button } from "@/components/ui/button";
import type {
  ProjectRoom,
  ReviewThread,
  ReviewCandidate,
} from "@/lib/project-room/contract";
import type { StaticSnapshot } from "@/lib/collab/static-snapshot";

const noop = () => undefined;
const initialSites: RoomSiteOption[] = [
  {
    id: "site-launch",
    slug: "studio-launch",
    canEditContent: true,
    liveUrl: null,
    currentVersionId: "version-3",
    currentContentHash: "hash-3",
  },
  {
    id: "site-guide",
    slug: "brand-guidelines",
    canEditContent: true,
    liveUrl: null,
    currentVersionId: "version-2",
    currentContentHash: "hash-2",
  },
  {
    id: "site-notes",
    slug: "customer-research",
    canEditContent: true,
    liveUrl: null,
    currentVersionId: "version-1",
    currentContentHash: "hash-1",
  },
];
initialSites.push({
  id: "image-campaign",
  slug: "summer-campaign",
  label: "Summer campaign.png",
  kind: "image",
  canEditContent: true,
  liveUrl: null,
  currentVersionId: "version-1",
  currentContentHash: "hash-1",
});
const initialRooms: ProjectRoom[] = [
  {
    ID: "project-launch",
    OrgID: "demo-org",
    Name: "Studio launch",
    OwnerUserID: "demo-owner",
    Role: "editor",
    CreatedAt: "",
    UpdatedAt: "",
    Unread: 2,
  },
  {
    ID: "project-brand",
    OrgID: "demo-org",
    Name: "Brand refresh",
    OwnerUserID: "demo-owner",
    Role: "editor",
    CreatedAt: "",
    UpdatedAt: "",
    Unread: 0,
  },
];
const initialThreads: ReviewThread[] = [
  {
    ID: "thread-copy",
    RoomID: "project-launch",
    SiteID: "site-launch",
    VersionID: "version-3",
    ContentHash: "hash-3",
    Page: "/",
    AnchorKind: "text",
    Anchor: {
      quote: "Independent design.",
      start_offset: 0,
      end_offset: 19,
      block_id: "hero",
    },
    AnchorState: "anchored",
    Status: "open",
    CreatedBy: "demo-reviewer",
    CreatedAt: "2026-11-14T18:00:00Z",
    Replies: [
      {
        ID: "reply-1",
        AuthorID: "demo-reviewer",
        Body: "Can we make the headline more specific? “Independent design for ambitious teams” says more about who this is for.",
        CreatedAt: "2026-11-14T18:00:00Z",
      },
    ],
  },
  {
    ID: "thread-old",
    RoomID: "project-launch",
    SiteID: "site-launch",
    VersionID: "version-1",
    ContentHash: "hash-1",
    Page: "/",
    AnchorKind: "page",
    Anchor: {},
    AnchorState: "anchored",
    Status: "resolved",
    CreatedBy: "demo-reviewer",
    CreatedAt: "2026-11-12T18:00:00Z",
    Replies: [
      {
        ID: "reply-2",
        AuthorID: "demo-reviewer",
        Body: "The contact section needs a clearer call to action. Updated in version 3 — keeping this here for context.",
        CreatedAt: "2026-11-12T18:00:00Z",
      },
    ],
  },
];
initialThreads.push({
  ...initialThreads[0]!,
  ID: "thread-image",
  SiteID: "image-campaign",
  VersionID: "version-1",
  ContentHash: "hash-1",
  AnchorKind: "component",
  Anchor: { stable_id: "campaign-image" },
  Replies: [
    {
      ID: "reply-image",
      AuthorID: "demo-reviewer",
      Body: "Love this direction. Could we increase the contrast of the campaign subtitle?",
      CreatedAt: "",
    },
  ],
});
const initialCandidates: ReviewCandidate[] = [
  {
    ID: "candidate-copy",
    RoomID: "project-launch",
    SiteID: "site-launch",
    BaseVersionID: "version-3",
    BaseContentHash: "hash-3",
    ContentHash: "hash-next",
    DiffHash: "diff-copy",
    Summary:
      "Update the homepage headline to explain who the studio works with. No layout or navigation changes.",
    State: "pending",
    CreatedBy: "demo-agent",
    CreatedAt: "2026-11-14T18:30:00Z",
  },
];
const loadSnapshot = async (
  _roomId: string,
  siteId: string,
  versionId: string,
  page: string,
) => {
  const snapshotId = `${siteId}-${versionId}`;
  const title =
    siteId === "site-guide"
      ? "A considered identity."
      : siteId === "site-notes"
        ? "Listen first. Build second."
        : "Independent design. Lasting impact.";
  // Deliberately inert fixture, not a rendering of a production site's HTML.
  let html = `<!doctype html><html data-review-snapshot="${snapshotId}"><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><style>body{margin:0;background:#f3f0e9;color:#22271f;font:16px Georgia,serif}main{padding:44px 36px}nav{display:flex;justify-content:space-between;border-bottom:1px solid #c8c9bc;padding-bottom:22px;font:12px Arial;letter-spacing:1px}small{display:block;margin-top:48px;font:11px Arial;letter-spacing:2px;color:#68705e}h1{font-size:clamp(38px,5vw,60px);font-weight:normal;letter-spacing:-2px;line-height:1.05;margin:22px 0}p{max-width:370px;font:15px/1.7 Arial;color:#68705e}.work{margin-top:44px;background:#dfe4d4;padding:30px;min-height:130px}.work span{font:11px Arial;letter-spacing:2px}.work h2{font-size:28px;font-weight:normal}footer{padding-top:30px;font:12px Arial;color:#68705e}</style></head><body><main id="review-content"><nav><b>FORM & FIELD</b><span>WORK · STUDIO · CONTACT</span></nav><small>STRATEGY / IDENTITY / DIGITAL</small><h1 data-review-component="hero">${title}</h1><p>We partner with people who care about what they make. From the first conversation to the final detail, we help good ideas find their form.</p><div class="work" data-review-component="work"><span>SELECTED WORK — 2026</span><h2>A new perspective<br>for everyday places.</h2></div><footer>Have something in mind? Let’s talk.</footer></main></body></html>`;
  if (siteId.startsWith("image-")) {
    const artwork = `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="800"><rect width="1200" height="800" fill="#ece4d5"/><rect x="50" y="50" width="1100" height="700" rx="4" fill="#c8d5b6"/><circle cx="820" cy="260" r="170" fill="#e9a67e"/><path d="M50 650L350 300L650 650L850 430L1150 700V750H50Z" fill="#647b66"/><path d="M50 720L390 470L780 750H50Z" fill="#34574c"/><text x="100" y="150" font-family="Georgia" font-size="60" fill="#243c32">A slower summer.</text><text x="100" y="205" font-family="Arial" font-size="20" letter-spacing="5" fill="#34574c">FORM &amp; FIELD / CAMPAIGN 2026</text></svg>`;
    html = `<!doctype html><html data-review-snapshot="${snapshotId}"><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:"><style>body{margin:0;background:#e8e8e8;display:grid;place-items:center;min-height:100vh}main{width:100%}img{display:block;max-width:100%;height:auto;margin:auto}</style></head><body><main id="review-content"><img data-review-component="campaign-image" alt="Summer campaign artwork" src="data:image/svg+xml;base64,${btoa(artwork)}"></main></body></html>`;
  }
  const snapshot: StaticSnapshot = {
    snapshotId,
    siteId,
    versionId,
    page,
    contentHash:
      versionId === "version-3"
        ? "hash-3"
        : versionId === "version-2"
          ? "hash-2"
          : "hash-1",
    renderer: "static-v1",
    html,
    omittedAssets: 0,
  };
  return { ok: true as const, snapshot };
};
export function RoomsPreview() {
  const [empty, setEmpty] = React.useState(false);
  return (
    <>
      <header className="border-b border-border">
        <div className="container flex h-14 items-center justify-between">
          <span className="flex items-center gap-2 text-sm font-semibold">
            <BrandMark />
            Dropway
          </span>
          <MainNav collaborationEnabled />
          <span className="text-xs text-muted-foreground">Studio team</span>
        </div>
      </header>
      <main>
        <div className="fixed bottom-2 left-2 z-50 flex max-w-[85vw] flex-wrap items-center gap-3 rounded-lg border border-border bg-background/95 px-3 py-1 text-[10px] shadow-sm">
          <span>
            Local design preview · sample data only · no production changes
          </span>
          <Button size="sm" variant="outline" onClick={() => setEmpty(!empty)}>
            {empty ? "Show populated workspace" : "Show first-project setup"}
          </Button>
        </div>
        <DemoWorkspace key={String(empty)} empty={empty} />
      </main>
    </>
  );
}
function DemoWorkspace({ empty }: { empty: boolean }) {
  const [rooms, setRooms] = React.useState(empty ? [] : initialRooms);
  const [sites, setSites] = React.useState(initialSites);
  const [roomId, setRoomId] = React.useState("project-launch");
  const [attachments, setAttachments] = React.useState<
    Record<string, string[]>
  >({
    "project-launch": ["site-launch", "site-guide", "image-campaign"],
    "project-brand": ["site-guide"],
  });
  const [threads, setThreads] = React.useState(empty ? [] : initialThreads);
  const [candidates, setCandidates] = React.useState(
    empty ? [] : initialCandidates,
  );
  const [notice, setNotice] = React.useState<string | null>(null);
  const onSelectRoom = React.useCallback((id: string) => setRoomId(id), []);
  const attached = React.useMemo(
    () =>
      (attachments[roomId] ?? []).map((siteId) => ({
        siteId,
        createdBy: "demo-owner",
      })),
    [attachments, roomId],
  );
  return (
    <RoomWorkspace
      detailsRoomId={roomId}
      rooms={rooms}
      sites={sites}
      threads={threads}
      candidates={candidates}
      attachedSites={attached}
      members={[
        { userId: "Project owner", role: "owner" },
        { userId: "Design teammate", role: "editor" },
      ]}
      activity={[
        {
          id: "activity-review",
          threadId: null,
          kind: "candidate_created",
          actorUserId: "Dropway",
          createdAt: "2026-11-14T18:15:00Z",
          siteId: "site-launch",
          versionId: "version-3",
        },
        {
          id: "activity-resolved",
          threadId: "thread-copy",
          kind: "comment_resolved",
          actorUserId: "Project owner",
          createdAt: "2026-11-14T18:05:00Z",
          siteId: "site-launch",
          versionId: "version-3",
        },
        {
          id: "activity-1",
          kind: "thread_created",
          actorUserId: "Reviewer",
          createdAt: "Today, 1:00 PM",
          siteId: "site-launch",
          threadId: "thread-copy",
          versionId: "version-3",
        },
      ]}
      diffs={{
        "candidate-copy": {
          status: "verified",
          diff: {
            candidateId: "candidate-copy",
            siteId: "site-launch",
            baseVersionId: "version-3",
            baseContentHash: "hash-3",
            contentHash: "hash-next",
            verified: true,
            truncated: false,
            omittedFiles: 0,
            files: [
              {
                path: "index.html",
                change: "modified",
                binary: false,
                beforeSHA256: "before",
                afterSHA256: "after",
                beforeSize: 46,
                afterSize: 55,
                before: "<h1>Independent design. Lasting impact.</h1>",
                after: "<h1>Independent design for ambitious teams.</h1>",
                beforeTruncated: false,
                afterTruncated: false,
              },
            ],
          },
        },
      }}
      siteComments={[]}
      commentCursor={null}
      threadCursor={null}
      candidateCursor={null}
      parentOrigin="http://localhost:3100"
      notice={notice}
      orgEnabled
      executionConfigured={false}
      snapshotLoader={loadSnapshot}
      onSelectRoom={onSelectRoom}
      onSelectSite={noop}
      onEnableRooms={noop}
      onRefreshRoom={() => setNotice("Project is up to date.")}
      onSiteCreated={(site) => setSites((prev) => [...prev, site])}
      renderImageUpload={(ready) => (
        <Button
          type="button"
          variant="outline"
          onClick={() =>
            ready({
              ...initialSites.find((item) => item.kind === "image")!,
              id: `image-sample-${sites.length}`,
            })
          }
        >
          Upload image (sample)
        </Button>
      )}
      renderNewSite={(ready) => (
        <Button
          type="button"
          variant="outline"
          onClick={() =>
            ready({
              id: `site-demo-${sites.length}`,
              slug: `new-site-${sites.length}`,
              canEditContent: true,
              liveUrl: null,
              currentVersionId: "version-1",
              currentContentHash: "hash-1",
            })
          }
        >
          Create a new site (sample)
        </Button>
      )}
      onCreateRoom={async (name, ids = []) => {
        const id = `project-${rooms.length}`;
        setRooms((prev) => [
          ...prev,
          { ...initialRooms[0]!, ID: id, Name: name, Unread: 0 },
        ]);
        setAttachments((prev) => ({ ...prev, [id]: ids }));
        setNotice(`Created ${name}.`);
        return id;
      }}
      onAttachSite={(id, site) =>
        setAttachments((prev) => ({
          ...prev,
          [id]: [...new Set([...(prev[id] ?? []), site])],
        }))
      }
      onDetachSite={(id, site) =>
        setAttachments((prev) => ({
          ...prev,
          [id]: (prev[id] ?? []).filter((s) => s !== site),
        }))
      }
      onCreateThread={(input) => {
        setThreads((prev) => [
          ...prev,
          {
            ...initialThreads[0]!,
            ID: `thread-${Date.now()}`,
            RoomID: input.roomId,
            SiteID: input.siteId,
            VersionID: input.versionId,
            Page: input.page,
            AnchorKind: input.kind,
            Anchor: input.anchor,
            ContentHash:
              input.versionId === "version-3"
                ? "hash-3"
                : input.versionId === "version-2"
                  ? "hash-2"
                  : "hash-1",
            Replies: [
              {
                ID: `reply-${Date.now()}`,
                AuthorID: "You",
                Body: input.body,
                CreatedAt: "",
              },
            ],
          },
        ]);
        setNotice("Comment added.");
        return Promise.resolve(true);
      }}
      onReply={(id, thread, body) =>
        setThreads((prev) =>
          prev.map((t) =>
            t.ID === thread
              ? {
                  ...t,
                  Replies: [
                    ...t.Replies,
                    {
                      ID: `reply-${Date.now()}`,
                      AuthorID: "You",
                      Body: body,
                      CreatedAt: "",
                    },
                  ],
                }
              : t,
          ),
        )
      }
      onResolve={(_, id) =>
        setThreads((prev) =>
          prev.map((t) => (t.ID === id ? { ...t, Status: "resolved" } : t)),
        )
      }
      onDecide={(_, candidate, decision) =>
        setCandidates((prev) =>
          prev.map((c) =>
            c.ID === candidate.ID
              ? {
                  ...c,
                  State: decision === "approve" ? "approved" : "rejected",
                }
              : c,
          ),
        )
      }
      onApply={() =>
        setNotice("Sample change applied. Production is untouched.")
      }
      onSignoff={() => setNotice("Version reviewed.")}
      onLoadMoreComments={noop}
      onLoadMoreThreads={noop}
      onLoadMoreCandidates={noop}
      onSubmitCandidate={noop}
      onAddMember={noop}
      onReanchor={noop}
    />
  );
}
