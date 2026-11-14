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
  UNROUTED,
  canComment,
  canDecideChange,
  canSignOffVersion,
  imageCommentAnchor,
  pageCommentAnchor,
  parseRole,
  shippedPreciseHtmlSelection,
  type LiveBaseline,
  type ProjectRoom,
  type ReviewCandidate,
  type ReviewThread,
  type SiteRights,
} from "@/lib/project-room/contract";

export interface RoomSiteOption {
  id: string;
  slug: string;
  liveUrl: string | null;
  currentVersionId: string | null;
}

export function RoomWorkspace({
  rooms,
  sites,
  threads,
  candidates,
  parentOrigin,
  notice,
  onCreateRoom,
  onCreateThread,
  onReply,
  onSignoff,
  onDecide,
}: {
  rooms: ProjectRoom[];
  sites: RoomSiteOption[];
  threads: ReviewThread[];
  candidates: ReviewCandidate[];
  parentOrigin: string;
  notice: string | null;
  onCreateRoom: (name: string) => void;
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
}) {
  const [roomId, setRoomId] = React.useState(rooms[0]?.ID ?? "");
  const [name, setName] = React.useState("");
  const [siteId, setSiteId] = React.useState(sites[0]?.id ?? "");
  const [versionId, setVersionId] = React.useState(sites[0]?.currentVersionId ?? "");
  const [page, setPage] = React.useState("/");
  const [body, setBody] = React.useState("");
  const [reply, setReply] = React.useState("");
  const frameRef = React.useRef<HTMLIFrameElement>(null);
  const room = rooms.find((item) => item.ID === roomId) ?? null;
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
    contentHash: null,
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

      <section className="space-y-2" aria-label="Members and sites">
        <h2 className="text-sm font-medium text-foreground">Members and sites</h2>
        <p className="text-sm text-muted-foreground">
          Only a room owner may manage members. This build has no HTTP route for adding a member,
          attaching a site, listing threads, listing candidates, resolving, or reanchoring.
        </p>
        <ul className="list-disc pl-5 text-xs text-muted-foreground">
          {UNROUTED.map((route) => <li key={route}>{route}</li>)}
        </ul>
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

      <section className="space-y-3" aria-label="Comments">
        <h2 className="text-sm font-medium text-foreground">Comments from this session</h2>
        <p className="text-sm text-muted-foreground">
          Existing threads are not listed. The API has no thread list route, so only threads
          returned by create in this session are shown, with the version they were stored on.
        </p>
        <ul className="space-y-3">
          {threads.map((thread) => (
            <li key={thread.ID} className="rounded-md border border-border p-3 text-sm">
              <p className="font-medium text-foreground">
                {thread.AnchorKind === "page" ? "Page comment" : "Image pin"} on {thread.Page}
              </p>
              <p className="text-xs text-muted-foreground">
                Version {thread.VersionID}, hash {thread.ContentHash}. State {thread.AnchorState}.
              </p>
              <ul className="mt-2 space-y-1">
                {thread.Replies.map((item) => (
                  <li key={item.ID}>{item.Body}</li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            const thread = threads[0];
            if (!room || !thread || reply.trim().length === 0) return;
            onReply(room.ID, thread.ID, reply.trim());
          }}
        >
          <Input aria-label="Reply" value={reply} onChange={(event) => setReply(event.target.value)} />
          <Button type="submit" disabled={!commentOk || threads.length === 0 || reply.trim().length === 0}>
            Reply
          </Button>
        </form>
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
        <h2 className="text-sm font-medium text-foreground">Proposed changes</h2>
        <p className="text-sm text-muted-foreground">
          Candidates are not listed by the API. A candidate shown here came from a response the
          server already returned.
        </p>
        {candidates.map((candidate) => (
          <CandidateReview
            key={candidate.ID}
            candidate={candidate}
            live={candidate.SiteID === siteId ? live : { versionId: null, contentHash: null }}
            canDecide={decideOk}
            onDecide={(decision) => {
              if (room) onDecide(room.ID, candidate, decision);
            }}
          />
        ))}
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
