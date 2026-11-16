import type { ReviewReply, ReviewThread } from "./contract";
import type { HostedJob } from "./hosted";

type ConversationEntry =
  | { kind: "reply"; reply: ReviewReply }
  | { kind: "job"; job: HostedJob };

/** Keep each task below its triggering comment even as its status changes. */
export function threadConversation(
  thread: ReviewThread,
  jobs: HostedJob[],
): ConversationEntry[] {
  const replies = [...thread.Replies].sort(
    (a, b) => (Date.parse(a.CreatedAt) || 0) - (Date.parse(b.CreatedAt) || 0),
  );
  const tasks = jobs
    .filter(
      (job) =>
        job.roomId === thread.RoomID && job.config.threadId === thread.ID,
    )
    .sort(
      (a, b) =>
        (Date.parse(a.createdAt ?? "") || 0) -
        (Date.parse(b.createdAt ?? "") || 0),
    );
  const entries: ConversationEntry[] = [];
  for (const reply of replies) {
    entries.push({ kind: "reply", reply });
    for (const job of tasks) {
      if (job.triggerReplyId === reply.ID) entries.push({ kind: "job", job });
    }
  }
  // Older API versions and manually started tasks have no comment binding.
  // Keep those at the bottom rather than attaching them to an unrelated reply.
  // Bound tasks whose comment has not loaded yet appear when it is loaded.
  for (const job of tasks) {
    if (!job.triggerReplyId) entries.push({ kind: "job", job });
  }
  return entries;
}
