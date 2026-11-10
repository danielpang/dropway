// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Client session for rooms the API already returned. Threads and candidates
// accumulate only from those responses. Applying is an explicit human action.

"use client";

import * as React from "react";

import { RoomWorkspace, type RoomSiteOption } from "@/components/project-room/room-workspace";
import {
  addMemberAction,
  loadRoomActivityAction,
  markRoomReadAction,
  loadThreadLinkAction,
  loadThreadRevisionsAction,
  applyCandidateAction,
  loadRoomSitesAction,
  updateMemberAction,
  removeMemberAction,
  attachSiteAction,
  createRoomAction,
  enableRoomsAction,
  createThreadAction,
  decideAction,
  detachSiteAction,
  loadCandidateDiffAction,
  loadMoreCandidatesAction,
  loadMoreThreadsAction,
  loadRoomAction,
  loadSiteCommentsAction,
  reanchorAction,
  replyAction,
  resolveAction,
  signoffAction,
  submitCandidateAction,
} from "@/app/(app)/rooms/actions";
import { appendThreadRevisions } from "@/lib/project-room/contract";
import type { RoomActivity, CandidateDiffView, ProjectRoom, ReviewCandidate, ReviewThread, RoomMemberRow, RoomSiteRef, SiteCommentItem } from "@/lib/project-room/contract";

