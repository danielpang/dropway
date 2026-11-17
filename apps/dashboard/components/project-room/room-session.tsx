// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Client session for rooms the API already returned. Threads and candidates
// accumulate only from those responses. Applying is an explicit human action.

"use client";

import * as React from "react";
import { useRoomNavigation } from "@/lib/project-room/use-room-navigation";
import { loadRoomDetail, loadRoomDiscussion } from "@/lib/project-room/room-client";
import { listHostedJobsAction } from "@/app/(app)/rooms/hosted-actions";
import type { HostedJob } from "@/lib/project-room/hosted";

import {
  RoomWorkspace,
  type RoomSiteOption,
} from "@/components/project-room/room-workspace";
import {
  addMemberAction,
  loadRoomActivityAction,
  markRoomReadAction,
  loadThreadLinkAction,
  loadThreadRevisionsAction,
  acceptAndApplyCandidateAction,
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
  reanchorAction,
  replyAction,
  resolveAction,
  submitCandidateAction,
} from "@/app/(app)/rooms/actions";
import { appendThreadRevisions } from "@/lib/project-room/contract";
import type {
  RoomActivity,
  CandidateDiffView,
  ProjectRoom,
  ReviewCandidate,
  ReviewThread,
  RoomMemberRow,
  RoomSiteRef,
} from "@/lib/project-room/contract";

