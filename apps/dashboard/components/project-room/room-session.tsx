// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Client session for rooms the API already returned. Threads and candidates
// accumulate only from those responses. Nothing here calls apply or execution.

"use client";

import * as React from "react";

import { RoomWorkspace, type RoomSiteOption } from "@/components/project-room/room-workspace";
import {
  addMemberAction,
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
import type { CandidateDiffView, ProjectRoom, ReviewCandidate, ReviewThread, RoomMemberRow, RoomSiteRef, SiteCommentItem } from "@/lib/project-room/contract";

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
  const [roomList, setRoomList] = React.useState(rooms);
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
    void loadRoomAction(roomId).then((result) => {
      if (!result.ok) {
        setNotice(result.message);
        return;
      }
      setMembers(result.detail.members);
      setAttachedSites(result.detail.sites);
      setThreads(result.threads);
      setThreadCursor(result.threadCursor);
      setCandidates(result.candidates);
      setCandidateCursor(result.candidateCursor);
    });
  }, []);

  const loadComments = React.useCallback((siteId: string, cursor?: string) => {
    if (!siteId) return;
    void loadSiteCommentsAction(siteId, cursor).then((result) => {
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
      rooms={roomList}
      sites={sites}
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
      onSelectSite={loadComments}
      onLoadMoreComments={loadComments}
      onLoadMoreThreads={(roomId, cursor) => {
        void loadMoreThreadsAction(roomId, cursor).then((result) => {
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
      onReply={(roomId, threadId, body) => {
        void replyAction(roomId, threadId, body).then((result) => {
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
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          loadedRoom.current = "";
          applyRoom(roomId);
          setNotice("Member added.");
        });
      }}
      onAttachSite={(roomId, siteId) => {
        void attachSiteAction(roomId, siteId).then((result) => {
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
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) => prev.map((item) => item.ID === result.thread.ID ? { ...item, ...result.thread, Replies: result.thread.Replies.length > 0 ? result.thread.Replies : item.Replies } : item));
          setNotice("Thread resolved. The anchor history was not rewritten.");
        });
      }}
      onReanchor={(roomId, threadId, reason) => {
        void reanchorAction(roomId, threadId, reason).then((result) => {
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) => prev.map((item) => item.ID === threadId ? { ...item, AnchorState: result.thread.AnchorState } : item));
          setNotice("Page reanchor appended. The original pin is unchanged.");
        });
      }}
      onSubmitCandidate={(input) => {
        void submitCandidateAction(input).then((result) => {
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setCandidates((prev) => [result.candidate, ...prev.filter((item) => item.ID !== result.candidate.ID)]);
          setNotice("Candidate recorded. The site was not edited.");
        });
      }}
      onDecide={(roomId, candidate, decision) => {
        void decideAction(roomId, candidate, decision).then((result) => {
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
        });
      }}
    />
  );
}
