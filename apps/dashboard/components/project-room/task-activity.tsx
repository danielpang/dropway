"use client";

import { ArrowRight, CheckCircle2, Circle, CircleAlert, Clock3, MessageCircle, XCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { hostedFailureMessage, type HostedEvent, type HostedJob } from "@/lib/project-room/hosted";

const states: Record<string, { label: string; icon: typeof Circle; tone: string }> = {
  queued: { label: "Queued", icon: Clock3, tone: "text-muted-foreground" },
  running: { label: "In progress", icon: Circle, tone: "text-foreground" },
  clarification: { label: "Needs your answer", icon: MessageCircle, tone: "text-amber-700 dark:text-amber-400" },
  candidate_ready: { label: "Ready for review", icon: CheckCircle2, tone: "text-emerald-700 dark:text-emerald-400" },
  completed: { label: "Completed", icon: CheckCircle2, tone: "text-emerald-700 dark:text-emerald-400" },
  failed: { label: "Failed", icon: CircleAlert, tone: "text-red-700 dark:text-red-400" },
  cancelled: { label: "Cancelled", icon: XCircle, tone: "text-muted-foreground" },
};
const count = (value: number) => value.toLocaleString("en-US");

export function TaskList({ jobs, selectedId, onSelect }: {
  jobs: HostedJob[]; selectedId: string; onSelect: (id: string) => void;
}) {
  if (!jobs.length) return <div className="rounded-lg border border-dashed p-6 text-sm text-muted-foreground">No AI tasks yet. Tasks you authorize will appear here with their progress and results.</div>;
  return <ul className="grid gap-3 sm:grid-cols-2" aria-label="AI tasks">
    {jobs.map((job) => {
      const status = states[job.state] ?? { label: job.state.replaceAll("_", " "), icon: Circle, tone: "text-muted-foreground" };
      const Icon = status.icon;
      const selected = selectedId === job.id;
      return <li key={job.id} className="min-w-0">
        <button type="button" aria-pressed={selected} onClick={() => onSelect(job.id)}
          className={cn("flex h-full w-full flex-col gap-3 rounded-lg border p-4 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background", selected ? "border-foreground/30 bg-secondary/50" : "border-border bg-card hover:bg-muted/40")}>
          <span className={cn("flex items-center gap-2 text-xs font-medium", status.tone)}><Icon className="size-4 shrink-0" aria-hidden />{status.label}</span>
          <span className="line-clamp-3 break-words text-sm font-medium leading-relaxed">{job.config.prompt}</span>
          <span className="mt-auto flex w-full items-end justify-between gap-3 border-t border-border pt-3">
            <span className="min-w-0 space-y-1 text-xs text-muted-foreground">
              <span className="block break-all">{job.config.provider} · {job.config.modelId}</span>
              <span className="block tabular-nums">{count(job.tokensUsed)} tokens · {count(job.callsUsed)} {job.callsUsed === 1 ? "model call" : "model calls"}</span>
            </span>
            <ArrowRight className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          </span>
        </button>
      </li>;
    })}
  </ul>;
}

export function TaskSummary({ job }: { job: HostedJob }) {
  const status = states[job.state];
  return <div className="space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="text-sm font-medium">Task details</h3>
      <Badge className={job.state === "failed" ? "text-red-700 dark:text-red-400" : undefined} variant={job.state === "failed" ? "destructive" : "muted"}>{status?.label ?? job.state.replaceAll("_", " ")}</Badge>
    </div>
    <p className="whitespace-pre-wrap break-words text-sm leading-relaxed">{job.config.prompt}</p>
    {job.state === "failed" ? <div role="status" className="flex gap-3 rounded-md border border-destructive/20 bg-destructive/5 p-4 text-sm leading-relaxed"><CircleAlert className="mt-0.5 size-4 shrink-0 text-red-700 dark:text-red-400" aria-hidden /><p>{hostedFailureMessage(job.failureCode)}</p></div> : null}
    <dl className="grid grid-cols-2 gap-4 rounded-md bg-muted/40 p-4 text-sm">
      <div><dt className="text-xs text-muted-foreground">Accounted tokens</dt><dd className="mt-1 tabular-nums">{count(job.tokensUsed)} <span className="text-muted-foreground">/ {count(job.config.budget.max_tokens)}</span></dd></div>
      <div><dt className="text-xs text-muted-foreground">Model calls</dt><dd className="mt-1 tabular-nums">{count(job.callsUsed)} <span className="text-muted-foreground">/ {count(job.config.budget.max_calls)}</span></dd></div>
    </dl>
    <p className="text-xs leading-relaxed text-muted-foreground">In-flight calls reserve tokens until usage is verified.</p>
    <p className="break-words text-xs text-muted-foreground">{job.config.provider} · {job.config.modelId} · {job.config.harnessId} {job.config.harnessVersion}</p>
  </div>;
}

export function TaskEvents({ events }: { events: HostedEvent[] }) {
  return <details open className="border-t border-border pt-4">
    <summary className="cursor-pointer rounded-sm text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Progress <span className="ml-1 font-normal text-muted-foreground">({events.length})</span></summary>
    {events.length ? <ol className="mt-4 space-y-4 border-l border-border pl-4">
      {events.map((event) => <li key={event.id} className="space-y-1">
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground"><span className="capitalize">{event.kind.replaceAll("_", " ")}</span><time dateTime={event.createdAt}>{eventTime(event.createdAt)}</time></div>
        <p className="whitespace-pre-wrap break-words text-sm leading-relaxed">{event.message}</p>
      </li>)}
    </ol> : <p className="mt-3 text-sm text-muted-foreground">No progress updates yet</p>}
  </details>;
}

function eventTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : `${date.toISOString().slice(0, 10)} · ${date.toISOString().slice(11, 19)} UTC`;
}
