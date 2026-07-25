"use client";

import { useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  FileText,
  FolderTree,
  Loader2,
  SquarePen,
  Terminal,
} from "lucide-react";

import type { ToolItem } from "@/components/ai/transcript";
import { toolLabel, toolSummary } from "@/components/ai/transcript";
import { cn } from "@/lib/utils";

const TOOL_ICONS: Record<string, typeof Terminal> = {
  run_command: Terminal,
  write_file: SquarePen,
  read_file: FileText,
  list_files: FolderTree,
};

/**
 * One tool call in the chat: a compact card with the tool's verb and its
 * subject (the command, the path), a spinner while it runs, and — once done —
 * a disclosure that reveals the output. Output is capped to a scrollable pane
 * so a long build log can't swallow the conversation. read/list results are
 * model-facing noise, so only run_command and errors default to interesting;
 * everything stays one click away rather than auto-expanded.
 */
export function ToolCard({ item }: { item: ToolItem }) {
  const [open, setOpen] = useState(false);
  const Icon = TOOL_ICONS[item.tool] ?? Terminal;
  const summary = toolSummary(item.tool, item.args);
  const hasResult = item.done && item.result !== null && item.result !== "";
  const Chevron = open ? ChevronDown : ChevronRight;

  return (
    <div className="max-w-[85%] rounded-md border bg-muted/40 text-xs">
      <button
        type="button"
        onClick={() => hasResult && setOpen((o) => !o)}
        disabled={!hasResult}
        aria-expanded={hasResult ? open : undefined}
        className={cn(
          "flex w-full items-center gap-2 px-2.5 py-1.5 text-left",
          hasResult && "hover:bg-muted/60",
        )}
      >
        {item.done ? (
          <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        ) : (
          <Loader2 className="size-3.5 shrink-0 animate-spin text-muted-foreground" aria-hidden />
        )}
        <span className="shrink-0 font-medium text-muted-foreground">
          {toolLabel(item.tool, item.done)}
        </span>
        {summary && (
          <span className="truncate font-mono text-muted-foreground/80" title={summary}>
            {summary}
          </span>
        )}
        {hasResult && (
          <Chevron className="ml-auto size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        )}
      </button>
      {open && hasResult && (
        <pre className="max-h-56 overflow-auto whitespace-pre-wrap border-t px-2.5 py-2 font-mono text-[0.7rem] leading-relaxed text-muted-foreground">
          {item.result}
        </pre>
      )}
    </div>
  );
}
