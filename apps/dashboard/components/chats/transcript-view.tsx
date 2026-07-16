"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { FilePenLine, Loader2, Trash2, Wrench } from "lucide-react";

import { deleteChatMessageAction } from "@/app/(app)/chats/actions";
import type { ChatMessage } from "@/lib/api";
import { renderMarkdownToHtml } from "@/lib/markdown";
import { cn } from "@/lib/utils";

/**
 * The shared-session transcript: user turns as right-aligned accent bubbles,
 * assistant turns as left-aligned neutral bubbles, and kind="action"
 * annotations as compact activity rows (file-edit path chips / tool name plus
 * the model's commentary). A divider is inserted wherever the stamped deploy
 * version_id changes, so readers see which turns produced which deploy.
 *
 * Content is Markdown rendered through renderMarkdownToHtml, which is XSS-safe
 * by construction (input is HTML-escaped before any transform).
 *
 * `canManage` (creator or org admin) shows a per-message delete button —
 * the "pasted a secret" escape hatch — wired to a server action.
 */
export function TranscriptView({
  chatId,
  messages,
  canManage,
}: {
  chatId: string;
  messages: ChatMessage[];
  canManage: boolean;
}) {
  const router = useRouter();
  const [deleting, setDeleting] = React.useState<number | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  async function onDelete(seq: number | undefined) {
    if (seq == null) return;
    if (!window.confirm("Delete this message from the shared log?")) return;
    setError(null);
    setDeleting(seq);
    const result = await deleteChatMessageAction({ chatId, seq });
    if (result.ok) {
      router.refresh();
    } else {
      setError(result.message);
    }
    setDeleting(null);
  }

  if (messages.length === 0) {
    return (
      <p className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
        No messages yet. Append turns from your agent, or import a transcript.
      </p>
    );
  }

  const rows: React.ReactNode[] = [];
  let prevVersion: string | undefined;
  messages.forEach((m, i) => {
    // Version divider: the deploy pointer moved between these messages.
    if (i > 0 && m.version_id !== prevVersion && m.version_id) {
      rows.push(<VersionDivider key={`v-${m.seq}`} versionId={m.version_id} />);
    }
    prevVersion = m.version_id;

    rows.push(
      <MessageRow
        key={m.seq ?? i}
        message={m}
        canManage={canManage}
        deleting={deleting === m.seq}
        onDelete={() => void onDelete(m.seq)}
      />,
    );
  });

  return (
    <div className="space-y-3">
      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}
      {rows}
    </div>
  );
}

function VersionDivider({ versionId }: { versionId: string }) {
  return (
    <div className="flex items-center gap-3 py-1" role="separator">
      <span className="h-px flex-1 bg-border" aria-hidden />
      <span className="font-mono text-[11px] text-muted-foreground">
        version {versionId.slice(0, 8)}
      </span>
      <span className="h-px flex-1 bg-border" aria-hidden />
    </div>
  );
}

function MessageRow({
  message,
  canManage,
  deleting,
  onDelete,
}: {
  message: ChatMessage;
  canManage: boolean;
  deleting: boolean;
  onDelete: () => void;
}) {
  const isAction = message.kind === "action";
  const isUser = message.role === "user";

  const deleteButton = canManage ? (
    <button
      type="button"
      onClick={onDelete}
      disabled={deleting}
      title="Delete message"
      aria-label={`Delete message ${message.seq ?? ""}`}
      className={cn(
        "shrink-0 rounded-md p-1.5 text-muted-foreground opacity-0 transition-opacity",
        "hover:bg-muted hover:text-destructive focus-visible:opacity-100 group-hover:opacity-100",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        "disabled:cursor-not-allowed disabled:opacity-50",
      )}
    >
      {deleting ? (
        <Loader2 className="size-3.5 animate-spin" aria-hidden />
      ) : (
        <Trash2 className="size-3.5" aria-hidden />
      )}
    </button>
  ) : null;

  if (isAction) {
    const meta = message.meta;
    const isEdit = meta?.action === "file_edit";
    return (
      <div className="group flex items-start gap-1.5">
        <div className="min-w-0 flex-1 rounded-md border border-border bg-muted/40 px-3 py-2">
          <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
            {isEdit ? (
              <FilePenLine className="size-3.5 shrink-0" aria-hidden />
            ) : (
              <Wrench className="size-3.5 shrink-0" aria-hidden />
            )}
            <span className="font-medium text-foreground">
              {isEdit ? "Edited files" : meta?.tool || "Ran a tool"}
            </span>
            {(meta?.paths ?? []).map((p) => (
              <code
                key={p}
                className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground"
              >
                {p}
              </code>
            ))}
          </div>
          {message.content ? (
            <div
              className="chat-markdown mt-1.5 space-y-2 text-sm leading-relaxed text-muted-foreground"
              dangerouslySetInnerHTML={{ __html: renderMarkdownToHtml(message.content) }}
            />
          ) : null}
        </div>
        {deleteButton}
      </div>
    );
  }

  return (
    <div className={cn("group flex items-start gap-1.5", isUser && "flex-row-reverse")}>
      <div
        className={cn(
          "max-w-[85%] rounded-lg px-3 py-2",
          isUser
            ? "bg-primary text-primary-foreground"
            : "border border-border bg-muted/60 text-foreground",
        )}
      >
        <div
          className="chat-markdown space-y-2 break-words text-sm leading-relaxed [&_a]:underline [&_code]:font-mono [&_code]:text-[0.85em] [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-black/10 [&_pre]:p-2 dark:[&_pre]:bg-white/10"
          dangerouslySetInnerHTML={{ __html: renderMarkdownToHtml(message.content ?? "") }}
        />
      </div>
      {deleteButton}
    </div>
  );
}
