// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Project workspace. Immutable previews and comments stay separate from human
// change approval and publication. No frame message can authorize either.

"use client";

import * as React from "react";
import { Tooltip } from "@/components/ui/tooltip";
import {
  LoaderCircle,
  FolderOpen,
  Users,
  UserRound,
  Unlink,
  Trash2,
  CheckCheck,
  ArrowUpRight,
  ChevronDown,
  Send,
  MessageSquare,
  Plus,
  PanelsTopLeft,
  GitPullRequest,
  History,
  Settings,
  ExternalLink,
  Link,
  Check,
  X,
} from "lucide-react";
import { RoomDirectory } from "./room-directory";
import type { RoomLocation } from "@/lib/project-room/use-room-navigation";
import { UploadProjectImage } from "./upload-project-image";
import { NewProjectSite } from "./new-project-site";
import { ProjectSharing } from "./project-sharing";
import { MentionTextarea } from "./mention-textarea";
import { mentionOptions } from "@/lib/project-room/mentions";
import { threadConversation } from "@/lib/project-room/thread-conversation";
import { AICommentStatus } from "./ai-comment-status";
import type { HostedJob } from "@/lib/project-room/hosted";
import { MentionExecutionControls } from "./mention-execution";
import {
  hasDropwayMention,
  type MentionExecution,
} from "@/lib/project-room/hosted";

import { AIConnections } from "./ai-connections";
import { HostedExecution } from "@/components/project-room/hosted-execution";
import { StaticReview } from "@/components/collab/static-review";
import { loadRoomPreview, loadRoomRevisions } from "@/lib/project-room/preview-client";
import { createPreviewCache, type SnapshotLoader } from "@/lib/project-room/preview-cache";
import type {
  StaticSelection,
  StaticSnapshot,
} from "@/lib/collab/static-snapshot";
import { CandidateReview } from "@/components/project-room/candidate-review";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import {
  canComment,
  canDecideChange,
  pageCommentAnchor,
  parseRole,
  untrustedText,
  type CandidateDiffView,
  type ProjectRoom,
  type ReviewCandidate,
  type ReviewThread,
  type SiteRights,
  type RoomActivity,
} from "@/lib/project-room/contract";

export interface RoomSiteOption {
  kind?: "site" | "image";
  label?: string;
  canEditContent?: boolean;
  id: string;
  slug: string;
  liveUrl: string | null;
  currentVersionId: string | null;
  currentContentHash: string | null;
}

