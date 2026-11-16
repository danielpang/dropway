import { expect, it } from "vitest";
import { threadConversation } from "@/lib/project-room/thread-conversation";
import { parseHostedJob } from "@/lib/project-room/hosted";
import type { ReviewReply, ReviewThread } from "@/lib/project-room/contract";

const reply = (ID: string, minute: number): ReviewReply => ({
  ID,
  AuthorID: "user",
  Body: "@dropway remove this",
  CreatedAt: `2026-11-16T05:0${minute}:00Z`,
});
const thread = {
  ID: "thread",
  RoomID: "room",
  Replies: [reply("latest", 2), reply("first", 1)],
} as ReviewThread;
function job(id: string, trigger: string | null, state = "running") {
  return parseHostedJob({
    id,
    room_id: "room",
    site_id: "site",
    version_id: "version",
    state,
    trigger_reply_id: trigger,
    created_at: "2026-11-16T05:01:00Z",
    config: { thread_id: "thread" },
  })!;
}
const ids = (entries: ReturnType<typeof threadConversation>) =>
  entries.map((entry) =>
    entry.kind === "reply" ? entry.reply.ID : entry.job.id,
  );

it("places each response below its own request, including repeated identical comments", () => {
  const tasks = [
    job("second-task", "latest"),
    job("first-task", "first", "completed"),
  ];
  expect(ids(threadConversation(thread, tasks))).toEqual([
    "first",
    "first-task",
    "latest",
    "second-task",
  ]);
  expect(
    ids(
      threadConversation(
        thread,
        tasks.map((task) => ({ ...task, state: "completed" })),
      ),
    ),
  ).toEqual(["first", "first-task", "latest", "second-task"]);
});

it("does not move a response below unrelated newer conversation", () => {
  const newer = {
    ...reply("unrelated", 3),
    Body: "Thanks, keep the other section.",
  };
  expect(
    ids(
      threadConversation({ ...thread, Replies: [...thread.Replies, newer] }, [
        job("task", "latest"),
      ]),
    ),
  ).toEqual(["first", "latest", "task", "unrelated"]);
});

it("keeps unbound tasks below the conversation and waits for unloaded trigger replies", () => {
  const tasks = [
    job("unbound", null),
    job("older-task", "not-loaded"),
    { ...job("other-room", "latest"), roomId: "another-room" },
  ];
  expect(ids(threadConversation(thread, tasks))).toEqual([
    "first",
    "latest",
    "unbound",
  ]);
  const loaded = {
    ...thread,
    Replies: [...thread.Replies, reply("not-loaded", 0)],
  };
  expect(ids(threadConversation(loaded, tasks))).toEqual([
    "not-loaded",
    "older-task",
    "first",
    "latest",
    "unbound",
  ]);
});
