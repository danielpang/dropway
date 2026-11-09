// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Same-org room surface. Page comments and parent-side image pins are the
// comments this phase can create. Text and component selection stay unmet.
// Frame messages are not wired to approval or execution.

"use client";

import * as React from "react";

import { CandidatePreviewFrame } from "@/components/collab/candidate-preview-frame";
import { CandidateReview } from "@/components/project-room/candidate-review";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { captureImagePoint, previewFrameDecision } from "@/lib/collab";
import {
  EXECUTION_BLOCKERS,
  canComment,
  canDecideChange,
  canSignOffVersion,
  imageCommentAnchor,
  pageCommentAnchor,
  parseRole,
  shippedPreciseHtmlSelection,
  untrustedText,
  type CandidateDiffView,
  type LiveBaseline,
  type ProjectRoom,
  type ReviewCandidate,
  type ReviewThread,
  type SiteCommentItem,
  type SiteRights,
} from "@/lib/project-room/contract";

export interface RoomSiteOption {
  id: string;
  slug: string;
  liveUrl: string | null;
  currentVersionId: string | null;
  currentContentHash: string | null;
}

export function RoomWorkspace({
  rooms,
  sites,
  threads,
  candidates,
  diffs,
  members,
  attachedSites,
  siteComments,
  commentCursor,
  threadCursor,
  candidateCursor,
  parentOrigin,
  notice,
  orgEnabled,
  onEnableRooms,
  onCreateRoom,
  onSelectRoom,
  onSelectSite,
  onLoadMoreComments,
  onLoadMoreThreads,
  onLoadMoreCandidates,
  onCreateThread,
  onReply,
  onSignoff,
  onDecide,
  onSubmitCandidate,
  onAddMember,
  onAttachSite,
  onDetachSite,
  onResolve,
  onReanchor,
}: {
  rooms: ProjectRoom[];
  sites: RoomSiteOption[];
  threads: ReviewThread[];
  candidates: ReviewCandidate[];
  diffs: Record<string, CandidateDiffView>;
  members: { userId: string; role: string }[];
  attachedSites: { siteId: string; createdBy: string }[];
  siteComments: SiteCommentItem[];
  commentCursor: string | null;
  threadCursor: string | null;
  candidateCursor: string | null;
  parentOrigin: string;
  notice: string | null;
  orgEnabled: boolean;
  onEnableRooms: () => void;
  onCreateRoom: (name: string) => void;
  onSelectRoom: (roomId: string) => void;
  onSelectSite: (siteId: string) => void;
  onLoadMoreComments: (siteId: string, cursor: string) => void;
  onLoadMoreThreads: (roomId: string, cursor: string) => void;
  onLoadMoreCandidates: (roomId: string, cursor: string) => void;
  onCreateThread: (input: {
    roomId: string;
    siteId: string;
    versionId: string;
    page: string;
    kind: "page" | "point";
    anchor: unknown;
    body: string;
  }) => void;
  onReply: (roomId: string, threadId: string, body: string) => void;
  onSignoff: (roomId: string, siteId: string, versionId: string) => void;
  onDecide: (roomId: string, candidate: ReviewCandidate, decision: "approve" | "reject") => void;
  onSubmitCandidate: (input: {
    roomId: string;
    siteId: string;
    baseVersionId: string;
    contentHash: string;
    diffHash: string;
    summary: string;
  }) => void;
  onAddMember: (roomId: string, userId: string, role: "editor" | "reviewer" | "viewer") => void;
  onAttachSite: (roomId: string, siteId: string) => void;
  onDetachSite: (roomId: string, siteId: string) => void;
  onResolve: (roomId: string, threadId: string) => void;
  onReanchor: (roomId: string, threadId: string, reason: string) => void;
}) {
  const [roomId, setRoomId] = React.useState(rooms[0]?.ID ?? "");
  const [name, setName] = React.useState("");
  const [siteId, setSiteId] = React.useState(sites[0]?.id ?? "");
  const [versionId, setVersionId] = React.useState(sites[0]?.currentVersionId ?? "");
  const [page, setPage] = React.useState("/");
  const [body, setBody] = React.useState("");
  const [replyByThread, setReplyByThread] = React.useState<Record<string, string>>({});
  const [memberId, setMemberId] = React.useState("");
  const [memberRole, setMemberRole] = React.useState<"editor" | "reviewer" | "viewer">("reviewer");
  const [reanchorReason, setReanchorReason] = React.useState("");
  const frameRef = React.useRef<HTMLIFrameElement>(null);
  const room = rooms.find((item) => item.ID === roomId) ?? null;
  React.useEffect(() => {
    if (!roomId && rooms[0]?.ID) setRoomId(rooms[0].ID);
  }, [roomId, rooms]);
  React.useEffect(() => {
    if (roomId) onSelectRoom(roomId);
  }, [onSelectRoom, roomId]);
  React.useEffect(() => {
    if (siteId) onSelectSite(siteId);
  }, [onSelectSite, siteId]);
  const role = room ? parseRole(room.Role) : null;
  const site = sites.find((item) => item.id === siteId) ?? null;
  const rights: SiteRights = { canRead: true, canEditContent: role === "owner" || role === "editor" };
  const commentOk = role ? canComment(role, rights) : false;
  const decideOk = role ? canDecideChange(role, rights) : false;
  const signoffOk = role ? canSignOffVersion(role, rights) : false;
  const preview = site?.liveUrl && previewFrameDecision(site.liveUrl, parentOrigin).ok ? site.liveUrl : null;
  const imagePreview = preview !== null && /\.(png|jpe?g|gif|webp)(\?|$)/i.test(preview);
  const live: LiveBaseline = {
    versionId: site?.currentVersionId ?? null,
    contentHash: site?.currentContentHash ?? null,
  };

  return (
    <div className="space-y-8">
      <header className="space-y-2">
        <h1 className="text-lg font-medium text-foreground">Project rooms</h1>
        <p className="text-sm text-muted-foreground">
          Same-org review of an existing site. Comments do not run an agent. Version sign-off is
          not approval of a change. Frame messages cannot approve or execute tools.
        </p>
        {notice ? <p role="status" className="text-sm text-foreground">{notice}</p> : null}
        {orgEnabled ? null : (
          <Button type="button" onClick={onEnableRooms}>Enable project rooms for this organization</Button>
        )}
      </header>

      <section className="space-y-3" aria-label="Rooms">
        <h2 className="text-sm font-medium text-foreground">Rooms you can read</h2>
        {rooms.length === 0 ? (
          <p className="text-sm text-muted-foreground">No rooms were returned.</p>
        ) : (
          <ul className="space-y-2">
            {rooms.map((item) => (
              <li key={item.ID}>
                <button
                  type="button"
                  aria-pressed={item.ID === roomId}
                  onClick={() => setRoomId(item.ID)}
                  className="w-full rounded-md border border-border px-3 py-2 text-left text-sm"
                >
                  <span className="font-medium text-foreground">{item.Name}</span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">
                    Role {item.Role}. Owner {item.OwnerUserID}.
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (name.trim()) onCreateRoom(name.trim());
          }}
        >
          <Input aria-label="New room name" value={name} onChange={(event) => setName(event.target.value)} />
          <Button type="submit" disabled={name.trim().length === 0}>Create room</Button>
        </form>
      </section>

      <section className="space-y-3" aria-label="Members and sites">
        <h2 className="text-sm font-medium text-foreground">Members and sites</h2>
        <p className="text-sm text-muted-foreground">
          Only a room owner can add or remove members and attach or detach sites. Detach does not
          delete the site or its candidates.
        </p>
        <ul className="text-sm text-foreground">
          {members.map((member) => (
            <li key={member.userId}>{member.userId} · {member.role}</li>
          ))}
        </ul>
        <ul className="text-sm text-foreground">
          {attachedSites.map((item) => (
            <li key={item.siteId} className="flex items-center gap-2">
              <span>{item.siteId}</span>
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={role !== "owner" || !room}
                onClick={() => room && onDetachSite(room.ID, item.siteId)}
              >
                Detach site
              </Button>
            </li>
          ))}
        </ul>
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (!room || role !== "owner" || memberId.trim().length === 0) return;
            onAddMember(room.ID, memberId.trim(), memberRole);
          }}
        >
          <Input aria-label="Member user id" value={memberId} onChange={(event) => setMemberId(event.target.value)} />
          <select
            aria-label="Member role"
            value={memberRole}
            onChange={(event) => setMemberRole(event.target.value as "editor" | "reviewer" | "viewer")}
            className="rounded-md border border-border bg-background px-2 py-1 text-sm"
          >
            <option value="editor">editor</option>
            <option value="reviewer">reviewer</option>
            <option value="viewer">viewer</option>
          </select>
          <Button type="submit" disabled={role !== "owner" || memberId.trim().length === 0}>Add member</Button>
        </form>
        <Button
          type="button"
          variant="outline"
          disabled={role !== "owner" || !room || siteId.trim().length === 0}
          onClick={() => room && onAttachSite(room.ID, siteId.trim())}
        >
          Attach site
        </Button>
      </section>

      <section className="space-y-3" aria-label="Preview and comments">
        <h2 className="text-sm font-medium text-foreground">Preview</h2>
        <p className="text-sm text-muted-foreground">
          The preview is a sandboxed page. It is not the identity of a proposed change.
          Opaque HTML cannot take a precise text or component selection, so those controls are
          absent. A page comment is only a page comment.
          {shippedPreciseHtmlSelection() ? "" : ""}
        </p>
        <ul className="text-sm text-muted-foreground">
          <li>Page comment: available on this side.</li>
          <li>Image pin: available when the URL is an allowed content image, measured here.</li>
          <li>Text highlight: not available on sandboxed HTML.</li>
          <li>Component id: not available on sandboxed HTML.</li>
        </ul>
        {preview ? (
          <CandidatePreviewFrame
            src={preview}
            parentOrigin={parentOrigin}
            title="Site preview"
            frameRef={frameRef}
            onLoad={() => undefined}
          />
        ) : (
          <p className="text-sm text-muted-foreground">
            This site URL is not an allowed https content origin, so the frame stays unloaded.
          </p>
        )}
        {imagePreview && preview ? (
          <ImagePin
            src={preview}
            disabled={!commentOk || !room}
            onPin={(anchor) => {
              if (!room || !site || !versionId.trim() || !body.trim()) return;
              onCreateThread({
                roomId: room.ID,
                siteId: site.id,
                versionId: versionId.trim(),
                page: page.trim() || "/",
                kind: "point",
                anchor,
                body: body.trim(),
              });
            }}
          />
        ) : null}
        <form
          className="space-y-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (!room || !commentOk) return;
            const anchor = pageCommentAnchor();
            onCreateThread({
              roomId: room.ID,
              siteId: siteId.trim(),
              versionId: versionId.trim(),
              page: page.trim() || "/",
              kind: anchor.kind,
              anchor: anchor.anchor,
              body: body.trim(),
            });
          }}
        >
          <label className="block text-sm">
            Site
            <select
              aria-label="Site"
              className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1"
              value={siteId}
              onChange={(event) => {
                setSiteId(event.target.value);
                const next = sites.find((item) => item.id === event.target.value);
                if (next?.currentVersionId) setVersionId(next.currentVersionId);
              }}
            >
              {sites.map((item) => (
                <option key={item.id} value={item.id}>{item.slug}</option>
              ))}
            </select>
          </label>
          <Input aria-label="Version id" value={versionId} onChange={(event) => setVersionId(event.target.value)} />
          <Input aria-label="Page path" value={page} onChange={(event) => setPage(event.target.value)} />
          <textarea
            aria-label="Comment on this page"
            value={body}
            onChange={(event) => setBody(event.target.value)}
            className="min-h-24 w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
          />
          <Button type="submit" disabled={!commentOk || body.trim().length === 0}>
            Comment on this page
          </Button>
          {commentOk ? null : (
            <p className="text-xs text-muted-foreground">This room role cannot comment.</p>
          )}
        </form>
      </section>

      <section className="space-y-3" aria-label="Site comment page">
        <h2 className="text-sm font-medium text-foreground">Site comment page</h2>
        <p className="text-sm text-muted-foreground">
          This page is the site comment feed. Every body is untrusted and shown as text.
        </p>
        <ul className="space-y-2">
          {siteComments.map((item) => (
            <li key={item.id} className="space-y-1 rounded-md border border-border p-3 text-sm">
              <p className="text-xs text-muted-foreground">
                {item.source}
                {item.anchorKind ? ` · ${item.anchorKind}` : ""}
                {item.page ? ` · ${item.page}` : ""}
                {item.status ? ` · ${item.status}` : ""}
                {item.versionId ? ` · version ${item.versionId}` : ""}
                {item.contentHash ? ` · hash ${item.contentHash}` : ""}
                {` · ${item.createdAt}`}
              </p>
              {item.body ? (
                <ReplyText body={untrustedText(item.body)} />
              ) : (
                <p className="text-sm text-muted-foreground">This comment page item did not include a body.</p>
              )}
            </li>
          ))}
        </ul>
        {commentCursor ? (
          <Button type="button" variant="outline" onClick={() => onLoadMoreComments(siteId, commentCursor)}>
            Load older site comments
          </Button>
        ) : null}
      </section>

      <section className="space-y-3" aria-label="Comments">
        <h2 className="text-sm font-medium text-foreground">Comments</h2>
        <p className="text-sm text-muted-foreground">
          Threads come from the room list. Each row keeps the immutable version and content hash.
          Comment text is untrusted and shown as text.
        </p>
        <ul className="space-y-3">
          {threads.map((thread) => (
            <li key={thread.ID} className="space-y-2 rounded-md border border-border p-3 text-sm">
              <p className="font-medium text-foreground">
                {thread.AnchorKind === "page" ? "Page comment" : thread.AnchorKind === "point" ? "Image pin" : thread.AnchorKind} on {thread.Page}
              </p>
              <p className="text-xs text-muted-foreground">
                Version {thread.VersionID}, hash {thread.ContentHash}. State {thread.AnchorState}. Status {thread.Status}.
              </p>
              <ul className="space-y-2">
                {thread.Replies.map((item) => (
                  <li key={item.ID}>
                    <ReplyText body={untrustedText(item.Body)} />
                  </li>
                ))}
              </ul>
              <form
                className="flex flex-wrap gap-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  const text = (replyByThread[thread.ID] ?? "").trim();
                  if (!room || !commentOk || text.length === 0) return;
                  onReply(room.ID, thread.ID, text);
                }}
              >
                <Input
                  aria-label={`Reply to ${thread.ID}`}
                  value={replyByThread[thread.ID] ?? ""}
                  onChange={(event) => setReplyByThread((prev) => ({ ...prev, [thread.ID]: event.target.value }))}
                />
                <Button type="submit" disabled={!commentOk || (replyByThread[thread.ID] ?? "").trim().length === 0}>
                  Reply
                </Button>
              </form>
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={!commentOk || !room}
                  onClick={() => room && onResolve(room.ID, thread.ID)}
                >
                  Resolve
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={!commentOk || !room || reanchorReason.trim().length === 0}
                  onClick={() => room && onReanchor(room.ID, thread.ID, reanchorReason.trim())}
                >
                  Append page reanchor
                </Button>
              </div>
            </li>
          ))}
        </ul>
        {threadCursor && room ? (
          <Button type="button" variant="outline" onClick={() => onLoadMoreThreads(room.ID, threadCursor)}>
            Load older threads
          </Button>
        ) : null}
        <Input
          aria-label="Reanchor reason"
          value={reanchorReason}
          onChange={(event) => setReanchorReason(event.target.value)}
          placeholder="Why this page reanchor"
        />
        <p className="text-xs text-muted-foreground">
          Reanchor appends a page revision and leaves the original pin in place. It is not a text
          or component selection.
        </p>
      </section>

      <section className="space-y-3" aria-label="Version sign-off">
        <h2 className="text-sm font-medium text-foreground">Version sign-off</h2>
        <p className="text-sm text-muted-foreground">
          Sign-off records that you looked at a version. It does not approve a change and it does
          not publish.
        </p>
        <Button
          type="button"
          variant="outline"
          disabled={!signoffOk || !room || versionId.trim().length === 0}
          onClick={() => {
            if (room) onSignoff(room.ID, siteId.trim(), versionId.trim());
          }}
        >
          Record version sign-off
        </Button>
      </section>

      <section className="space-y-3" aria-label="Proposed changes">
        <form
          className="space-y-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (!room || !decideOk) return;
            const data = new FormData(event.currentTarget);
            onSubmitCandidate({
              roomId: room.ID,
              siteId: siteId.trim(),
              baseVersionId: versionId.trim(),
              contentHash: String(data.get("content-hash") ?? ""),
              diffHash: String(data.get("diff-hash") ?? ""),
              summary: String(data.get("candidate-summary") ?? ""),
            });
          }}
        >
          <p className="text-sm text-muted-foreground">
            Recording a candidate does not edit the site. Approval is a separate human decision.
          </p>
          <Input name="candidate-summary" aria-label="Candidate summary" />
          <Input name="content-hash" aria-label="Candidate content hash" />
          <Input name="diff-hash" aria-label="Candidate diff hash" />
          <Button type="submit" disabled={!decideOk || !room}>Record candidate</Button>
        </form>
        <h2 className="text-sm font-medium text-foreground">Proposed changes</h2>
        <p className="text-sm text-muted-foreground">
          Candidates are loaded from the room. The summary and the diff text are untrusted and
          shown as text.
        </p>
        {candidates.map((candidate) => (
          <CandidateReview
            key={candidate.ID}
            candidate={candidate}
            live={candidate.SiteID === siteId ? live : { versionId: null, contentHash: null }}
            diff={diffs[candidate.ID] ?? { status: "loading" }}
            canDecide={decideOk}
            onDecide={(decision) => {
              if (room) onDecide(room.ID, candidate, decision);
            }}
          />
        ))}
        {candidateCursor && room ? (
          <Button type="button" variant="outline" onClick={() => onLoadMoreCandidates(room.ID, candidateCursor)}>
            Load older candidates
          </Button>
        ) : null}
      </section>

      <section className="space-y-2" aria-label="Execution">
        <h2 className="text-sm font-medium text-foreground">Execution</h2>
        <p className="text-sm text-muted-foreground">
          Hosted execution is disabled. No provider key is collected.
        </p>
        <ul className="list-disc pl-5 text-sm text-muted-foreground">
          {EXECUTION_BLOCKERS.map((blocker) => <li key={blocker}>{blocker}</li>)}
        </ul>
      </section>
    </div>
  );
}