export function RoomWorkspace({
  navigation,
  onNavigate,
  discussionLoading = false,
  discussionError,
  activity = [],
  activityCursor = null,
  onLoadMoreActivity,
  onMarkRead,
  rooms,
  detailsRoomId = rooms[0]?.ID ?? null,
  roomLoadError,
  sites,
  threads,
  candidates,
  diffs,
  onRequestDiff,
  members,
  attachedSites,
  threadCursor,
  candidateCursor,
  parentOrigin,
  notice,
  orgEnabled,
  executionConfigured = false,
  jobs = [],
  jobStatusUnavailable = false,
  demo = false,
  onEnableRooms,
  onCreateRoom,
  snapshotLoader = loadRoomPreview,
  onSiteCreated,
  renderNewSite,
  renderImageUpload,
  onSelectRoom,
  onRefreshRoom,
  onLoadMoreThreads,
  onLoadMoreCandidates,
  onCreateThread,
  onReply,
  onLoadMoreReplies,
  onLoadMoreRevisions,
  onDecide,
  onApply,
  publishedRevision,
  busyCandidates = [],
  onSubmitCandidate,
  onAddMember,
  onUpdateMember,
  onRemoveMember,
  onAttachSite,
  onDetachSite,
  onResolve,
}: {
  navigation?: RoomLocation;
  onNavigate?: (location: RoomLocation) => void;
  discussionLoading?: boolean;
  discussionError?: string | null;
  activity?: RoomActivity[];
  activityCursor?: string | null;
  onLoadMoreActivity?: (roomId: string, cursor: string) => void;
  onMarkRead?: (roomId: string) => void;
  rooms: ProjectRoom[];
  detailsRoomId?: string | null;
  roomLoadError?: { roomId: string; message: string } | null;
  sites: RoomSiteOption[];
  threads: ReviewThread[];
  candidates: ReviewCandidate[];
  onRequestDiff?: (candidate: ReviewCandidate) => void;
  diffs: Record<string, CandidateDiffView>;
  members: { userId: string; role: string; name?: string }[];
  attachedSites: { siteId: string; createdBy: string }[];
  threadCursor: string | null;
  candidateCursor: string | null;
  parentOrigin: string;
  notice: string | null;
  orgEnabled: boolean;
  demo?: boolean;
  executionConfigured?: boolean;
  jobs?: HostedJob[];
  jobStatusUnavailable?: boolean;
  onEnableRooms: () => void;
  onCreateRoom: (
    name: string,
    siteIds?: string[],
  ) => Promise<string | void> | void;
  snapshotLoader?: SnapshotLoader;
  onSiteCreated?: (site: RoomSiteOption) => void;
  renderNewSite?: (onReady: (site: RoomSiteOption) => void) => React.ReactNode;
  renderImageUpload?: (
    onReady: (image: RoomSiteOption) => void,
  ) => React.ReactNode;
  onSelectRoom: (roomId: string) => void;
  onRefreshRoom?: (roomId: string) => void;
  onLoadMoreThreads: (roomId: string, cursor: string) => void;
  onLoadMoreCandidates: (roomId: string, cursor: string) => void;
  onCreateThread: (input: {
    roomId: string;
    siteId: string;
    versionId: string;
    page: string;
    kind: "page" | "point" | "text" | "component";
    anchor: unknown;
    body: string;
    execution?: MentionExecution;
  }) => Promise<boolean> | void;
  onLoadMoreRevisions?: (
    roomId: string,
    threadId: string,
    cursor: string,
  ) => void;
  onLoadMoreReplies?: (
    roomId: string,
    threadId: string,
    cursor: string,
  ) => void;
  onReply: (
    roomId: string,
    threadId: string,
    body: string,
    execution?: MentionExecution,
  ) => Promise<boolean> | void;
  onDecide: (
    roomId: string,
    candidate: ReviewCandidate,
    decision: "approve" | "reject",
  ) => void;
  onApply?: (roomId: string, candidate: ReviewCandidate) => void;
  publishedRevision?: { siteId: string; versionId: string } | null;
  busyCandidates?: string[];
  onSubmitCandidate: (input: {
    roomId: string;
    siteId: string;
    baseVersionId: string;
    contentHash: string;
    diffHash: string;
    summary: string;
  }) => void;
  onAddMember: (
    roomId: string,
    userId: string,
    role: "editor" | "reviewer" | "viewer",
  ) => void;
  onUpdateMember?: (
    roomId: string,
    userId: string,
    role: "editor" | "reviewer" | "viewer",
  ) => void;
  onRemoveMember?: (roomId: string, userId: string) => void;
  onAttachSite: (roomId: string, siteId: string) => void;
  onDetachSite: (roomId: string, siteId: string) => void;
  onResolve: (roomId: string, threadId: string) => void;
  onReanchor: (
    roomId: string,
    threadId: string,
    reason: string,
    selection?: StaticSelection,
  ) => void;
}) {
  const [roomId, setRoomId] = React.useState(navigation?.roomId ?? rooms[0]?.ID ?? "");
  const [workspace, setWorkspace] = React.useState(navigation?.workspace ?? true);
  const [name, setName] = React.useState("");
  const [creating, setCreating] = React.useState(false);
  const [selectedSiteIds, setSelectedSiteIds] = React.useState<string[]>([]);
  const [showCreate, setShowCreate] = React.useState(navigation ? Boolean(navigation.create) : rooms.length === 0);
  const [onboarding, setOnboarding] = React.useState(false);
  const [settingsSection, setSettingsSection] = React.useState<
    "general" | "people" | "ai"
  >(demo ? "ai" : "general");
  const [panel, setPanel] = React.useState<
    "review" | "changes" | "activity" | "settings"
  >(demo ? "settings" : "review");
  const [toast, setToast] = React.useState(notice);
  React.useEffect(() => {
    setToast(notice);
    if (!notice) return;
    const timer = window.setTimeout(() => setToast(null), 4000);
    return () => window.clearTimeout(timer);
  }, [notice]);
  const sidebarComposer = React.useRef<HTMLDivElement>(null);
  const [commentsVisible, setCommentsVisible] = React.useState(true);
  React.useEffect(() => {
    if (window.matchMedia("(max-width: 1023px)").matches && !window.location.hash.startsWith("#thread-")) setCommentsVisible(false);
  }, []);
  const [activeThreadId, setActiveThreadId] = React.useState("");
  const [commentBusy, setCommentBusy] = React.useState(false);
  const [selectionResetKey, setSelectionResetKey] = React.useState(0);
  React.useEffect(() => {
    if (activeThreadId && commentsVisible)
      document
        .getElementById(`thread-${activeThreadId}`)
        ?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [activeThreadId, commentsVisible]);
  const [commentStatus, setCommentStatus] = React.useState<"open" | "resolved">("open");
  const [commentFilter, setCommentFilter] = React.useState("all");
  const [attachId, setAttachId] = React.useState("");
  const requestedSite = React.useRef<{ roomId: string; siteId: string } | null>(
    null,
  );
  const [siteId, setSiteId] = React.useState(navigation?.siteId ?? attachedSites[0]?.siteId ?? "");
  const [versionId, setVersionId] = React.useState(
    sites.find((s) => s.id === (navigation?.siteId || attachedSites[0]?.siteId))?.currentVersionId ??
      "",
  );
  const [revisionNumbers, setRevisionNumbers] = React.useState<
    Record<string, number>
  >({});
  const revisionCache = React.useRef(new Map<string, Record<string, number>>());
  const currentSiteVersion = sites.find((item) => item.id === siteId)?.currentVersionId;
  React.useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const key = JSON.stringify([siteId, currentSiteVersion, publishedRevision?.versionId]);
    setRevisionNumbers(revisionCache.current.get(key) ?? {});
    if (demo) {
      setRevisionNumbers({ "version-1": 1, "version-2": 2, "version-3": 3 });
      return;
    }
    if (workspace && siteId && !revisionCache.current.has(key))
      void loadRoomRevisions(siteId, controller.signal)
        .then((result) => {
          if (active && result.ok) {
            const numbers = Object.fromEntries(result.versions.map((version) => [version.id, version.number]));
            revisionCache.current.set(key, numbers);
            if (revisionCache.current.size > 16) revisionCache.current.delete(revisionCache.current.keys().next().value!);
            setRevisionNumbers(numbers);
          }
        })
        .catch(() => {});
    return () => {
      active = false;
      controller.abort();
    };
  }, [siteId, currentSiteVersion, demo, publishedRevision, workspace]);
  const revisionLabel = (id: string) =>
    revisionNumbers[id]
      ? `Revision ${revisionNumbers[id]}`
      : id === sites.find((item) => item.id === siteId)?.currentVersionId
        ? "Current revision"
        : "Earlier revision";
  React.useEffect(() => {
    if (!publishedRevision) return;
    setSiteId(publishedRevision.siteId);
    setVersionId(publishedRevision.versionId);
    setLinkedThreadId("");
    setPanel("review");
  }, [publishedRevision]);
  const [page, setPage] = React.useState("/");
  const draftKey = JSON.stringify([roomId, siteId, versionId, page]);
  const [drafts, setDrafts] = React.useState<Record<string, string>>({});
  const body = drafts[draftKey] ?? "";
  const setBody = (value: string) => setDrafts((previous) => {
    const next = { ...previous };
    if (value) next[draftKey] = value;
    else delete next[draftKey];
    return next;
  });
  const latestDraft = React.useRef({ draftKey, body });
  latestDraft.current = { draftKey, body };
  const [snapshot, setSnapshot] = React.useState<StaticSnapshot | null>(null);
  const [previewAttempt, setPreviewAttempt] = React.useState(0);
  const [snapshotNotice, setSnapshotNotice] = React.useState("");
  const [snapshotLoading, setSnapshotLoading] = React.useState(false);
  const [staticSelection, setStaticSelection] =
    React.useState<StaticSelection | null>(null);
  const snapshotRequest = React.useRef(0);
  const [replyByThread, setReplyByThread] = React.useState<
    Record<string, string>
  >({});
  const replyRequests = React.useRef(new Set<string>());
  const [pendingReplies, setPendingReplies] = React.useState<Set<string>>(new Set());
  const [memberId, setMemberId] = React.useState("");
  const [memberRole, setMemberRole] = React.useState<
    "editor" | "reviewer" | "viewer"
  >("reviewer");
  const [linkedThreadId, setLinkedThreadId] = React.useState("");
  React.useEffect(() => {
    const readLink = () =>
      setLinkedThreadId(
        window.location.hash.startsWith("#thread-")
          ? window.location.hash.slice(8)
          : "",
      );
    readLink();
    window.addEventListener("hashchange", readLink);
    return () => window.removeEventListener("hashchange", readLink);
  }, []);
  const linkedThread =
    threads.find(
      (item) => item.ID === linkedThreadId && item.RoomID === roomId,
    ) ?? null;
  const linkedSiteId = linkedThread?.SiteID;
  // A permalink reveals a resolved discussion, but resolving an already open
  // thread must not switch tabs and keep its card on screen.
  const revealedThread = React.useRef("");
  React.useEffect(() => {
    if (!linkedThread) {
      revealedThread.current = "";
      return;
    }
    if (revealedThread.current === linkedThread.ID) return;
    revealedThread.current = linkedThread.ID;
    setCommentsVisible(true);
    setActiveThreadId(linkedThread.ID);
    setCommentStatus(linkedThread.Status === "resolved" ? "resolved" : "open");
  }, [linkedThread]);
  React.useEffect(() => {
    if (activeThreadId && !threads.some((thread) =>
      thread.ID === activeThreadId && (thread.Status === "resolved") === (commentStatus === "resolved"),
    )) {
      setActiveThreadId("");
    }
  }, [threads, activeThreadId, commentStatus]);
  const linkedVersionId = linkedThread?.VersionID;
  const linkedPage = linkedThread?.Page;
  const linkedContentHash = linkedThread?.ContentHash;
  React.useEffect(() => {
    if (!linkedSiteId || !linkedVersionId || !linkedPage) return;
    setSiteId(linkedSiteId);
    setVersionId(linkedVersionId);
    setPage(linkedPage);
    setPanel("review");
    setCommentFilter("all");
  }, [linkedSiteId, linkedVersionId, linkedPage]);
  const previewCache = React.useMemo(() => createPreviewCache(snapshotLoader), [snapshotLoader, roomId]);
  const annotations = React.useMemo(() => {
    if (!snapshot) return [];
    return threads
      .filter((thread) =>
        thread.RoomID === roomId &&
        (thread.Status === "resolved") === (commentStatus === "resolved") &&
        thread.SiteID === snapshot.siteId &&
        thread.VersionID === snapshot.versionId &&
        thread.ContentHash === snapshot.contentHash &&
        thread.Page === snapshot.page)
      .map((thread) => ({
        id: thread.ID,
        kind: thread.AnchorKind,
        anchor: thread.Anchor,
        label: thread.Replies[0]?.Body ?? "Comment",
      }));
  }, [threads, roomId, commentStatus, snapshot]);
  const siteAttached = attachedSites.some((item) => item.siteId === siteId);
  React.useEffect(() => {
    const request = ++snapshotRequest.current;
    let active = true;
    setSnapshot(null);
    setStaticSelection(null);
    setSnapshotNotice("");
    if (
      !workspace ||
      detailsRoomId !== roomId ||
      !roomId ||
      !siteId ||
      !versionId ||
      !siteAttached
    ) {
      setSnapshotLoading(false);
      return;
    }
    const cached = previewCache.peek(roomId, siteId, versionId, page);
    if (cached && (!linkedContentHash || cached.contentHash === linkedContentHash)) {
      setSnapshot(cached);
      setSnapshotLoading(false);
      return;
    }
    setSnapshotLoading(true);
    void previewCache.load(roomId, siteId, versionId, page)
      .then((result) => {
        if (!active || request !== snapshotRequest.current) return;
        setSnapshotLoading(false);
        if (!result.ok) {
          setSnapshotNotice(result.message);
          return;
        }
        if (
          linkedContentHash &&
          result.snapshot.contentHash !== linkedContentHash
        ) {
          setSnapshotNotice("The original version could not be verified.");
          return;
        }
        setSnapshot(result.snapshot);
      })
      .catch(() => {
        if (active && request === snapshotRequest.current) {
          setSnapshotLoading(false);
          setSnapshotNotice(
            "The preview request was interrupted. Try loading this revision again.",
          );
        }
      });
    return () => {
      active = false;
    };
  }, [
    roomId,
    siteId,
    versionId,
    page,
    linkedContentHash,
    siteAttached,
    previewCache,
    previewAttempt,
    detailsRoomId,
    workspace,
  ]);
  const room = rooms.find((item) => item.ID === roomId) ?? null;
  React.useEffect(() => {
    onSelectRoom(roomId);
  }, [onSelectRoom, roomId]);
  React.useEffect(() => {
    if (!workspace) return;
    if (
      detailsRoomId === roomId &&
      requestedSite.current?.roomId === roomId &&
      attachedSites.some(
        (item) => item.siteId === requestedSite.current?.siteId,
      )
    ) {
      const next = sites.find(
        (item) => item.id === requestedSite.current?.siteId,
      );
      if (next) {
        requestedSite.current = null;
        setLinkedThreadId("");
        setSiteId(next.id);
        setVersionId(next.currentVersionId ?? "");
        setPage("/");
        setCommentFilter("all");
        setPanel("review");
        return;
      }
    }
    if (
      detailsRoomId !== roomId ||
      linkedThreadId ||
      attachedSites.some((item) => item.siteId === siteId)
    )
      return;
    const next = sites.find((item) => item.id === attachedSites[0]?.siteId);
    setSiteId(next?.id ?? "");
    setVersionId(next?.currentVersionId ?? "");
    setLinkedThreadId("");
    setPage("/");
  }, [attachedSites, siteId, sites, detailsRoomId, roomId, linkedThreadId, workspace]);
  const role = room ? parseRole(room.Role) : null;
  const site = sites.find((item) => item.id === siteId) ?? null;
  const rights: SiteRights = {
    canRead: site !== null,
    canEditContent: site?.canEditContent === true,
  };
  const [commentExecution, setCommentExecution] =
    React.useState<MentionExecution>();
  const [replyExecutions, setReplyExecutions] = React.useState<
    Record<string, MentionExecution | undefined>
  >({});
  const commentOk = role ? canComment(role, rights) : false;
  const decideOk = role ? canDecideChange(role, rights) : false;
  const sourceContentHash =
    versionId === site?.currentVersionId
      ? site.currentContentHash
      : snapshot?.versionId === versionId && snapshot.siteId === siteId
        ? snapshot.contentHash
        : null;
  const visibleThreads = threads.filter(
    (t) =>
      t.RoomID === roomId &&
      t.SiteID === siteId &&
      (t.Status === "resolved") === (commentStatus === "resolved") &&
      (commentFilter === "all" ||
        t.VersionID ===
          (commentFilter === "current" ? versionId : commentFilter)),
  );
  const projectSites = sites.filter((item) =>
    attachedSites.some((attached) => attached.siteId === item.id),
  );
  const availableSites = sites.filter(
    (item) =>
      !attachedSites.some((attached) => attached.siteId === item.id) &&
      item.canEditContent,
  );
  const pendingCount = candidates.filter(
    (item) => item.RoomID === roomId && item.State === "pending",
  ).length;
  const newSiteControl = (onReady: (site: RoomSiteOption) => void) =>
    renderNewSite ? (
      renderNewSite(onReady)
    ) : (
      <NewProjectSite onReady={onReady} />
    );
  const imageUploadControl = (onReady: (image: RoomSiteOption) => void) =>
    renderImageUpload ? (
      renderImageUpload(onReady)
    ) : (
      <UploadProjectImage onReady={onReady} />
    );
  const navigate = (next: RoomLocation) => {
    setRoomId(next.roomId);
    setSiteId(next.siteId);
    setVersionId(sites.find((item) => item.id === next.siteId)?.currentVersionId ?? "");
    setWorkspace(next.workspace);
    setLinkedThreadId("");
    setActiveThreadId("");
    setCommentStatus("open");
    setShowCreate(Boolean(next.create));
    setPage("/");
    onNavigate?.(next);
  };
  React.useEffect(() => {
    if (!navigation) return;
    setRoomId(navigation.roomId);
    setWorkspace(navigation.workspace);
    setShowCreate(Boolean(navigation.create));
    // Thread links choose their own site, version and page once hydrated.
    // The hash becomes available after hydration, separately from query params.
    if (navigation.roomId && window.location.hash.startsWith("#thread-")) {
      setPanel("review");
      return;
    }
    setSiteId(navigation.siteId);
    setVersionId(sites.find((item) => item.id === navigation.siteId)?.currentVersionId ?? "");
    if (navigation.siteId) setPanel("review");
    setActiveThreadId("");
    setPage("/");
    // Only navigation resets a chosen historical revision, not resource refreshes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigation?.roomId, navigation?.siteId, navigation?.workspace, navigation?.create]);
  const openCreate = () => navigate({ roomId, siteId, workspace, create: true });
  const chooseSite = (id: string) => {
    navigate({ roomId, siteId: id, workspace: true });
    requestedSite.current = null;
    setLinkedThreadId("");
    setCommentFilter("all");
    setSiteId(id);
    setVersionId(sites.find((item) => item.id === id)?.currentVersionId ?? "");
    setPage("/");
  };

  const addSitePanel = (
    <>
      {room ? (
        role === "owner" || role === "editor" ? (
          <details className="group/add text-sm">
            <summary className="flex cursor-pointer list-none items-center gap-2 rounded-md py-2 text-xs font-medium text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              Add a site or image
              <ChevronDown
                className="ml-auto h-3.5 w-3.5 transition-transform group-open/add:rotate-180"
                aria-hidden="true"
              />
            </summary>
            <div className="mt-3 space-y-3">
              <Select
                aria-label="Site to add"
                value={attachId}
                onChange={(e) => setAttachId(e.target.value)}
                className="w-full rounded border border-border bg-background p-2 text-xs"
              >
                <option value="">Choose a site or image</option>
                {availableSites.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.label ?? item.slug}
                  </option>
                ))}
              </Select>
              <p className="text-xs leading-relaxed text-muted-foreground">
                AI-proposed changes require review. Users with site write access
                can continue publishing through the CLI or MCP.
              </p>
              <Button
                size="sm"
                variant="outline"
                disabled={!attachId}
                onClick={() => {
                  requestedSite.current = { roomId: room.ID, siteId: attachId };
                  onAttachSite(room.ID, attachId);
                  setAttachId("");
                }}
              >
                Add to project
              </Button>
              {newSiteControl((created) => {
                onSiteCreated?.(created);
                requestedSite.current = { roomId: room.ID, siteId: created.id };
                onAttachSite(room.ID, created.id);
              })}
              {imageUploadControl((image) => {
                onSiteCreated?.(image);
                requestedSite.current = { roomId: room.ID, siteId: image.id };
                onAttachSite(room.ID, image.id);
              })}
            </div>
          </details>
        ) : null
      ) : null}
    </>
  );
  const activityPanel = (
    <section className="space-y-6" aria-label="Room activity">
      <header className="flex items-center justify-between gap-4">
        <div>
          <h2 className="text-xl font-medium">
            Project activity
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Comments, reviews, and updates in one place.
          </p>
        </div>
        <Tooltip label="Mark all activity as read" align="end">
          <Button
            type="button"
            size="icon"
            variant="outline"
            aria-label="Mark room read"
            disabled={!room || !onMarkRead || room.Unread === 0}
            onClick={() => room && onMarkRead?.(room.ID)}
          >
            <CheckCheck className="h-4 w-4" aria-hidden="true" />
          </Button>
        </Tooltip>
      </header>
      {activity.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-xl border border-dashed border-border px-6 py-16 text-center">
          <History
            className="h-6 w-6 text-muted-foreground"
            aria-hidden="true"
          />
          <p className="text-sm font-medium">No activity yet</p>
          <p className="text-xs text-muted-foreground">
            Your first comment or review will appear here.
          </p>
        </div>
      ) : null}
      <ol className="divide-y divide-border/60 rounded-xl border border-border bg-background px-4">
        {activity.map((item) => (
          <li key={item.id} className="flex items-start gap-3 py-4">
            <span className="mt-0.5 rounded-full bg-muted p-2 text-muted-foreground">
              {item.threadId ? (
                <MessageSquare className="h-4 w-4" aria-hidden="true" />
              ) : (
                <GitPullRequest className="h-4 w-4" aria-hidden="true" />
              )}
            </span>
            <div className="min-w-0 flex-1 space-y-1">
              <p className="text-sm font-medium capitalize">
                {item.kind.replaceAll("_", " ")}
              </p>
              <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                <span title={item.actorUserId}>
                  {item.actorUserId.length > 20
                    ? `Member ${item.actorUserId.slice(0, 8)}`
                    : item.actorUserId}
                </span>
                <span>·</span>
                <time
                  dateTime={
                    Number.isNaN(Date.parse(item.createdAt))
                      ? undefined
                      : item.createdAt
                  }
                >
                  {formatActivityTime(item.createdAt)}
                </time>
              </p>
              {item.siteId ? (
                <p className="text-xs text-muted-foreground">
                  {sites.find((site) => site.id === item.siteId)?.label ??
                    sites.find((site) => site.id === item.siteId)?.slug ??
                    "Project asset"}
                  {item.versionId ? ` · ${revisionLabel(item.versionId)}` : ""}
                </p>
              ) : null}
            </div>
            {item.threadId && room ? (
              <Tooltip label="Open comment thread" align="end">
                <a
                  className="rounded-md p-2 text-muted-foreground hover:bg-muted hover:text-foreground"
                  aria-label="Open comment thread"
                  href={`/rooms?room=${encodeURIComponent(room.ID)}#thread-${encodeURIComponent(item.threadId)}`}
                >
                  <ArrowUpRight className="h-4 w-4" aria-hidden="true" />
                </a>
              </Tooltip>
            ) : null}
          </li>
        ))}
      </ol>
      {activityCursor && room ? (
        <Button
          type="button"
          variant="outline"
          onClick={() => onLoadMoreActivity?.(room.ID, activityCursor)}
        >
          Load older activity
        </Button>
      ) : null}
    </section>
  );
  const attachedResourcesPanel = (
    <>
      <div className="border-t border-border pt-6">
        <h3 className="text-sm font-semibold">Sites & images</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Removing an item from this project does not delete it.
        </p>
      </div>
      <ul className="divide-y divide-border/60 text-sm text-foreground">
        {attachedSites.map((item) => (
          <li key={item.siteId} className="flex items-center gap-3 py-3">
            <a
              className="flex min-w-0 flex-1 items-center gap-3 rounded-md p-2 transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              href={`/sites/${item.siteId}`}
            >
              <PanelsTopLeft className="size-4 shrink-0 text-muted-foreground" aria-hidden />
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">
                  {sites.find((site) => site.id === item.siteId)?.label ??
                    sites.find((site) => site.id === item.siteId)?.slug ?? item.siteId}
                </span>
                <span className="mt-1 block text-xs text-muted-foreground">Open site settings</span>
              </span>
              <ArrowUpRight className="size-4 shrink-0 text-muted-foreground" aria-hidden />
            </a>
            <Tooltip label="Remove from project" align="end">
              <Button
                aria-label="Detach site"
                type="button"
                size="sm"
                variant="outline"
                disabled={role !== "owner" || !room}
                onClick={() => room && onDetachSite(room.ID, item.siteId)}
              >
                <Unlink className="h-4 w-4" aria-hidden="true" />
              </Button>
            </Tooltip>
          </li>
        ))}
      </ul>
    </>
  );
  const settingsPanel = (
    <>
      <section className="space-y-6" aria-label="Project settings">
        <div className="flex items-center gap-3 py-3">
          <Users className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          <h2 className="text-sm font-semibold">Project members</h2>
          <span className="ml-auto text-xs tabular-nums text-muted-foreground">
            {members.length}
          </span>
        </div>
        <p className="text-sm text-muted-foreground">
          Manage access for members of your organization.
        </p>
        <ul className="divide-y divide-border/60 text-sm text-foreground">
          {members.map((member) => (
            <li
              key={member.userId}
              className="flex flex-wrap items-center gap-3 py-3"
            >
              <span className="rounded-full bg-muted p-2 text-muted-foreground">
                <UserRound className="h-4 w-4" aria-hidden="true" />
              </span>
              <span
                className="min-w-0 flex-1 truncate text-xs"
                title={member.name || member.userId}
              >
                {member.name || member.userId}
              </span>
              {member.role === "owner" ? (
                <span className="rounded-md bg-muted px-2 py-1 text-xs">
                  Owner
                </span>
              ) : (
                <>
                  <Select
                    aria-label={`Role for ${member.name || member.userId}`}
                    value={member.role}
                    disabled={role !== "owner" || !onUpdateMember}
                    onChange={(event) =>
                      room &&
                      onUpdateMember?.(
                        room.ID,
                        member.userId,
                        event.target.value as "editor" | "reviewer" | "viewer",
                      )
                    }
                    className="h-8 w-auto rounded-md border border-border bg-background px-2 py-1 text-sm"
                  >
                    <option value="editor">editor</option>
                    <option value="reviewer">reviewer</option>
                    <option value="viewer">viewer</option>
                  </Select>
                  <Tooltip label="Remove project member" align="end">
                    <Button
                      aria-label="Remove project member"
                      type="button"
                      size="sm"
                      variant="outline"
                      disabled={role !== "owner" || !onRemoveMember}
                      onClick={() =>
                        room && onRemoveMember?.(room.ID, member.userId)
                      }
                    >
                      <Trash2 className="h-4 w-4" aria-hidden="true" />
                    </Button>
                  </Tooltip>
                </>
              )}
            </li>
          ))}
        </ul>
        <details className="group/member border-t border-border pt-4">
          <summary className="flex cursor-pointer list-none items-center gap-2 rounded-md py-2 text-xs font-medium text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
            <Plus className="h-3.5 w-3.5" aria-hidden="true" /> Add organization
            member{" "}
            <ChevronDown
              className="ml-auto h-3.5 w-3.5 transition-transform group-open/member:rotate-180"
              aria-hidden="true"
            />
          </summary>
          <form
            className="mt-3 flex flex-wrap gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (!room || role !== "owner" || memberId.trim().length === 0)
                return;
              onAddMember(room.ID, memberId.trim(), memberRole);
            }}
          >
            <Input
              placeholder="Member user ID"
              aria-label="Member user id"
              value={memberId}
              onChange={(event) => setMemberId(event.target.value)}
            />
            <Select
              aria-label="Member role"
              value={memberRole}
              onChange={(event) =>
                setMemberRole(
                  event.target.value as "editor" | "reviewer" | "viewer",
                )
              }
              className="h-8 w-auto rounded-md border border-border bg-background px-2 py-1 text-sm"
            >
              <option value="editor">editor</option>
              <option value="reviewer">reviewer</option>
              <option value="viewer">viewer</option>
            </Select>
            <Button
              type="submit"
              disabled={role !== "owner" || memberId.trim().length === 0}
            >
              Add member
            </Button>
          </form>
        </details>
      </section>
    </>
  );
  const mentions = React.useMemo(() => mentionOptions(members), [members]);
  const commentComposer = (
    <>
      <form
        className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2"
        onSubmit={async (event) => {
          event.preventDefault();
          if (
            commentBusy ||
            !room ||
            !commentOk ||
            !body.trim() ||
            !versionId ||
            (hasDropwayMention(body) && !commentExecution)
          )
            return;
          const anchor =
            staticSelection &&
            snapshot?.siteId === siteId &&
            snapshot.versionId === versionId &&
            snapshot.page === page
              ? staticSelection
              : pageCommentAnchor();
          setCommentBusy(true);
          const submitted = latestDraft.current;
          try {
            const saved = await onCreateThread({
              roomId: room.ID,
              siteId: siteId.trim(),
              versionId: versionId.trim(),
              page: page.trim() || "/",
              kind: anchor.kind,
              anchor: anchor.anchor,
              body: body.trim(),
              execution: hasDropwayMention(body) ? commentExecution : undefined,
            });
            if (saved) {
              setDrafts((previous) => {
                if (previous[draftKey] !== submitted.body) return previous;
                const next = { ...previous };
                delete next[draftKey];
                return next;
              });
            }
            if (
              saved &&
              latestDraft.current.draftKey === submitted.draftKey &&
              latestDraft.current.body === submitted.body
            ) {
              setCommentStatus("open");
              if (commentFilter !== "current" && commentFilter !== versionId) setCommentFilter("all");
              if (hasDropwayMention(body)) setCommentsVisible(true);
              setStaticSelection(null);
              setSelectionResetKey((key) => key + 1);
              setCommentExecution(undefined);
            }
          } finally {
            setCommentBusy(false);
          }
        }}
      >
        <MentionTextarea
          options={mentions}
          rows={1}
          placeholder={staticSelection ? "Comment on this selection…" : "Leave feedback on this page…"}
          aria-label="Comment on this page"
          value={body}
          disabled={commentBusy || !commentOk || !versionId}
          onValueChange={setBody}
          className="block min-h-10 w-full resize-none rounded-md border border-border bg-background px-3 py-2 text-sm leading-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
        {hasDropwayMention(body) ? (
          <div className="order-last col-span-2">
            <MentionExecutionControls
              roomId={roomId}
              key={`${room?.ID}:${siteId}:${versionId}:${page}`}
              body={body}
              allowed={
                !!executionConfigured &&
                decideOk &&
                versionId === site?.currentVersionId
              }
              onChange={setCommentExecution}
            />
          </div>
        ) : null}
        <Tooltip
          label="Send comment (⌘/Ctrl + Enter)"
          align="end"
          side="top"
          className="flex w-fit self-end"
        >
          <Button
            type="submit"
            size="icon"
            aria-label={commentBusy ? "Posting comment" : "Send comment"}
            className="ml-auto flex"
            disabled={
              commentBusy ||
              !commentOk ||
              !versionId ||
              body.trim().length === 0 ||
              (hasDropwayMention(body) && !commentExecution)
            }
          >
            {commentBusy ? <LoaderCircle className="size-4 motion-safe:animate-spin" /> : <Send className="size-4" />}
          </Button>
        </Tooltip>
        {commentOk ? null : (
          <p className="col-span-2 text-xs text-muted-foreground">
            This room role cannot comment.
          </p>
        )}
      </form>
    </>
  );
  const memberNames = React.useMemo(() => new Map(members.map((member) => [member.userId, member.name])), [members]);
  const renderThread = (thread: ReviewThread, inline = false) => (
    <li
      id={inline ? undefined : `thread-${thread.ID}`}
      key={thread.ID}
      className={`space-y-2 text-sm ${inline ? "px-1 pb-1" : `rounded-lg border p-2.5 ${activeThreadId === thread.ID ? "border-amber-400 bg-amber-500/5" : "border-border"}`}`}
    >
      <div
        className={`flex items-start gap-2 ${inline ? "justify-end" : "justify-between"}`}
      >
        {!inline ? (
          <button
            type="button"
            className="text-left font-medium text-foreground hover:underline"
            onClick={() => {
              setLinkedThreadId(thread.ID);
              setActiveThreadId(thread.ID);
            }}
          >
            {thread.AnchorKind === "page"
              ? "Page comment"
              : thread.AnchorKind === "point"
                ? "Image pin"
                : thread.AnchorKind === "text" ? "Text selection" : "Component"}{" "}
            <span className="font-normal text-muted-foreground">on {thread.Page === "/" ? "Home page" : thread.Page}</span>
          </button>
        ) : null}
        {thread.Status !== "resolved" ? (
          <Tooltip label="Resolve comment" align="end">
          <Button
            type="button"
            size="icon"
            variant="ghost"
            disabled={!commentOk || !room || thread.Status === "resolved"}
            className="h-7 w-7 shrink-0 rounded-full text-muted-foreground hover:bg-emerald-500/10 hover:text-emerald-500"
            aria-label="Resolve comment"
            onClick={() => room && onResolve(room.ID, thread.ID)}
          >
            <Check className="h-4 w-4" aria-hidden="true" />
          </Button>
          </Tooltip>
        ) : <span className="text-xs text-muted-foreground">Resolved</span>}
      </div>
      <p className="text-xs text-muted-foreground">
        {revisionLabel(thread.VersionID)} · {thread.Status}{" "}
        <Tooltip label="Link to thread" align="end">
          <a
            href={`/rooms?room=${encodeURIComponent(thread.RoomID)}#thread-${encodeURIComponent(thread.ID)}`}
            className="inline-flex align-middle"
            aria-label="Link to thread"
          >
            <Link className="h-3 w-3" aria-hidden="true" />
          </a>
        </Tooltip>
      </p>
      <ul className="space-y-2">
        {threadConversation(thread, jobs).map((entry, index) => (
          <li
            key={entry.kind === "reply" ? `reply-${entry.reply.ID}` : `job-${entry.job.id}`}
            className={entry.kind === "reply" && index > 0 ? "border-l border-border pl-2 text-xs" : undefined}
          >
            {entry.kind === "reply" ? (
              <div className="space-y-1">
                <div className="flex flex-wrap items-baseline justify-between gap-x-2 text-xs">
                  <span className="font-medium">{memberNames.get(entry.reply.AuthorID) || "Project member"}</span>
                  <time dateTime={entry.reply.CreatedAt} className="text-[11px] text-muted-foreground">{formatActivityTime(entry.reply.CreatedAt)}</time>
                </div>
                <ReplyText body={untrustedText(entry.reply.Body)} compact={inline} />
              </div>
            ) : (
              <AICommentStatus
                job={entry.job}
                stale={jobStatusUnavailable}
                onReview={() => {
                  onRefreshRoom?.(thread.RoomID);
                  setPanel("changes");
                }}
                onActivity={() => setPanel("activity")}
              />
            )}
          </li>
        ))}
      </ul>
      <div className="space-y-2 text-xs">
        {thread.NextReplyCursor ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() =>
              onLoadMoreReplies?.(
                thread.RoomID,
                thread.ID,
                thread.NextReplyCursor!,
              )
            }
          >
            Load older replies
          </Button>
        ) : null}
        <form
          className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2"
          onSubmit={async (event) => {
            event.preventDefault();
            const draft = replyByThread[thread.ID] ?? "";
            const text = draft.trim();
            const execution = replyExecutions[thread.ID];
            if (!room || !commentOk || !text || replyRequests.current.has(thread.ID)) return;
            if (hasDropwayMention(text) && !execution) return;
            replyRequests.current.add(thread.ID);
            setPendingReplies(new Set(replyRequests.current));
            try {
              const saved = await onReply(
                room.ID,
                thread.ID,
                text,
                hasDropwayMention(text) ? execution : undefined,
              );
              if (saved) {
                setReplyByThread((previous) => previous[thread.ID] === draft
                  ? { ...previous, [thread.ID]: "" }
                  : previous);
                setReplyExecutions((previous) => previous[thread.ID] === execution
                  ? { ...previous, [thread.ID]: undefined }
                  : previous);
              }
            } finally {
              replyRequests.current.delete(thread.ID);
              setPendingReplies(new Set(replyRequests.current));
            }
          }}
        >
          <MentionTextarea
            options={mentions}
            rows={1}
            disabled={pendingReplies.has(thread.ID) || !commentOk}
            aria-label={`Reply to ${thread.ID}`}
            placeholder="Write a reply…"
            className="min-h-10 min-w-0 w-full resize-none rounded-md border border-border bg-background px-3 py-2 text-sm leading-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            value={replyByThread[thread.ID] ?? ""}
            onValueChange={(value) =>
              setReplyByThread((prev) => ({
                ...prev,
                [thread.ID]: value,
              }))
            }
          />
          {hasDropwayMention(replyByThread[thread.ID] ?? "") ? (
            <div className="order-last col-span-2">
              <MentionExecutionControls
                roomId={roomId}
                body={replyByThread[thread.ID] ?? ""}
                allowed={
                  !!executionConfigured &&
                  decideOk &&
                  thread.SiteID === site?.id &&
                  thread.VersionID === site.currentVersionId &&
                  thread.ContentHash === site.currentContentHash
                }
                onChange={(value) =>
                  setReplyExecutions((prev) => ({
                    ...prev,
                    [thread.ID]: value,
                  }))
                }
              />
            </div>
          ) : null}
          <Tooltip
            label="Send reply (⌘/Ctrl + Enter)"
            align="end"
            side="top"
            className="ml-auto flex w-fit self-end"
          >
            <Button
              type="submit"
              size="icon"
              aria-label="Send reply"
              aria-busy={pendingReplies.has(thread.ID)}
              className="self-end"
              disabled={
                pendingReplies.has(thread.ID) ||
                !commentOk ||
                (replyByThread[thread.ID] ?? "").trim().length === 0 ||
                (hasDropwayMention(replyByThread[thread.ID] ?? "") &&
                  !replyExecutions[thread.ID])
              }
            >
              <Send className="h-4 w-4" aria-hidden="true" />
            </Button>
          </Tooltip>
        </form>
        <details className="group/history border-t border-border/60 pt-2 text-xs">
          <summary className="flex cursor-pointer list-none items-center gap-2 rounded-md px-1 py-1.5 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground [&::-webkit-details-marker]:hidden">
            <History className="h-3.5 w-3.5" aria-hidden="true" />
            <span className="flex-1">Anchor history</span>
            <ChevronDown
              className="h-3.5 w-3.5 transition-transform group-open/history:rotate-180"
              aria-hidden="true"
            />
          </summary>
          <div className="mt-2 space-y-2 rounded-md bg-muted/30 p-2.5">
            <p className="break-all text-xs text-muted-foreground">
              {revisionLabel(thread.VersionID)} ·{" "}
              {thread.AnchorState === "anchored"
                ? "Original location"
                : "Location no longer matches"}
            </p>
            <ol className="space-y-2">
              {(thread.Revisions ?? []).map((revision) => (
                <li
                  key={revision.id}
                  className="rounded-md border border-border p-2"
                >
                  <p>
                    {revision.anchorKind} · {revision.anchorState} · page{" "}
                    {revision.page}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {revision.createdAt}
                  </p>
                  <p className="whitespace-pre-wrap">
                    {untrustedText(revision.reason) || "No reason recorded."}
                  </p>
                </li>
              ))}
            </ol>
            {thread.NextRevisionCursor ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() =>
                  onLoadMoreRevisions?.(
                    thread.RoomID,
                    thread.ID,
                    thread.NextRevisionCursor!,
                  )
                }
              >
                Load older anchor revisions
              </Button>
            ) : null}
          </div>
        </details>
      </div>
    </li>
  );
  const commentsPanel = (
    <>
      <section className="space-y-3" aria-label="Comments">
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs font-medium text-muted-foreground">Show comments from</span>
          <Select
            aria-label="Comment version filter"
            align="end"
            value={commentFilter}
            onChange={(e) => setCommentFilter(e.target.value)}
            className="h-8 w-auto max-w-44 rounded-md border border-border bg-background px-2 py-1.5 text-xs"
          >
            <option value="all">All versions</option>
            <option value="current">Viewing version</option>
            {Array.from(
              new Set(
                threads
                  .filter((t) => t.RoomID === roomId && t.SiteID === siteId)
                  .map((t) => t.VersionID),
              ),
            ).map((id) => (
              <option key={id} value={id}>
                {revisionLabel(id)}
              </option>
            ))}
          </Select>
        </div>
        <div className="flex gap-1 rounded-lg bg-muted/50 p-1" role="group" aria-label="Comment status">
          {(["open", "resolved"] as const).map((status) => (
            <button
              key={status}
              type="button"
              aria-pressed={commentStatus === status}
              onClick={() => { setCommentStatus(status); setActiveThreadId(""); }}
              className={`flex-1 rounded-md px-2 py-2 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${commentStatus === status ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>
              {status === "open" ? "Open comments" : "Resolved comments"}
            </button>
          ))}
        </div>
        {discussionError ? (
          <p role="alert" className="text-sm text-destructive">
            {discussionError}{" "}
            <button type="button" className="underline" onClick={() => onRefreshRoom?.(roomId)}>Retry</button>
          </p>
        ) : null}
        {discussionLoading ? (
          <p role="status" className="p-4 text-sm text-muted-foreground">Loading comments…</p>
        ) : null}
        {!discussionLoading && !discussionError && visibleThreads.length === 0 ? (
          <p className="rounded-lg bg-muted/40 p-4 text-sm text-muted-foreground">
            {commentStatus === "resolved" ? "No resolved comments yet." : "No open comments. Select a point in the preview or leave a page comment."}
          </p>
        ) : null}
        <ul className="space-y-3">
          {visibleThreads.map((thread) => renderThread(thread))}
        </ul>
        {threadCursor && room ? (
          <Button
            type="button"
            variant="outline"
            onClick={() => onLoadMoreThreads(room.ID, threadCursor)}
          >
            Load older threads
          </Button>
        ) : null}
      </section>
    </>
  );
  const changesPanel = (
    <>
      <section className="space-y-3" aria-label="Proposed changes">
        <header className="flex items-center gap-3">
          <h2 className="text-xl font-medium">
            Proposed changes
          </h2>
          <span className="rounded-md bg-muted px-2 py-1 text-xs tabular-nums text-muted-foreground">
            {pendingCount} pending
          </span>
        </header>
        <p className="text-sm text-muted-foreground">
          Compare the current version with the proposal. Accept change publishes it; reject keeps your current version.
        </p>
        {discussionLoading ? <p role="status" className="text-sm text-muted-foreground">Loading proposed changes…</p> : null}
        {discussionError ? <p role="alert" className="text-sm text-destructive">{discussionError}</p> : null}
        {!discussionLoading && !discussionError && candidates.filter((item) => item.RoomID === roomId).length === 0 ? (
          <p className="rounded-lg bg-muted/40 p-5 text-sm text-muted-foreground">
            No proposed changes yet. AI revisions will appear here for your
            approval.
          </p>
        ) : null}
        {candidates
          .filter((candidate) => candidate.RoomID === roomId)
          .map((candidate, index) => (
            <CandidateReview
              initiallyOpen={index === 0}
              onRequestDiff={onRequestDiff}
              key={JSON.stringify([candidate.ID, candidate.BaseContentHash, candidate.ContentHash, candidate.DiffHash])}
              candidate={candidate}
              live={{
                versionId:
                  sites.find((item) => item.id === candidate.SiteID)
                    ?.currentVersionId ?? null,
                contentHash:
                  sites.find((item) => item.id === candidate.SiteID)
                    ?.currentContentHash ?? null,
              }}
              diff={diffs[candidate.ID] ?? { status: "loading" }}
              canDecide={Boolean(
                role &&
                  canDecideChange(role, {
                    canRead: sites.some((item) => item.id === candidate.SiteID),
                    canEditContent:
                      sites.find((item) => item.id === candidate.SiteID)
                        ?.canEditContent === true,
                  }),
              )}
              busy={busyCandidates.includes(candidate.ID)}
              onApply={
                onApply ? () => onApply(candidate.RoomID, candidate) : undefined
              }
              onDecide={(decision) => {
                if (room && room.ID === candidate.RoomID)
                  onDecide(candidate.RoomID, candidate, decision);
              }}
            />
          ))}
        {candidateCursor && room ? (
          <Button
            type="button"
            variant="outline"
            onClick={() => onLoadMoreCandidates(room.ID, candidateCursor)}
          >
            Load older candidates
          </Button>
        ) : null}
        <details className="group/advanced border-t border-border pt-4">
          <summary className="flex cursor-pointer list-none items-center justify-between rounded-md py-2 text-xs text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
            Advanced: register an existing candidate
            <ChevronDown
              className="h-3.5 w-3.5 transition-transform group-open/advanced:rotate-180"
              aria-hidden="true"
            />
          </summary>
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
              Recording a candidate does not edit the site. Approval is a
              separate human decision.
              {site && !site.canEditContent
                ? " You can review this site, but its content-edit permission is restricted. Room membership does not grant editing rights."
                : ""}
            </p>
            <Input
              name="candidate-summary"
              aria-label="Candidate summary"
              placeholder="Summarize the proposed change"
            />
            <Input
              name="content-hash"
              aria-label="Candidate content hash"
              placeholder="Content hash"
            />
            <Input
              name="diff-hash"
              aria-label="Candidate diff hash"
              placeholder="Diff hash"
            />
            <Button type="submit" disabled={!decideOk || !room}>
              Record candidate
            </Button>
          </form>
        </details>
      </section>
    </>
  );
  return (
    <div
      className={
        !showCreate && (room || !workspace)
          ? "min-h-0"
          : "mx-auto max-w-6xl space-y-6 p-6 sm:p-10"
      }
    >
      {showCreate || (!room && workspace) ? (
        <header className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <p className="mb-1 text-xs font-medium text-muted-foreground">
              Project workspace
            </p>
            <h1 className="text-2xl font-medium tracking-tight sm:text-3xl">Rooms</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Bring sites and images together. Review feedback and approve
              changes in one place.
            </p>
          </div>
          <Button
            onClick={() => showCreate ? navigate({ roomId, siteId, workspace }) : openCreate()}
            disabled={!orgEnabled}
          >
            New project
          </Button>
        </header>
      ) : null}
      {toast ? (
        <div
          role="status"
          aria-live="polite"
          className="fixed bottom-5 left-5 z-[60] flex max-w-[min(24rem,calc(100vw-2.5rem))] items-center gap-3 rounded-lg border border-border bg-popover px-4 py-3 text-sm text-popover-foreground shadow-lg"
        >
          <span>{toast}</span>
          <Tooltip label="Dismiss notification" align="start" side="top">
            <button
              type="button"
              aria-label="Dismiss notification"
              onClick={() => setToast(null)}
              className="shrink-0 rounded p-1 text-muted-foreground hover:bg-muted"
            >
              <X className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          </Tooltip>
        </div>
      ) : null}
      {!orgEnabled ? (
        <section className="rounded-xl border border-border p-8">
          <h2 className="text-lg font-semibold">
            Projects are disabled for this organization
          </h2>
          <p className="mb-4 mt-2 max-w-2xl text-sm text-muted-foreground">
            An administrator can re-enable projects. Sites attached to a project
            require human approval for automated changes.
          </p>
          <Button onClick={onEnableRooms}>Re-enable projects</Button>
        </section>
      ) : null}
      {showCreate && orgEnabled ? (
        <section
          className="rounded-xl border border-border bg-card p-5 sm:p-6"
          aria-label="Create project"
        >
          <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
            <div>
              <h2 className="text-lg font-semibold">Create a project</h2>
              <p className="mt-2 max-w-md text-sm leading-relaxed text-muted-foreground">
                Choose a site or images, create a new site, or upload an image.
                You can add more sites and invite reviewers later.
              </p>
              <div className="mt-5">
                {newSiteControl((site) => {
                  onSiteCreated?.(site);
                  setSelectedSiteIds((prev) => [
                    ...new Set([...prev, site.id]),
                  ]);
                })}
                <div className="mt-3">
                  {imageUploadControl((image) => {
                    onSiteCreated?.(image);
                    setSelectedSiteIds((prev) => [
                      ...new Set([...prev, image.id]),
                    ]);
                  })}
                </div>
              </div>
            </div>
            <form
              className="space-y-5"
              onSubmit={async (event) => {
                event.preventDefault();
                if (!name.trim() || creating) return;
                setCreating(true);
                try {
                  const id = await onCreateRoom(name.trim(), selectedSiteIds);
                  if (id) {
                    navigate({ roomId: id, siteId: "", workspace: true });
                    setName("");
                    setSelectedSiteIds([]);
                    setShowCreate(false);
                    setOnboarding(true);
                    setSettingsSection("ai");
                    setPanel("settings");
                  }
                } finally {
                  setCreating(false);
                }
              }}
            >
              <label className="block text-sm font-medium">
                Project name
                <Input
                  className="mt-2"
                  placeholder="e.g. Website launch"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  required
                  maxLength={120}
                />
              </label>
              <fieldset>
                <legend className="mb-2 text-sm font-medium">
                  Choose sites and images{" "}
                  <span className="font-normal text-muted-foreground">
                    · optional
                  </span>
                </legend>
                <div className="max-h-52 overflow-y-auto rounded-lg border border-border">
                  {sites
                    .filter((site) => site.canEditContent)
                    .map((site) => (
                      <label
                        key={site.id}
                        className="flex cursor-pointer items-center gap-3 border-b border-border px-3 py-3 last:border-0 hover:bg-muted/40"
                      >
                        <input
                          type="checkbox"
                          checked={selectedSiteIds.includes(site.id)}
                          onChange={(event) =>
                            setSelectedSiteIds((prev) =>
                              event.target.checked
                                ? [...prev, site.id]
                                : prev.filter((id) => id !== site.id),
                            )
                          }
                          className="h-4 w-4 accent-current"
                        />
                        <span className="min-w-0">
                          <span className="block truncate text-sm font-medium">
                            {site.label ?? site.slug}
                          </span>
                          <span className="block truncate text-xs text-muted-foreground">
                            {site.kind === "image"
                              ? site.currentVersionId
                                ? "Image"
                                : "Image · upload incomplete"
                              : site.currentVersionId
                                ? "Published site"
                                : "No published version"}
                          </span>
                        </span>
                        <a
                          className="ml-auto inline-flex h-8 shrink-0 items-center rounded-md border border-border px-3 text-xs text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                          href={`/sites/${site.id}`}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          Manage
                        </a>
                      </label>
                    ))}
                  {sites.filter((site) => site.canEditContent).length === 0 ? (
                    <p className="p-4 text-sm text-muted-foreground">
                      No editable items yet. Create a site or upload an image.
                    </p>
                  ) : null}
                </div>
              </fieldset>
              {selectedSiteIds.length > 0 ? (
                <p className="text-xs leading-relaxed text-muted-foreground">
                  AI-proposed changes require review before publishing. Existing
                  site permissions still apply. CLI and MCP publishing remain
                  available.
                </p>
              ) : null}
              <div className="flex items-center gap-3">
                <Button type="submit" disabled={creating || !name.trim()}>
                  {creating ? "Creating project…" : "Create project"}
                </Button>
                {rooms.length > 0 || navigation ? (
                  <Button
                    variant="ghost"
                    type="button"
                    onClick={() => navigate({ roomId, siteId, workspace })}
                  >
                    Cancel
                  </Button>
                ) : null}
              </div>
            </form>
          </div>
        </section>
      ) : null}
      {orgEnabled && rooms.length === 0 ? (
        <div className="grid gap-4 border-t border-border pt-6 sm:grid-cols-3">
          {[
            [
              "01",
              "Bring your sites",
              "Group existing sites or upload something new.",
            ],
            [
              "02",
              "Review together",
              "Invite people and keep feedback tied to each version.",
            ],
            [
              "03",
              "Approve changes",
              "Review proposed edits before anything is published.",
            ],
          ].map(([n, title, text]) => (
            <div key={n} className="flex gap-3 p-2">
              <span className="pt-0.5 font-mono text-xs text-muted-foreground">
                {n}
              </span>
              <div>
                <h3 className="text-sm font-medium">{title}</h3>
                <p className="mt-1 text-sm text-muted-foreground">{text}</p>
              </div>
            </div>
          ))}
        </div>
      ) : null}
      {orgEnabled && !workspace && !showCreate ? (
        <RoomDirectory
          rooms={rooms}
          room={room}
          sites={detailsRoomId === roomId ? projectSites : []}
          loading={Boolean(room && detailsRoomId !== roomId && roomLoadError?.roomId !== roomId)}
          error={roomLoadError?.roomId === roomId ? roomLoadError.message : undefined}
          onChooseRoom={(id) => navigate({ roomId: id, siteId: "", workspace: false })}
          onOpenSite={(id) => { chooseSite(id); setPanel("review"); }}
          onCreate={openCreate}
          onSettings={() => { navigate({ roomId, siteId: "", workspace: true }); setPanel("settings"); }}
          onRetry={() => onRefreshRoom?.(roomId)}
          addSite={role === "owner" || role === "editor" ? addSitePanel : null}
        />
      ) : null}
      {orgEnabled && room && workspace && !showCreate ? (
        <div className="flex h-[calc(100dvh-3.5rem)] min-h-[32rem] flex-col bg-background">
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-2.5">
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <Button
                size="sm"
                variant="ghost"
                onClick={() => navigate({ roomId, siteId: "", workspace: false })}
                aria-label="Browse room contents"
              >
                <FolderOpen className="h-4 w-4" aria-hidden="true" />
                <span className="ml-2 hidden sm:inline">Files</span>
              </Button>
              <Select
                aria-label="Project"
                value={roomId}
                variant="ghost"
                className="h-9 w-auto font-semibold"
                onChange={(event) => {
                  navigate({ roomId: event.target.value, siteId: "", workspace: false });
                  setPanel("review");
                  setLinkedThreadId("");
                  setCommentFilter("all");
                }}
              >
                {rooms.map((item) => <option key={item.ID} value={item.ID}>{item.Name}</option>)}
              </Select>
              <span className="text-border">/</span>
              <Select
                aria-label="Site or image"
                value={siteId}
                variant="ghost"
                className="h-9 w-auto"
                onChange={(event) => chooseSite(event.target.value)}
              >
                <option value="" disabled>Select a site or image</option>
                {projectSites.map((item) => <option key={item.id} value={item.id}>{item.label ?? item.slug}</option>)}
              </Select>
              <Tooltip label="Create a new project" align="end">
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={openCreate}
                  aria-label="New project"
                >
                  <Plus className="h-4 w-4" aria-hidden="true" />
                </Button>
              </Tooltip>
            </div>
            <nav
              className="flex items-center gap-1"
              aria-label="Project sections"
            >
              {(["review", "changes", "activity", "settings"] as const).map(
                (value) => (
                  <Tooltip
                    label={
                      {
                        review: "View canvas",
                        changes: "Review proposed changes",
                        activity: "View project activity",
                        settings: "Project settings and sharing",
                      }[value]
                    }
                    align="end"
                    key={value}
                  >
                    <button
                      key={value}
                      type="button"
                      aria-label={
                        {
                          review: "Canvas",
                          changes: `Changes${pendingCount ? ` (${pendingCount})` : ""}`,
                          activity: "Activity",
                          settings: "Settings & sharing",
                        }[value]
                      }
                      aria-current={panel === value ? "page" : undefined}
                      onClick={() => setPanel(value)}
                      className={`inline-flex h-9 min-w-9 items-center justify-center gap-1 rounded-md px-2 py-1.5 text-xs transition-colors ${panel === value ? "bg-secondary font-medium" : "text-muted-foreground hover:text-foreground"}`}
                    >
                      {React.createElement(
                        {
                          review: PanelsTopLeft,
                          changes: GitPullRequest,
                          activity: History,
                          settings: Settings,
                        }[value],
                        { className: "h-4 w-4", "aria-hidden": true },
                      )}
                      <span className="hidden lg:inline">
                        {{ review: "Canvas", changes: "Changes", activity: "Activity", settings: "Settings" }[value]}
                      </span>
                      {value === "changes" && pendingCount > 0 ? (
                        <span className="text-[10px] font-semibold">
                          {pendingCount}
                        </span>
                      ) : null}
                    </button>
                  </Tooltip>
                ),
              )}
            </nav>
          </div>
          {detailsRoomId !== roomId ? (
            <div className="p-8 text-sm text-muted-foreground">
              {roomLoadError?.roomId === roomId ? (
                <>
                  <p role="alert">{roomLoadError.message}</p>
                  <Button
                    variant="outline"
                    className="mt-3"
                    onClick={() => onRefreshRoom?.(room.ID)}
                  >
                    Retry loading project
                  </Button>
                </>
              ) : (
                <p role="status">Loading project…</p>
              )}
            </div>
          ) : panel === "review" ? (
            site ? (
              <>
                <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <Select
                      aria-label="Viewing version"
                      onValuePreview={(id) => {
                        if (detailsRoomId === roomId && siteAttached && id !== versionId)
                          void previewCache.load(roomId, siteId, id, page).catch(() => {});
                      }}
                      value={versionId}
                      onChange={(e) => {
                        setLinkedThreadId("");
                        setVersionId(e.target.value);
                      }}
                      className="h-9 w-auto min-w-48 text-sm"
                    >
                      {Array.from(
                        new Set(
                          [
                            site.currentVersionId,
                            versionId,
                            ...Object.keys(revisionNumbers).sort((a,b)=>revisionNumbers[b]!-revisionNumbers[a]!),
                            ...threads
                              .filter((t) => t.SiteID === siteId)
                              .map((t) => t.VersionID),
                          ].filter(Boolean) as string[],
                        ),
                      ).map((id) => (
                        <option key={id} value={id}>
                          {id === site.currentVersionId
                            ? `${revisionNumbers[id] ? `Revision ${revisionNumbers[id]} · ` : ""}Latest`
                            : revisionLabel(id)}
                        </option>
                      ))}
                    </Select>
                    <PagePathForm key={draftKey} page={page} onGo={(next) => {
                      setLinkedThreadId("");
                      setPage(next);
                    }} />

                  </div>
                  <div className="flex items-center gap-2">
                    {site.liveUrl ? (
                      <Tooltip
                        label={
                          site.kind === "image"
                            ? "Open original"
                            : "Open live site"
                        }
                        align="end"
                      >
                        <a
                          href={site.liveUrl}
                          aria-label={
                            site.kind === "image"
                              ? "Open original"
                              : "Open live site"
                          }
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex size-9 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        >
                          <ExternalLink
                            className="h-4 w-4"
                            aria-hidden="true"
                          />
                        </a>
                      </Tooltip>
                    ) : null}
                    <Tooltip
                      label={
                        commentsVisible ? "Hide comments" : "Show comments"
                      }
                      align="end"
                    >
                      <Button
                        size="sm"
                        variant={commentsVisible ? "secondary" : "outline"}
                        aria-pressed={commentsVisible}
                        aria-label={
                          commentsVisible ? "Hide comments" : "Show comments"
                        }
                        onClick={() =>
                          setCommentsVisible((visible) => !visible)
                        }
                      >
                        <MessageSquare className="h-4 w-4" aria-hidden="true" />
                      </Button>
                    </Tooltip>
                  </div>
                </div>
                <div
                  className={`relative grid min-h-0 flex-1 ${commentsVisible ? "lg:grid-cols-[minmax(0,1fr)_22rem]" : "grid-cols-1"}`}
                >
                  <section
                    className="flex min-h-0 min-w-0 flex-col bg-muted/20"
                    aria-label="Site preview"
                  >
                    {snapshotLoading || (siteAttached && versionId && !snapshot && !snapshotNotice) ? (
                      <div
                        role="status"
                        className="flex flex-1 items-center justify-center text-sm text-muted-foreground"
                      >
                        <LoaderCircle aria-hidden="true" className="mr-2 size-4 motion-safe:animate-spin" />
                        Loading site preview…
                      </div>
                    ) : snapshot ? (
                      <StaticReview
                        snapshot={snapshot}
                        readOnly={!commentOk}
                        activeThreadId={activeThreadId}
                        selectionResetKey={selectionResetKey}
                        annotations={annotations}
                        onThreadSelect={(id) => {
                          setActiveThreadId(id);
                          setCommentFilter("all");
                        }}
                        renderDraft={commentsVisible || !commentOk ? undefined : () => commentComposer}
                        renderThread={(id) => {
                          const thread = threads.find(
                            (item) =>
                              item.ID === id &&
                              item.RoomID === roomId &&
                              item.SiteID === siteId &&
                              (item.Status === "resolved") === (commentStatus === "resolved"),
                          );
                          return thread ? (
                            <ul>{renderThread(thread, true)}</ul>
                          ) : null;
                        }}
                        onSelect={(selection) => {
                          setStaticSelection(selection);
                          if (selection) {
                            if (commentsVisible) sidebarComposer.current?.querySelector("textarea")?.focus();
                            setActiveThreadId("");
                          }
                        }}
                      />
                    ) : (
                      <div className="flex flex-1 flex-col items-center justify-center p-8 text-center">
                        <h3 className="font-medium">
                          {site.currentVersionId
                            ? "Preview unavailable"
                            : "This site is ready for its first upload"}
                        </h3>
                        <p
                          role="status"
                          className="mt-2 max-w-sm text-sm text-muted-foreground"
                        >
                          {snapshotNotice ||
                            "Upload your site files to see them here."}
                        </p>
                        <a
                          className="mt-4 inline-flex h-9 items-center rounded-md border border-border px-3 text-sm font-medium transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                          href={`/sites/${site.id}`}
                        >
                          Open site settings
                        </a>
                        {versionId && <Button variant="outline" className="mt-2" onClick={() => setPreviewAttempt((n) => n + 1)}>Retry preview</Button>}
                      </div>
                    )}
                    <p className="shrink-0 border-t border-border px-4 py-1.5 text-[11px] text-muted-foreground">
                      Safe preview of this version. Scripts and original styling
                      are removed. Comments are pinned to this version.
                    </p>
                  </section>
                  {commentsVisible ? (
                    <aside
                      className="absolute inset-y-0 right-0 z-10 flex w-[min(22rem,100%)] min-h-0 flex-col border-l border-border bg-background shadow-lg lg:static lg:w-auto lg:shadow-none"
                      aria-label="Review panel"
                    >
                      <div className="flex shrink-0 items-center justify-between border-b border-border px-3 py-2">
                        <h2 className="text-sm font-semibold">Discussion</h2>
                        <Tooltip label="Close comments" align="end">
                          <button
                            type="button"
                            onClick={() => setCommentsVisible(false)}
                            aria-label="Close comments"
                            className="rounded px-2 py-1 text-muted-foreground hover:bg-muted"
                          >
                            <X className="h-4 w-4" aria-hidden="true" />
                          </button>
                        </Tooltip>
                      </div>
                      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
                        {commentsPanel}
                      </div>
                      <div ref={sidebarComposer} className="max-h-[45%] shrink-0 overflow-y-auto border-t border-border bg-muted/10 p-4">
                        {staticSelection ? (
                          <div className="mb-3 border-l-2 border-amber-400 pl-3 text-xs text-muted-foreground">
                            <span className="line-clamp-3">
                              {staticSelection.label}
                            </span>
                            <Button
                              type="button"
                              onClick={() => {
                                setStaticSelection(null);
                                setSelectionResetKey((key) => key + 1);
                              }}
                              variant="outline"
                              size="sm"
                              className="mt-2"
                            >
                              Clear selection
                            </Button>
                          </div>
                        ) : null}
                        {commentComposer}
                      </div>
                    </aside>
                  ) : null}
                </div>
              </>
            ) : (
              <section className="flex min-h-0 flex-1 flex-col items-center justify-center overflow-y-auto p-8">
                <h3 className="text-xl font-semibold">
                  Add your first site or image
                </h3>
                <p className="mb-6 mt-2 max-w-md text-center text-sm text-muted-foreground">
                  Choose an existing site, create a new site, or upload an
                  image. Your site becomes the canvas for comments and changes.
                </p>
                <div className="w-full max-w-lg">{addSitePanel}</div>
              </section>
            )
          ) : null}
          {detailsRoomId === roomId && panel === "changes" ? (
            <div className="min-h-0 flex-1 overflow-y-auto">
              <div className="mx-auto w-full max-w-[1600px] p-4 lg:p-6">
                <div className="min-w-0">{changesPanel}</div>
              </div>
            </div>
          ) : null}
          {detailsRoomId === roomId && panel === "activity" ? (
            <div className="min-h-0 flex-1 overflow-y-auto">
              <div className="mx-auto max-w-4xl p-6 lg:p-8">
                {activityPanel}
                {executionConfigured && room ? (
                  <div className="mt-8 border-t border-border pt-6">
                    <HostedExecution
                      key={room.ID}
                      roomId={room.ID}
                      siteId={siteId}
                      versionId={versionId}
                      canRun={decideOk && versionId === site?.currentVersionId}
                      threads={[]}
                      tasksOnly
                      onManageConnections={() => {
                        setSettingsSection("ai");
                        setPanel("settings");
                      }}
                      onCandidateReady={() => onRefreshRoom?.(room.ID)}
                    />
                  </div>
                ) : null}
              </div>
            </div>
          ) : null}
          {detailsRoomId === roomId && panel === "settings" ? (
            <div className="min-h-0 flex-1 overflow-y-auto">
              <div className="mx-auto max-w-6xl space-y-7 p-6 lg:p-8">
                <header>
                  <h2 className="text-xl font-medium">
                    Settings & sharing
                  </h2>
                  <p className="mt-1 text-sm text-muted-foreground">
                    Manage project resources, sharing, and reusable AI
                    connections.
                  </p>
                </header>
                <div className="grid items-start gap-8 md:grid-cols-[200px_minmax(0,1fr)]">
                  <nav
                    aria-label="Project settings sections"
                    className="flex gap-1 md:flex-col"
                  >
                    {(["general", "people", "ai"] as const).map((section) => (
                      <button
                        key={section}
                        type="button"
                        aria-current={
                          settingsSection === section ? "page" : undefined
                        }
                        onClick={() => setSettingsSection(section)}
                        className={`rounded-lg px-3 py-2.5 text-left text-sm ${settingsSection === section ? "bg-secondary font-medium" : "text-muted-foreground hover:bg-secondary/50"}`}
                      >
                        {
                          {
                            general: "Sites & images",
                            people: "People & sharing",
                            ai: "AI connections",
                          }[section]
                        }
                      </button>
                    ))}
                  </nav>
                  <div className="min-w-0 max-w-3xl space-y-5">
                    <h3 className="text-lg font-semibold">
                      {
                        {
                          general: "Sites & images",
                          people: "People & sharing",
                          ai: "AI connections",
                        }[settingsSection]
                      }
                    </h3>
                    {settingsSection === "general" ? (
                      <>
                        {attachedResourcesPanel}
                        {addSitePanel}
                      </>
                    ) : null}
                    {settingsSection === "people" ? (
                      <>
                        {settingsPanel}
                        {role === "owner" ? (
                          <ProjectSharing key={room.ID} roomId={room.ID} />
                        ) : null}
                      </>
                    ) : null}
                    {settingsSection === "ai" ? (
                      <AIConnections
                        key={room.ID}
                        roomId={room.ID}
                        canManageProject={role === "owner"}
                        onboarding={onboarding}
                        onDone={() => {
                          setOnboarding(false);
                          setPanel("review");
                        }}
                        demo={demo}
                      />
                    ) : null}
                  </div>
                </div>
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function PagePathForm({ page, onGo }: { page: string; onGo: (path: string) => void }) {
  const [draft, setDraft] = React.useState(page === "/" ? "" : page);
  const path = draft.trim() || "/";
  const next = path.startsWith("/") ? path : "/" + path;
  return (
    <form className="flex items-center gap-2" onSubmit={(event) => {
      event.preventDefault();
      setDraft(next === "/" ? "" : next);
      onGo(next);
    }}>
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        Page
        <Input aria-label="Page path" placeholder="Home page" value={draft} className="h-9 w-36 text-sm"
          onFocus={(event) => event.target.select()} onChange={(event) => setDraft(event.target.value)} />
      </label>
      <Button type="submit" variant="outline" size="sm" disabled={next === page}>Go</Button>
    </form>
  );
}

function ReplyText({
  body,
  compact = false,
}: {
  body: string;
  compact?: boolean;
}) {
  const [open, setOpen] = React.useState(false);
  const limit = 160;
  const long = body.length > limit;
  const shown = long && !open ? `${body.slice(0, limit)}…` : body;
  return (
    <div className="space-y-1">
      <p
        className={`whitespace-pre-wrap text-foreground ${compact ? "text-xs leading-5" : "text-sm"}`}
      >
        {shown}
      </p>
      {long ? (
        <button
          type="button"
          className="text-xs text-primary"
          onClick={() => setOpen((value) => !value)}
        >
          {open ? "Collapse reply" : "Expand reply"}
        </button>
      ) : null}
    </div>
  );
}

const activityTime = new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    timeZone: "UTC",
    timeZoneName: "short",
});

function formatActivityTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : activityTime.format(date);
}