export function RoomSession({
  orgEnabled: initialOrgEnabled,
  rooms,
  sites,
  parentOrigin,
  loadError,
}: {
  orgEnabled: boolean;
  rooms: ProjectRoom[];
  sites: RoomSiteOption[];
  parentOrigin: string;
  loadError?: string;
}) {
  const [activity, setActivity] = React.useState<RoomActivity[]>([]);
  const [activityCursor, setActivityCursor] = React.useState<string | null>(null);
  const [roomList, setRoomList] = React.useState(rooms);
  const [siteList, setSiteList] = React.useState(sites);
  const candidateRequests = React.useRef(new Set<string>());
  const [busyCandidates, setBusyCandidates] = React.useState<string[]>([]);
  const roomRequest = React.useRef(0);
  const commentRequest = React.useRef(0);
  const selectedSite = React.useRef("");
  const startCandidateRequest = (candidate: ReviewCandidate) => {
    if (candidate.RoomID !== loadedRoom.current || candidateRequests.current.has(candidate.ID)) return false;
    candidateRequests.current.add(candidate.ID);
    setBusyCandidates([...candidateRequests.current]);
    return true;
  };
  const finishCandidateRequest = (candidate: ReviewCandidate) => {
    candidateRequests.current.delete(candidate.ID);
    setBusyCandidates([...candidateRequests.current]);
  };
  const [threads, setThreads] = React.useState<ReviewThread[]>([]);
  const [candidates, setCandidates] = React.useState<ReviewCandidate[]>([]);
  const [diffs, setDiffs] = React.useState<Record<string, CandidateDiffView>>({});
  const requestedDiffs = React.useRef(new Set<string>());
  const [members, setMembers] = React.useState<RoomMemberRow[]>([]);
  const [attachedSites, setAttachedSites] = React.useState<RoomSiteRef[]>([]);
  const [siteComments, setSiteComments] = React.useState<SiteCommentItem[]>([]);
  const [commentCursor, setCommentCursor] = React.useState<string | null>(null);
  const [threadCursor, setThreadCursor] = React.useState<string | null>(null);
  const [candidateCursor, setCandidateCursor] = React.useState<string | null>(null);
  const loadedRoom = React.useRef("");

  const applyRoom = React.useCallback((roomId: string) => {
    if (!roomId || loadedRoom.current === roomId) return;
    loadedRoom.current = roomId;
    const request = ++roomRequest.current;
    setActivity([]);
    setActivityCursor(null);
    setMembers([]);
    setAttachedSites([]);
    setThreads([]);
    setCandidates([]);
    setThreadCursor(null);
    setCandidateCursor(null);
    void loadRoomAction(roomId).then((result) => {
      if (request !== roomRequest.current || roomId !== loadedRoom.current) return;
      if (!result.ok) {
        setNotice(result.message);
        return;
      }
      setRoomList((prev) => prev.map((item) => item.ID === roomId ? result.detail.room : item));
      setMembers(result.detail.members);
      setAttachedSites(result.detail.sites);
      setThreads(result.threads);
      setThreadCursor(result.threadCursor);
      setCandidates(result.candidates);
      setCandidateCursor(result.candidateCursor);
      const linkedId = window.location.hash.startsWith("#thread-") ? window.location.hash.slice(8) : "";
      if (linkedId) {
        void loadThreadLinkAction(roomId, linkedId).then((linked) => {
          if (request !== roomRequest.current || loadedRoom.current !== roomId) return;
          if (!linked.ok) { setNotice(linked.message); return; }
          setThreads((prev) => [linked.thread, ...prev.filter((item) => item.ID !== linked.thread.ID)]);
          requestAnimationFrame(() => document.getElementById(`thread-${linked.thread.ID}`)?.scrollIntoView());
        });
      }
      void loadRoomActivityAction(roomId).then((activityResult) => {
        if (request !== roomRequest.current || loadedRoom.current !== roomId) return;
        if (!activityResult.ok) { setNotice(activityResult.message); return; }
        setActivity(activityResult.items);
        setActivityCursor(activityResult.nextCursor);
        setRoomList((prev) => prev.map((item) => item.ID === roomId ? { ...item, Unread: activityResult.unread } : item));
      });
    });
  }, []);

  const loadComments = React.useCallback((siteId: string, cursor?: string) => {
    if (!siteId) return;
    if (cursor && siteId !== selectedSite.current) return;
    selectedSite.current = siteId;
    const request = ++commentRequest.current;
    if (!cursor) { setSiteComments([]); setCommentCursor(null); }
    void loadSiteCommentsAction(siteId, cursor).then((result) => {
      if (request !== commentRequest.current || selectedSite.current !== siteId) return;
      if (!result.ok) {
        setNotice(result.message);
        return;
      }
      setSiteComments((prev) => {
        const incoming = cursor ? [...prev, ...result.items] : result.items;
        const seen = new Set<string>();
        return incoming.filter((item) => {
          if (seen.has(item.id)) return false;
          seen.add(item.id);
          return true;
        });
      });
      setCommentCursor(result.nextCursor);
    });
  }, []);
  const [orgEnabled, setOrgEnabled] = React.useState(initialOrgEnabled);
  const [notice, setNotice] = React.useState<string | null>(
    loadError ?? (initialOrgEnabled ? null : "The org has not enabled project rooms."),
  );

  React.useEffect(() => {
    for (const candidate of candidates) {
      if (requestedDiffs.current.has(candidate.ID)) continue;
      requestedDiffs.current.add(candidate.ID);
      setDiffs((prev) => ({ ...prev, [candidate.ID]: { status: "loading" } }));
      void loadCandidateDiffAction(candidate.RoomID, candidate.ID).then((result) => {
        const next: CandidateDiffView = result.ok
          ? { status: "verified", diff: result.diff }
          : "unverified" in result && result.unverified
            ? { status: "unverified" }
            : { status: "unavailable" };
        setDiffs((prev) => ({ ...prev, [candidate.ID]: next }));
      });
    }
  }, [candidates]);

  return (
    <RoomWorkspace
      activity={activity}
      activityCursor={activityCursor}
      onLoadMoreActivity={(roomId, cursor) => {
        void loadRoomActivityAction(roomId, cursor).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) { setNotice(result.message); return; }
          setActivity((prev) => {
            const seen = new Set(prev.map((item) => item.id));
            return [...prev, ...result.items.filter((item) => !seen.has(item.id))];
          });
          setActivityCursor(result.nextCursor);
        });
      }}
      onMarkRead={(roomId) => {
        void markRoomReadAction(roomId).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) { setNotice(result.message); return; }
          setRoomList((prev) => prev.map((item) => item.ID === roomId ? { ...item, Unread: 0 } : item));
          setNotice("Room marked read.");
        });
      }}
      rooms={roomList}
      sites={siteList}
      busyCandidates={busyCandidates}
      threads={threads}
      candidates={candidates}
      diffs={diffs}
      members={members}
      attachedSites={attachedSites}
      siteComments={siteComments}
      commentCursor={commentCursor}
      threadCursor={threadCursor}
      candidateCursor={candidateCursor}
      parentOrigin={parentOrigin}
      notice={notice}
      orgEnabled={orgEnabled}
      onEnableRooms={() => {
        void enableRoomsAction(true).then((result) => {
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setOrgEnabled(true);
          setNotice("Project rooms are enabled for this organization. Sites are still unchanged.");
        });
      }}
      onSelectRoom={applyRoom}
      onRefreshRoom={(roomId) => {
        void loadRoomSitesAction().then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) { setNotice(result.message); return; }
          setSiteList(result.sites);
          requestedDiffs.current.clear();
          setDiffs({});
          loadedRoom.current = "";
          applyRoom(roomId);
        });
      }}
      onSelectSite={loadComments}
      onLoadMoreComments={loadComments}
      onLoadMoreThreads={(roomId, cursor) => {
        void loadMoreThreadsAction(roomId, cursor).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) => {
            const seen = new Set(prev.map((item) => item.ID));
            return [...prev, ...result.threads.filter((item) => !seen.has(item.ID))];
          });
          setThreadCursor(result.nextCursor);
        });
      }}
      onLoadMoreCandidates={(roomId, cursor) => {
        void loadMoreCandidatesAction(roomId, cursor).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setCandidates((prev) => {
            const seen = new Set(prev.map((item) => item.ID));
            return [...prev, ...result.candidates.filter((item) => !seen.has(item.ID))];
          });
          setCandidateCursor(result.nextCursor);
        });
      }}
      onCreateRoom={(name) => {
        void createRoomAction(name).then((result) => {
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setRoomList((prev) => [...prev, result.room]);
          setNotice(`Created ${result.room.Name}.`);
        });
      }}
      onCreateThread={(input) => {
        void createThreadAction(input).then((result) => {
          if (loadedRoom.current !== input.roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) => {
            const rest = prev.filter((item) => item.ID !== result.thread.ID);
            return [result.thread, ...rest];
          });
          setNotice(
            result.thread.AnchorKind === "page"
              ? "Page comment stored on the original version."
              : "Image pin stored on the original version.",
          );
          loadComments(input.siteId);
        });
      }}
      onLoadMoreRevisions={(roomId, threadId, cursor) => {
        void loadThreadRevisionsAction(roomId, threadId, cursor).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) { setNotice(result.message); return; }
          setThreads((prev) => prev.map((thread) => thread.ID === threadId ? appendThreadRevisions(thread, result.thread) : thread));
        });
      }}
      onLoadMoreReplies={(roomId, threadId, cursor) => {
        void loadThreadLinkAction(roomId, threadId, cursor).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) { setNotice(result.message); return; }
          setThreads((prev) => prev.map((thread) => {
            if (thread.ID !== threadId) return thread;
            const seen = new Set(thread.Replies.map((reply) => reply.ID));
            return { ...thread, NextReplyCursor: result.thread.NextReplyCursor,
              Replies: [...thread.Replies, ...result.thread.Replies.filter((reply) => !seen.has(reply.ID))] };
          }));
        });
      }}
      onReply={(roomId, threadId, body) => {
        void replyAction(roomId, threadId, body).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) =>
            prev.map((thread) =>
              thread.ID === threadId
                ? { ...thread, Replies: [...thread.Replies, result.reply] }
                : thread,
            ),
          );
          setNotice("Reply stored.");
        });
      }}
      onSignoff={(roomId, siteId, versionId) => {
        void signoffAction(roomId, siteId, versionId).then((result) => {
          setNotice(result.ok ? "Version sign-off recorded. The site was not published." : result.message);
        });
      }}
      onAddMember={(roomId, userId, role) => {
        void addMemberAction(roomId, userId, role).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          loadedRoom.current = "";
          applyRoom(roomId);
          setNotice("Member added.");
        });
      }}
      onUpdateMember={(roomId, userId, role) => {
        void updateMemberAction(roomId, userId, role).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) { setNotice(result.message); return; }
          loadedRoom.current = "";
          applyRoom(roomId);
          setNotice("Member role updated.");
        });
      }}
      onRemoveMember={(roomId, userId) => {
        void removeMemberAction(roomId, userId).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) { setNotice(result.message); return; }
          loadedRoom.current = "";
          applyRoom(roomId);
          setNotice("Member removed.");
        });
      }}
      onAttachSite={(roomId, siteId) => {
        void attachSiteAction(roomId, siteId).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          loadedRoom.current = "";
          applyRoom(roomId);
          setNotice("Site attached. The site files were not changed.");
        });
      }}
      onDetachSite={(roomId, siteId) => {
        void detachSiteAction(roomId, siteId).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          loadedRoom.current = "";
          applyRoom(roomId);
          setNotice("Site detached. Candidates were not deleted.");
        });
      }}
      onResolve={(roomId, threadId) => {
        void resolveAction(roomId, threadId).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) => prev.map((item) => item.ID === result.thread.ID ? { ...item, Status: result.thread.Status, AnchorState: result.thread.AnchorState } : item));
          setNotice("Thread resolved. The anchor history was not rewritten.");
        });
      }}
      onReanchor={(roomId, threadId, reason, selection) => {
        void reanchorAction(roomId, threadId, reason, selection).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) => prev.map((item) => item.ID === threadId ? { ...item, AnchorState: result.thread.AnchorState } : item));
          setNotice("Anchor revision appended. The original pin is unchanged.");
          void loadThreadLinkAction(roomId, threadId).then((refreshed) => {
            if (loadedRoom.current !== roomId || !refreshed.ok) return;
            setThreads((prev) => prev.map((thread) => thread.ID === threadId ? {
              ...thread, Revisions: refreshed.thread.Revisions, NextRevisionCursor: refreshed.thread.NextRevisionCursor,
            } : thread));
          });
        });
      }}
      onSubmitCandidate={(input) => {
        void submitCandidateAction(input).then((result) => {
          if (loadedRoom.current !== input.roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setCandidates((prev) => [result.candidate, ...prev.filter((item) => item.ID !== result.candidate.ID)]);
          setNotice("Candidate recorded. The site was not edited.");
        });
      }}
      onApply={(roomId, candidate) => {
        if (!startCandidateRequest(candidate)) return;
        void (async () => {
          try {
            const result = await applyCandidateAction(roomId, candidate).catch(() => ({ ok: false as const, message: "The apply response was interrupted. Refreshing the room to check whether it was published." }));
            if (loadedRoom.current !== roomId) return;
            setNotice(result.ok ? (result.changed ? "Approved change published." : "This change was already applied. No new publication was made.") : result.message);
            // Reload after ambiguous failures too: the server may have committed before a response was lost.
            const refreshed = await loadRoomSitesAction();
            if (loadedRoom.current !== roomId) return;
            if (refreshed.ok) setSiteList(refreshed.sites);
            else setSiteList((prev) => prev.map((item) => item.id === candidate.SiteID ? { ...item, currentVersionId: null, currentContentHash: null } : item));
            loadedRoom.current = "";
            applyRoom(roomId);
          } finally {
            finishCandidateRequest(candidate);
          }
        })();
      }}
      onDecide={(roomId, candidate, decision) => {
        if (!startCandidateRequest(candidate)) return;
        void decideAction(roomId, candidate, decision).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            if ("candidate" in result && result.candidate) {
              setCandidates((prev) => prev.map((item) => item.ID === result.candidate.ID ? result.candidate : item));
            }
            return;
          }
          setCandidates((prev) => prev.map((item) => item.ID === result.candidate.ID ? result.candidate : item));
          setNotice(
            result.changed
              ? `Recorded ${result.decision}. The change was not applied.`
              : "No new decision was recorded.",
          );
        }).catch(() => {
          if (loadedRoom.current === roomId) setNotice("The decision response was interrupted. Refresh the room before trying again.");
        }).finally(() => finishCandidateRequest(candidate));
      }}
    />
  );
}
