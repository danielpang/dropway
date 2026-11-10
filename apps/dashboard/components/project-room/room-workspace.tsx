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
  threadPageUrl,
  type CandidateDiffView,
  type ProjectRoom,
  type ReviewCandidate,
  type ReviewThread,
  type SiteCommentItem,
  type SiteRights,
  type RoomActivity,
} from "@/lib/project-room/contract";

export interface RoomSiteOption {
  canEditContent?: boolean;
  id: string;
  slug: string;
  liveUrl: string | null;
  currentVersionId: string | null;
  currentContentHash: string | null;
}

export function RoomWorkspace({
  activity = [],
  activityCursor = null,
  onLoadMoreActivity,
  onMarkRead,
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
  onRefreshRoom,
  onSelectSite,
  onLoadMoreComments,
  onLoadMoreThreads,
  onLoadMoreCandidates,
  onCreateThread,
  onReply,
  onLoadMoreReplies,
  onLoadMoreRevisions,
  onSignoff,
  onDecide,
  onApply,
  busyCandidates = [],
  onSubmitCandidate,
  onAddMember,
  onUpdateMember,
  onRemoveMember,
  onAttachSite,
  onDetachSite,
  onResolve,
  onReanchor,
}: {
  activity?: RoomActivity[];
  activityCursor?: string | null;
  onLoadMoreActivity?: (roomId: string, cursor: string) => void;
  onMarkRead?: (roomId: string) => void;
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
  onRefreshRoom?: (roomId: string) => void;
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
  onLoadMoreRevisions?: (roomId: string, threadId: string, cursor: string) => void;
  onLoadMoreReplies?: (roomId: string, threadId: string, cursor: string) => void;
  onReply: (roomId: string, threadId: string, body: string) => void;
  onSignoff: (roomId: string, siteId: string, versionId: string) => void;
  onDecide: (roomId: string, candidate: ReviewCandidate, decision: "approve" | "reject") => void;
  onApply?: (roomId: string, candidate: ReviewCandidate) => void;
  busyCandidates?: string[];
  onSubmitCandidate: (input: {
    roomId: string;
    siteId: string;
    baseVersionId: string;
    contentHash: string;
    diffHash: string;
    summary: string;
  }) => void;
  onAddMember: (roomId: string, userId: string, role: "editor" | "reviewer" | "viewer") => void;
  onUpdateMember?: (roomId: string, userId: string, role: "editor" | "reviewer" | "viewer") => void;
  onRemoveMember?: (roomId: string, userId: string) => void;
  onAttachSite: (roomId: string, siteId: string) => void;
  onDetachSite: (roomId: string, siteId: string) => void;
  onResolve: (roomId: string, threadId: string) => void;
  onReanchor: (roomId: string, threadId: string, reason: string) => void;
}) {
  const [roomId, setRoomId] = React.useState(rooms[0]?.ID ?? "");
  const restoredRoomLink = React.useRef(false);
  React.useEffect(() => {
    if (restoredRoomLink.current || rooms.length === 0) return;
    restoredRoomLink.current = true;
    const requested = new URLSearchParams(window.location.search).get("room");
    if (requested && rooms.some((item) => item.ID === requested)) setRoomId(requested);
  }, [rooms]);
  const [name, setName] = React.useState("");
  const [siteId, setSiteId] = React.useState(sites[0]?.id ?? "");
  const [versionId, setVersionId] = React.useState(sites[0]?.currentVersionId ?? "");
  const [page, setPage] = React.useState("/");
  const [body, setBody] = React.useState("");
  const [replyByThread, setReplyByThread] = React.useState<Record<string, string>>({});
  const [memberId, setMemberId] = React.useState("");
  const [memberRole, setMemberRole] = React.useState<"editor" | "reviewer" | "viewer">("reviewer");
  const [reanchorReason, setReanchorReason] = React.useState("");
  const [linkedThreadId, setLinkedThreadId] = React.useState("");
  React.useEffect(() => {
    const readLink = () => setLinkedThreadId(window.location.hash.startsWith("#thread-") ? window.location.hash.slice(8) : "");
    readLink();
    window.addEventListener("hashchange", readLink);
    return () => window.removeEventListener("hashchange", readLink);
  }, []);
  const linkedThread = threads.find((item) => item.ID === linkedThreadId && item.RoomID === roomId) ?? null;
  const linkedSiteId = linkedThread?.SiteID;
  const linkedVersionId = linkedThread?.VersionID;
  const linkedPage = linkedThread?.Page;
  React.useEffect(() => {
    if (!linkedSiteId || !linkedVersionId || !linkedPage) return;
    setSiteId(linkedSiteId);
    setVersionId(linkedVersionId);
    setPage(linkedPage);
  }, [linkedSiteId, linkedVersionId, linkedPage]);
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
  const rights: SiteRights = { canRead: site !== null, canEditContent: site?.canEditContent === true };
  const commentOk = role ? canComment(role, rights) : false;
  const decideOk = role ? canDecideChange(role, rights) : false;
  const signoffOk = role ? canSignOffVersion(role, rights) : false;
  const originalUnavailable = linkedThread !== null && (linkedThread.VersionID !== site?.currentVersionId || linkedThread.ContentHash !== site?.currentContentHash);
  const previewUrl = site?.liveUrl ? (linkedThread ? threadPageUrl(site.liveUrl, linkedThread.Page) : site.liveUrl) : null;
  const preview = !originalUnavailable && previewUrl && previewFrameDecision(previewUrl, parentOrigin).ok ? previewUrl : null;
  const imagePreview = preview !== null && /\.(png|jpe?g|gif|webp)(\?|$)/i.test(preview);


  return (
    <div className="space-y-8">
      <header className="space-y-2">
        <h1 className="text-lg font-medium text-foreground">Project rooms</h1>
        <p className="text-sm text-muted-foreground">
          Same-org review of an existing site. Comments do not run an agent. Version sign-off is
          not approval of a change. Frame messages cannot approve or execute tools.
        </p>
        {notice ? <p role="status" className="text-sm text-foreground">{notice}</p> : null}
        {room ? <Button type="button" size="sm" variant="outline" disabled={!onRefreshRoom || busyCandidates.length > 0}
          onClick={() => onRefreshRoom?.(room.ID)}>Refresh room</Button> : null}
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
                    Role {item.Role}. Owner {item.OwnerUserID}. {item.Unread > 0 ? `${item.Unread} unread activities.` : ""}
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

      <section className="space-y-3" aria-label="Room activity">
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-sm font-medium text-foreground">Activity</h2>
          <Button type="button" size="sm" variant="outline" disabled={!room || !onMarkRead || room.Unread === 0}
            onClick={() => room && onMarkRead?.(room.ID)}>Mark room read</Button>
        </div>
        <p className="text-sm text-foreground">{candidates.filter((item) => item.State === "pending").length} pending decisions in the loaded changes.</p>
        <ul className="space-y-2 text-sm text-muted-foreground">
          {activity.map((item) => <li key={item.id}>
            <span>{item.kind.replaceAll("_", " ")} · {item.actorUserId} · {item.createdAt}</span>
            {item.threadId && room ? <> · <a className="underline" href={`/rooms?room=${encodeURIComponent(room.ID)}#thread-${encodeURIComponent(item.threadId)}`}>View thread</a></> : null}
            {item.versionId ? <span> · version {item.versionId}</span> : null}
          </li>)}
        </ul>
        {activityCursor && room ? <Button type="button" variant="outline" onClick={() => onLoadMoreActivity?.(room.ID, activityCursor)}>Load older activity</Button> : null}
      </section>

      <section className="space-y-3" aria-label="Members and sites">
        <h2 className="text-sm font-medium text-foreground">Members and sites</h2>
        <p className="text-sm text-muted-foreground">
          Room owners manage members. Owners and editors can attach sites; only owners can detach them. Detach does not
          delete the site or its candidates.
        </p>
        <ul className="text-sm text-foreground">
          {members.map((member) => (
            <li key={member.userId} className="flex flex-wrap items-center gap-2 py-1">
              <span>{member.userId}</span>
              {member.role === "owner" ? <span>owner</span> : <>
                <select
                  aria-label={`Role for ${member.userId}`}
                  value={member.role}
                  disabled={role !== "owner" || !onUpdateMember}
                  onChange={(event) => room && onUpdateMember?.(room.ID, member.userId, event.target.value as "editor" | "reviewer" | "viewer")}
                  className="rounded-md border border-border bg-background px-2 py-1 text-sm"
                >
                  <option value="editor">editor</option>
                  <option value="reviewer">reviewer</option>
                  <option value="viewer">viewer</option>
                </select>
                <Button type="button" size="sm" variant="outline" disabled={role !== "owner" || !onRemoveMember}
                  onClick={() => room && onRemoveMember?.(room.ID, member.userId)}>Remove member</Button>
              </>}
            </li>
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
          disabled={(role !== "owner" && role !== "editor") || !room || siteId.trim().length === 0 || attachedSites.some((item) => item.siteId === siteId)}
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
        {linkedThread ? <p className="text-sm text-foreground">
          Original thread context: version {linkedThread.VersionID}, page {linkedThread.Page}, content hash {linkedThread.ContentHash}.
          {originalUnavailable ? " This is an older version. Its immutable preview is unavailable here; the current live page is not shown in its place." : ""}
        </p> : null}
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
            {originalUnavailable ? "The original version remains pinned in the thread below." : "This site URL is not an allowed https content origin, so the frame stays unloaded."}
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
                setLinkedThreadId("");
                setSiteId(event.target.value);
                const next = sites.find((item) => item.id === event.target.value);
                setVersionId(next?.currentVersionId ?? "");
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
          {threads.filter((thread) => thread.RoomID === roomId).map((thread) => (
            <li id={`thread-${thread.ID}`} key={thread.ID} className="space-y-2 rounded-md border border-border p-3 text-sm">
              <p className="font-medium text-foreground">
                {thread.AnchorKind === "page" ? "Page comment" : thread.AnchorKind === "point" ? "Image pin" : thread.AnchorKind} on {thread.Page}
              </p>
              <p className="text-xs text-muted-foreground">
                Version {thread.VersionID}, hash {thread.ContentHash}. State {thread.AnchorState}. Status {thread.Status}.
                {" "}<a href={`/rooms?room=${encodeURIComponent(thread.RoomID)}#thread-${encodeURIComponent(thread.ID)}`} className="underline">Link to thread</a>
              </p>
              <details className="space-y-2 text-sm">
                <summary className="cursor-pointer text-muted-foreground">Anchor history</summary>
                <p className="text-xs text-muted-foreground">Revisions record changes to the anchor. The original version, page and content hash above remain pinned.</p>
                <ol className="space-y-2">
                  {(thread.Revisions ?? []).map((revision) => <li key={revision.id} className="rounded-md border border-border p-2">
                    <p>{revision.anchorKind} · {revision.anchorState} · page {revision.page}</p>
                    <p className="text-xs text-muted-foreground">{revision.createdAt}</p>
                    <p className="whitespace-pre-wrap">{untrustedText(revision.reason) || "No reason recorded."}</p>
                  </li>)}
                </ol>
                {thread.NextRevisionCursor ? <Button type="button" size="sm" variant="outline"
                  onClick={() => onLoadMoreRevisions?.(thread.RoomID, thread.ID, thread.NextRevisionCursor!)}>Load older anchor revisions</Button> : null}
              </details>
              <ul className="space-y-2">
                {thread.Replies.map((item) => (
                  <li key={item.ID}>
                    <ReplyText body={untrustedText(item.Body)} />
                  </li>
                ))}
              </ul>
              {thread.NextReplyCursor ? <Button type="button" size="sm" variant="outline"
                onClick={() => onLoadMoreReplies?.(thread.RoomID, thread.ID, thread.NextReplyCursor!)}>Load older replies</Button> : null}
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
                  disabled={!commentOk || !room || thread.Status === "resolved"}
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
            {site && !site.canEditContent ? " You can review this site, but its content-edit permission is restricted. Room membership does not grant editing rights." : ""}
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
        {candidates.filter((candidate) => candidate.RoomID === roomId).map((candidate) => (
          <CandidateReview
            key={candidate.ID}
            candidate={candidate}
            live={{ versionId: sites.find((item) => item.id === candidate.SiteID)?.currentVersionId ?? null, contentHash: sites.find((item) => item.id === candidate.SiteID)?.currentContentHash ?? null }}
            diff={diffs[candidate.ID] ?? { status: "loading" }}
            canDecide={Boolean(role && canDecideChange(role, { canRead: sites.some((item) => item.id === candidate.SiteID), canEditContent: sites.find((item) => item.id === candidate.SiteID)?.canEditContent === true }))}
            busy={busyCandidates.includes(candidate.ID)}
            onApply={onApply ? () => onApply(candidate.RoomID, candidate) : undefined}
            onDecide={(decision) => {
              if (room && room.ID === candidate.RoomID) onDecide(candidate.RoomID, candidate, decision);
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
