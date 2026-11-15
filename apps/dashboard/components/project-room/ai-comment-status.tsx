"use client";
import * as React from "react";
import { Bot, LoaderCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { HostedJob } from "@/lib/project-room/hosted";
const workingPhrases = [
  "Working on it…",
  "Still working on your request…",
  "I’ll share the proposed changes here when they’re ready.",
];
export function aiStatusText(job: HostedJob, phrase = 0): string {
  if (job.state === "queued")
    return "Your request is queued. I’ll start shortly.";
  if (job.state === "running")
    return workingPhrases[phrase % workingPhrases.length]!;
  if (job.state === "clarification")
    return "I need a little more information. Open Activity to reply.";
  if (
    job.state === "candidate_ready" ||
    (job.state === "completed" && job.candidateId)
  )
    return "Your proposed changes are ready to review. Nothing has been published.";
  if (job.state === "completed")
    return "Finished. Open Activity to see the result.";
  if (job.state === "cancelled")
    return "This request was cancelled. No changes were published.";
  if (job.state === "failed")
    return "I couldn’t finish this request. No changes were published. Open Activity for details.";
  return "Checking the request status…";
}
export function AICommentStatus({
  job,
  stale = false,
  onReview,
  onActivity,
}: {
  job: HostedJob;
  stale?: boolean;
  onReview: () => void;
  onActivity: () => void;
}) {
  const [phrase, setPhrase] = React.useState(0);
  React.useEffect(() => {
    setPhrase(0);
    if (job.state !== "running" || stale) return;
    const timer = setInterval(
      () => setPhrase((value) => (value + 1) % workingPhrases.length),
      5000,
    );
    return () => clearInterval(timer);
  }, [job.id, job.state, stale]);
  const working = !stale && (job.state === "running" || job.state === "queued");
  const ready =
    job.state === "candidate_ready" ||
    (job.state === "completed" && !!job.candidateId);
  return (
    <div
      className="flex gap-2 rounded-md bg-muted/50 p-2.5 text-xs"
      role="status"
      aria-live="polite"
    >
      <Bot className="mt-0.5 size-4 shrink-0" aria-hidden />
      <div className="min-w-0 space-y-1">
        <p className="font-medium">@dropway</p>
        <p>
          {stale
            ? "I can’t refresh this request’s status right now. Retrying…"
            : aiStatusText(job, phrase)}
        </p>
        {ready ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onReview}
          >
            Review changes
          </Button>
        ) : !working ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onActivity}
          >
            View activity
          </Button>
        ) : null}
      </div>
      {working ? (
        <LoaderCircle
          className="ml-auto size-3.5 shrink-0 animate-spin motion-reduce:animate-none"
          aria-hidden
        />
      ) : null}
    </div>
  );
}
