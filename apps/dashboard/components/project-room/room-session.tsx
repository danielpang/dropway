// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Client session for rooms the API already returned. Threads and candidates
// accumulate only from those responses. Nothing here calls apply or execution.

"use client";

import * as React from "react";

import { RoomWorkspace, type RoomSiteOption } from "@/components/project-room/room-workspace";
import {
  createRoomAction,
  createThreadAction,
  decideAction,
  replyAction,
  signoffAction,
} from "@/app/(app)/rooms/actions";
import type { ProjectRoom, ReviewCandidate, ReviewThread } from "@/lib/project-room/contract";

export function RoomSession({
  orgEnabled,
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
  const [notice, setNotice] = React.useState<string | null>(
    loadError ?? (orgEnabled ? null : "The org has not enabled project rooms."),
  );

  return (
    <RoomWorkspace
      rooms={roomList}
      sites={sites}
      threads={threads}
      candidates={candidates}
      parentOrigin={parentOrigin}
      notice={notice}
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
          setThreads((prev) => [...prev, result.thread]);
          setNotice(
            result.thread.AnchorKind === "page"
              ? "Page comment stored on the original version."
              : "Image pin stored on the original version.",
          );
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