function ReplyText({ body }: { body: string }) {
  const [open, setOpen] = React.useState(false);
  const long = body.length > 280;
  const shown = long && !open ? `${body.slice(0, 280)}…` : body;
  return (
    <div className="space-y-1">
      <p className="whitespace-pre-wrap text-sm text-foreground">{shown}</p>
      {long ? (
        <button type="button" className="text-xs text-primary" onClick={() => setOpen((value) => !value)}>
          {open ? "Collapse reply" : "Expand reply"}
        </button>
      ) : null}
    </div>
  );
}

function ImagePin({
  src,
  disabled,
  onPin,
}: {
  src: string;
  disabled: boolean;
  onPin: (anchor: Record<string, unknown>) => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      className="block"
      onClick={(event) => {
        const img = event.currentTarget.querySelector("img");
        if (!img) return;
        const rect = img.getBoundingClientRect();
        const captured = captureImagePoint(
          { clientX: event.clientX, clientY: event.clientY },
          {
            imageId: src,
            left: rect.left,
            top: rect.top,
            width: rect.width,
            height: rect.height,
            naturalWidth: img.naturalWidth,
            naturalHeight: img.naturalHeight,
          },
        );
        if (!captured.ok) return;
        const width = Math.max(1, Math.round(rect.width));
        const height = Math.max(1, Math.round(rect.height));
        const anchor = imageCommentAnchor({
          normalizedX: captured.image.x / captured.image.naturalWidth,
          normalizedY: captured.image.y / captured.image.naturalHeight,
          viewportWidth: width,
          viewportHeight: height,
          renditionWidth: width,
          renditionHeight: height,
          scrollX: 0,
          scrollY: 0,
          intrinsicWidth: captured.image.naturalWidth,
          intrinsicHeight: captured.image.naturalHeight,
          imageX: Math.round(captured.image.x),
          imageY: Math.round(captured.image.y),
        });
        if (anchor.ok) onPin(anchor.anchor);
      }}
    >
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={src} alt="Preview image for a pin" className="max-h-64 max-w-full" />
      <span className="mt-1 block text-xs text-muted-foreground">Place an image pin</span>
    </button>
  );
}