export function RoomSession({
  orgEnabled: initialOrgEnabled,
  executionConfigured = false,
  rooms,
  sites,
  parentOrigin,
  loadError,
}: {
  orgEnabled: boolean;
  executionConfigured?: boolean;
  rooms: ProjectRoom[];
  sites: RoomSiteOption[];
  parentOrigin: string;
  loadError?: string;
}) {
  const { location, navigate } = useRoomNavigation();
  const [discussionLoading, setDiscussionLoading] = React.useState(false);
  const [discussionError, setDiscussionError] = React.useState<string | null>(null);
  const [jobs, setJobs] = React.useState<HostedJob[]>([]);
  const [jobStatusUnavailable, setJobStatusUnavailable] = React.useState(false);
  const commentRetries = React.useRef(new Map<string, string>());
  const commentRequests = React.useRef(new Set<string>());
  const [activity, setActivity] = React.useState<RoomActivity[]>([]);
  const [activityCursor, setActivityCursor] = React.useState<string | null>(
    null,
  );
  const [roomList, setRoomList] = React.useState(rooms);
  const [publishedRevision,setPublishedRevision] = React.useState<{siteId:string;versionId:string}|null>(null);
  const [siteList, setSiteList] = React.useState(sites);
  const candidateRequests = React.useRef(new Set<string>());
  const [busyCandidates, setBusyCandidates] = React.useState<string[]>([]);
  const roomRequest = React.useRef(0);
  const startCandidateRequest = (candidate: ReviewCandidate) => {
    if (
      candidate.RoomID !== loadedRoom.current ||
      candidateRequests.current.has(candidate.ID)
    )
      return false;
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
  const [diffs, setDiffs] = React.useState<Record<string, CandidateDiffView>>(
    {},
  );
  const requestedDiffs = React.useRef(new Set<string>());
  const [members, setMembers] = React.useState<RoomMemberRow[]>([]);
  const [attachedSites, setAttachedSites] = React.useState<RoomSiteRef[]>([]);
  const [threadCursor, setThreadCursor] = React.useState<string | null>(null);
  const [candidateCursor, setCandidateCursor] = React.useState<string | null>(
    null,
  );
  const loadedRoom = React.useRef("");
  const [detailsRoomId, setDetailsRoomId] = React.useState<string | null>(null);
  const [roomLoadError, setRoomLoadError] = React.useState<{
    roomId: string;
    message: string;
  } | null>(null);

  React.useEffect(() => {
    setJobs([]);
    setJobStatusUnavailable(false);
    if (!detailsRoomId || !executionConfigured) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const result = await listHostedJobsAction(detailsRoomId!);
        if (!active) return;
        if (result.ok) {
          setJobs(result.jobs);
          setJobStatusUnavailable(false);
        } else setJobStatusUnavailable(true);
      } catch {
        if (active) setJobStatusUnavailable(true);
      }
      if (active) timer = setTimeout(() => void poll(), 3000);
    }
    void poll();
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [detailsRoomId, executionConfigured]);

  const applyRoom = React.useCallback((roomId: string) => {
    if (loadedRoom.current === roomId) return;
    loadedRoom.current = roomId;
    const request = ++roomRequest.current;
    setDetailsRoomId(null);
    setRoomLoadError(null);
    setActivity([]);
    setActivityCursor(null);
    setMembers([]);
    setAttachedSites([]);
    setThreads([]);
    setCandidates([]);
    setThreadCursor(null);
    setCandidateCursor(null);
    setDiscussionLoading(false);
    setDiscussionError(null);
    if (!roomId) return;
    void loadRoomDetail(roomId)
      .then((result) => {
        if (request !== roomRequest.current || roomId !== loadedRoom.current)
          return;
        if (!result.ok) {
          setNotice(result.message);
          setRoomLoadError({ roomId, message: result.message });
          return;
        }
        setRoomList((prev) =>
          prev.map((item) => (item.ID === roomId ? result.detail.room : item)),
        );
        setDetailsRoomId(roomId);
        setMembers(result.detail.members);
        setAttachedSites(result.detail.sites);
        setDiscussionLoading(true);
        void loadRoomDiscussion(roomId).then((discussion) => {
          if (request !== roomRequest.current || roomId !== loadedRoom.current) return;
          setDiscussionLoading(false);
          if (!discussion.ok) {
            setDiscussionError(discussion.message);
            return;
          }
          setMembers(discussion.members);
          // Preserve comments posted while the initial discussion was loading.
          setThreads((previous) => [
            ...previous,
            ...discussion.threads.filter((thread) => !previous.some((item) => item.ID === thread.ID)),
          ]);
          setThreadCursor(discussion.threadCursor);
          setCandidates(discussion.candidates);
          setCandidateCursor(discussion.candidateCursor);
          const linkedId = window.location.hash.startsWith("#thread-")
            ? window.location.hash.slice(8)
            : "";
          if (linkedId) {
            void loadThreadLinkAction(roomId, linkedId).then((linked) => {
              if (
                request !== roomRequest.current ||
                loadedRoom.current !== roomId
              )
                return;
              if (!linked.ok) {
                setNotice(linked.message);
                return;
              }
              setThreads((prev) => [
                linked.thread,
                ...prev.filter((item) => item.ID !== linked.thread.ID),
              ]);
              requestAnimationFrame(() =>
                document
                  .getElementById(`thread-${linked.thread.ID}`)
                  ?.scrollIntoView(),
              );
            });
          }
        }).catch(() => {
          if (request !== roomRequest.current || roomId !== loadedRoom.current) return;
          setDiscussionLoading(false);
          setDiscussionError("Comments and proposals could not be loaded.");
        });
        void loadRoomActivityAction(roomId).then((activityResult) => {
          if (request !== roomRequest.current || loadedRoom.current !== roomId)
            return;
          if (!activityResult.ok) {
            setNotice(activityResult.message);
            return;
          }
          setActivity(activityResult.items);
          setActivityCursor(activityResult.nextCursor);
          setRoomList((prev) =>
            prev.map((item) =>
              item.ID === roomId
                ? { ...item, Unread: activityResult.unread }
                : item,
            ),
          );
        });
      })
      .catch(() => {
        if (request !== roomRequest.current || roomId !== loadedRoom.current)
          return;
        const message = "The project could not be loaded. Try refreshing it.";
        setNotice(message);
        setRoomLoadError({ roomId, message });
      });
  }, []);

  const [orgEnabled, setOrgEnabled] = React.useState(initialOrgEnabled);
  const [notice, setNotice] = React.useState<string | null>(loadError ?? null);

  React.useEffect(() => {
    for (const candidate of candidates) {
      if (requestedDiffs.current.has(candidate.ID)) continue;
      requestedDiffs.current.add(candidate.ID);
      setDiffs((prev) => ({ ...prev, [candidate.ID]: { status: "loading" } }));
      void loadCandidateDiffAction(candidate.RoomID, candidate.ID).then(
        (result) => {
          const next: CandidateDiffView = result.ok
            ? { status: "verified", diff: result.diff }
            : "unverified" in result && result.unverified
              ? { status: "unverified" }
              : { status: "unavailable" };
          setDiffs((prev) => ({ ...prev, [candidate.ID]: next }));
        },
      );
    }
  }, [candidates]);

  const publishCandidate = (roomId: string, candidate: ReviewCandidate) => {
    if (!startCandidateRequest(candidate)) return;
    void (async () => {
      try {
        const result = await acceptAndApplyCandidateAction(roomId, candidate).catch(
          () => ({
            ok: false as const,
            message:
              "The apply response was interrupted. Refreshing the room to check whether it was published.",
          }),
        );
        if (loadedRoom.current !== roomId) return;
        if (result.ok) {
          setPublishedRevision({ siteId: candidate.SiteID, versionId: result.versionId });
        }
        setNotice(
          result.ok
            ? result.changed
              ? "Approved change published."
              : "This change was already applied. No new publication was made."
            : result.message,
        );
        // Reload after ambiguous failures too: the server may have committed before a response was lost.
        const refreshed = await loadRoomSitesAction();
        if (loadedRoom.current !== roomId) return;
        if (refreshed.ok) {
          setSiteList(refreshed.sites);
          const published = refreshed.sites.find(
            site => site.id === candidate.SiteID && site.currentContentHash === candidate.ContentHash,
          );
          if (published?.currentVersionId) {
            setPublishedRevision({ siteId: candidate.SiteID, versionId: published.currentVersionId });
          }
        } else
          setSiteList((prev) =>
            prev.map((item) =>
              item.id === candidate.SiteID
                ? {
                    ...item,
                    currentVersionId: null,
                    currentContentHash: null,
                  }
                : item,
            ),
          );
        loadedRoom.current = "";
        applyRoom(roomId);
      } finally {
        finishCandidateRequest(candidate);
      }
    })();
  };
  return (
    <RoomWorkspace
      navigation={location}
      onNavigate={navigate}
      discussionLoading={discussionLoading}
      discussionError={discussionError}
      publishedRevision={publishedRevision}
      jobs={jobs}
      jobStatusUnavailable={jobStatusUnavailable}
      detailsRoomId={detailsRoomId}
      roomLoadError={roomLoadError}
      activity={activity}
      activityCursor={activityCursor}
      onLoadMoreActivity={(roomId, cursor) => {
        void loadRoomActivityAction(roomId, cursor).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setActivity((prev) => {
            const seen = new Set(prev.map((item) => item.id));
            return [
              ...prev,
              ...result.items.filter((item) => !seen.has(item.id)),
            ];
          });
          setActivityCursor(result.nextCursor);
        });
      }}
      onMarkRead={(roomId) => {
        void markRoomReadAction(roomId).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setRoomList((prev) =>
            prev.map((item) =>
              item.ID === roomId ? { ...item, Unread: 0 } : item,
            ),
          );
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
      threadCursor={threadCursor}
      candidateCursor={candidateCursor}
      parentOrigin={parentOrigin}
      notice={notice}
      executionConfigured={executionConfigured}
      orgEnabled={orgEnabled}
      onEnableRooms={() => {
        void enableRoomsAction(true).then((result) => {
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setOrgEnabled(true);
          setNotice(
            "Project rooms are enabled. Existing CLI and MCP publishing access is unchanged.",
          );
        });
      }}
      onSelectRoom={applyRoom}
      onRefreshRoom={(roomId) => {
        void loadRoomSitesAction().then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setSiteList(result.sites);
          requestedDiffs.current.clear();
          setDiffs({});
          loadedRoom.current = "";
          applyRoom(roomId);
        });
      }}
      onLoadMoreThreads={(roomId, cursor) => {
        void loadMoreThreadsAction(roomId, cursor).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) => {
            const seen = new Set(prev.map((item) => item.ID));
            return [
              ...prev,
              ...result.threads.filter((item) => !seen.has(item.ID)),
            ];
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
            return [
              ...prev,
              ...result.candidates.filter((item) => !seen.has(item.ID)),
            ];
          });
          setCandidateCursor(result.nextCursor);
        });
      }}
      onSiteCreated={(site) =>
        setSiteList((prev) => [
          ...prev.filter((item) => item.id !== site.id),
          site,
        ])
      }
      onCreateRoom={async (name, siteIds = []) => {
        try {
          const result = await createRoomAction(name);
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          const failures: string[] = [];
          for (const siteId of [...new Set(siteIds)]) {
            try {
              const attached = await attachSiteAction(result.room.ID, siteId);
              if (!attached.ok)
                failures.push(
                  `${siteList.find((site) => site.id === siteId)?.slug ?? siteId}: ${attached.message}`,
                );
            } catch {
              failures.push(
                `${siteList.find((site) => site.id === siteId)?.slug ?? siteId}: could not add this site`,
              );
            }
          }
          setRoomList((prev) => [...prev, result.room]);
          setNotice(
            failures.length
              ? `Created ${result.room.Name}, but some sites could not be added. Use Add a site to retry. ${failures.join(". ")}`
              : `Created ${result.room.Name}.`,
          );
          return result.room.ID;
        } catch {
          setNotice(
            "The project could not be created. Refresh before trying again.",
          );
        }
      }}
      onCreateThread={async (input) => {
        const request = JSON.stringify(input);
        if (commentRequests.current.has(request)) return false;
        commentRequests.current.add(request);
        return createThreadAction({
          ...input,
          retryThreadId: commentRetries.current.get(request),
        })
          .then((result) => {
            if (!result.ok && "retryThreadId" in result && result.retryThreadId)
              commentRetries.current.set(request, result.retryThreadId);
            if (result.ok)
              commentRetries.current.set(request, result.thread.ID);
            if (loadedRoom.current !== input.roomId) return false;
            if (!result.ok) {
              setNotice(result.message);
              return false;
            }
            setThreads((prev) => {
              const rest = prev.filter((item) => item.ID !== result.thread.ID);
              return [result.thread, ...rest];
            });
            if (result.job)
              setJobs((previous) => [
                result.job!,
                ...previous.filter((job) => job.id !== result.job!.id),
              ]);
            setNotice(
              result.job
                ? "Comment stored and revision queued. Review the proposed change before accepting or rejecting it."
                : result.thread.AnchorKind === "page"
                  ? "Page comment stored on the original version."
                  : "Image pin stored on the original version.",
            );
            return true;
          })
          .catch(() => {
            if (loadedRoom.current === input.roomId)
              setNotice(
                "The comment request was interrupted. Refresh the thread before retrying.",
              );
            return false;
          })
          .finally(() => commentRequests.current.delete(request));
      }}
      onLoadMoreRevisions={(roomId, threadId, cursor) => {
        void loadThreadRevisionsAction(roomId, threadId, cursor).then(
          (result) => {
            if (loadedRoom.current !== roomId) return;
            if (!result.ok) {
              setNotice(result.message);
              return;
            }
            setThreads((prev) =>
              prev.map((thread) =>
                thread.ID === threadId
                  ? appendThreadRevisions(thread, result.thread)
                  : thread,
              ),
            );
          },
        );
      }}
      onLoadMoreReplies={(roomId, threadId, cursor) => {
        void loadThreadLinkAction(roomId, threadId, cursor).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setThreads((prev) =>
            prev.map((thread) => {
              if (thread.ID !== threadId) return thread;
              const seen = new Set(thread.Replies.map((reply) => reply.ID));
              return {
                ...thread,
                NextReplyCursor: result.thread.NextReplyCursor,
                Replies: [
                  ...thread.Replies,
                  ...result.thread.Replies.filter(
                    (reply) => !seen.has(reply.ID),
                  ),
                ],
              };
            }),
          );
        });
      }}
      onReply={(roomId, threadId, body, execution) => {
        return replyAction(roomId, threadId, body, execution).then((result) => {
          if (loadedRoom.current !== roomId) return false;
          if (!result.ok) {
            setNotice(result.message);
            return false;
          }
          setThreads((prev) =>
            prev.map((thread) =>
              thread.ID === threadId
                ? {
                    ...thread,
                    Replies: [
                      ...thread.Replies.filter(
                        (item) => item.ID !== result.reply.ID,
                      ),
                      result.reply,
                    ],
                  }
                : thread,
            ),
          );
          if (result.job)
            setJobs((previous) => [
              result.job!,
              ...previous.filter((job) => job.id !== result.job!.id),
            ]);
          setNotice(
            result.job
              ? "Reply stored and revision queued. Review the proposed change before accepting or rejecting it."
              : "Reply stored.",
          );
          return true;
        }).catch(() => {
          if (loadedRoom.current === roomId)
            setNotice("The reply could not be posted. Your draft has been kept; refresh the thread before retrying.");
          return false;
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
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          loadedRoom.current = "";
          applyRoom(roomId);
          setNotice("Member role updated.");
        });
      }}
      onRemoveMember={(roomId, userId) => {
        void removeMemberAction(roomId, userId).then((result) => {
          if (loadedRoom.current !== roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
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
          setNotice(
            "Site attached. Existing CLI and MCP publishing access is unchanged.",
          );
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
          setThreads((prev) =>
            prev.map((item) =>
              item.ID === result.thread.ID
                ? {
                    ...item,
                    Status: result.thread.Status,
                    AnchorState: result.thread.AnchorState,
                  }
                : item,
            ),
          );
          setNotice("Comment moved to resolved comments.");
        }).catch(() => {
          if (loadedRoom.current === roomId)
            setNotice("The comment could not be resolved. Try again.");
        });
      }}
      onReanchor={(roomId, threadId, reason, selection) => {
        void reanchorAction(roomId, threadId, reason, selection).then(
          (result) => {
            if (loadedRoom.current !== roomId) return;
            if (!result.ok) {
              setNotice(result.message);
              return;
            }
            setThreads((prev) =>
              prev.map((item) =>
                item.ID === threadId
                  ? { ...item, AnchorState: result.thread.AnchorState }
                  : item,
              ),
            );
            setNotice(
              "Anchor revision appended. The original pin is unchanged.",
            );
            void loadThreadLinkAction(roomId, threadId).then((refreshed) => {
              if (loadedRoom.current !== roomId || !refreshed.ok) return;
              setThreads((prev) =>
                prev.map((thread) =>
                  thread.ID === threadId
                    ? {
                        ...thread,
                        Revisions: refreshed.thread.Revisions,
                        NextRevisionCursor: refreshed.thread.NextRevisionCursor,
                      }
                    : thread,
                ),
              );
            });
          },
        );
      }}
      onSubmitCandidate={(input) => {
        void submitCandidateAction(input).then((result) => {
          if (loadedRoom.current !== input.roomId) return;
          if (!result.ok) {
            setNotice(result.message);
            return;
          }
          setCandidates((prev) => [
            result.candidate,
            ...prev.filter((item) => item.ID !== result.candidate.ID),
          ]);
          setNotice("Candidate recorded. The site was not edited.");
        });
      }}
      onApply={publishCandidate}
      onDecide={(roomId, candidate, decision) => {
        if (decision === "approve") {
          publishCandidate(roomId, candidate);
          return;
        }
        if (!startCandidateRequest(candidate)) return;
        void decideAction(roomId, candidate, decision)
          .then((result) => {
            if (loadedRoom.current !== roomId) return;
            if (!result.ok) {
              setNotice(result.message);
              if ("candidate" in result && result.candidate) {
                setCandidates((prev) =>
                  prev.map((item) =>
                    item.ID === result.candidate.ID ? result.candidate : item,
                  ),
                );
              }
              return;
            }
            setCandidates((prev) =>
              prev.map((item) =>
                item.ID === result.candidate.ID ? result.candidate : item,
              ),
            );
            setNotice(
              result.changed
                ? "Change rejected."
                : "No new decision was recorded.",
            );
          })
          .catch(() => {
            if (loadedRoom.current === roomId)
              setNotice(
                "The decision response was interrupted. Refresh the room before trying again.",
              );
          })
          .finally(() => finishCandidateRequest(candidate));
      }}
    />
  );
}
