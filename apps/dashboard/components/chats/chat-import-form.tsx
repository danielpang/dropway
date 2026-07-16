"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ArrowRight, Loader2, Scissors, Upload } from "lucide-react";

import { createChatAction } from "@/app/(app)/chats/actions";
import { SOURCE_TOOL_OPTIONS } from "@/components/chats/source-tools";
import { UpgradeModal } from "@/components/sites/upgrade-modal";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import type { QuotaExceeded } from "@/lib/api";

/** A site the log can be attached to (id + slug are all the picker needs). */
export type AttachableSite = { id: string; slug: string };

/**
 * The "Share a conversation" import form: paste (or upload) a transcript
 * export — Claude Code JSONL, a ChatGPT JSON export, or plain text — pick the
 * source tool, optionally derive kind="action" rows from tool activity, and
 * optionally attach the log to a site.
 *
 * The API normalizes the transcript server-side and DISCLOSES what it kept:
 * `pruned`/`dropped` > 0 means the tier's sliding window kept only the newest
 * messages, shown here as an informational notice with the upgrade pointer.
 * A hard 402 cap (pro tier) opens the shared UpgradeModal instead.
 */
export function ChatImportForm({ sites }: { sites: AttachableSite[] }) {
  const router = useRouter();

  const [title, setTitle] = React.useState("");
  const [sourceTool, setSourceTool] = React.useState<string>("claude_code");
  const [transcript, setTranscript] = React.useState("");
  const [deriveActions, setDeriveActions] = React.useState(true);
  const [siteId, setSiteId] = React.useState("");

  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [quota, setQuota] = React.useState<QuotaExceeded | null>(null);
  const [upgradeOpen, setUpgradeOpen] = React.useState(false);
  // Set after a successful import that pruned/dropped messages: the disclosure
  // step (the import itself succeeded; this is informational).
  const [imported, setImported] = React.useState<{
    chatId: string;
    kept: number;
    pruned: number;
    dropped: number;
    window: number;
  } | null>(null);

  const fileInputRef = React.useRef<HTMLInputElement>(null);

  async function onFilePicked(file: File | undefined) {
    if (!file) return;
    setError(null);
    try {
      setTranscript(await file.text());
      if (!title) setTitle(file.name.replace(/\.[^.]+$/, ""));
    } catch {
      setError("Couldn't read that file. Paste the transcript instead.");
    }
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!transcript.trim()) {
      setError("Paste or upload a transcript to share.");
      return;
    }
    setError(null);
    setPending(true);
    const result = await createChatAction({
      title: title.trim() || undefined,
      source_tool: sourceTool,
      site_id: siteId || undefined,
      transcript,
      derive_actions: deriveActions,
    });
    if (!result.ok) {
      if (result.quota) {
        setQuota(result.quota);
        setUpgradeOpen(true);
      }
      setError(result.message);
      setPending(false);
      return;
    }
    const chatId = result.chatLog.id ?? "";
    if (result.pruned > 0 || result.dropped > 0) {
      // Disclose the trim before navigating — the user should know the shared
      // log is not their full history.
      setImported({
        chatId,
        kept: result.chatLog.message_count ?? result.appended,
        pruned: result.pruned,
        dropped: result.dropped,
        window: result.window,
      });
      setPending(false);
      return;
    }
    router.push(`/chats/${chatId}`);
    router.refresh();
  }

  if (imported) {
    return (
      <Card className="space-y-4 p-6">
        <div className="flex items-start gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <Scissors className="size-5" aria-hidden />
          </span>
          <div className="space-y-1">
            <p className="text-sm font-medium text-foreground">
              Shared — with the last {imported.kept} messages
            </p>
            <p className="text-sm text-muted-foreground">
              {imported.pruned > 0 ? (
                <>
                  Your plan keeps a window of the newest{" "}
                  {imported.window > 0 ? imported.window : imported.kept} messages per
                  log, so {imported.pruned} older{" "}
                  {imported.pruned === 1 ? "message was" : "messages were"} trimmed.{" "}
                </>
              ) : null}
              {imported.dropped > 0 ? (
                <>
                  {imported.dropped} {imported.dropped === 1 ? "message" : "messages"}{" "}
                  beyond the import limit {imported.dropped === 1 ? "was" : "were"} left
                  out.{" "}
                </>
              ) : null}
              Keep your full history with Pro.
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild>
            <Link href={`/chats/${imported.chatId}`}>
              View the chat
              <ArrowRight aria-hidden />
            </Link>
          </Button>
          <Button asChild variant="outline">
            <Link href="/billing">Upgrade to Pro</Link>
          </Button>
        </div>
      </Card>
    );
  }

  return (
    <form onSubmit={onSubmit} className="space-y-5">
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="chat-title">Title</Label>
          <Input
            id="chat-title"
            value={title}
            maxLength={200}
            placeholder="Building the launch page"
            onChange={(e) => setTitle(e.target.value)}
            disabled={pending}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="chat-source">Source tool</Label>
          <Select
            id="chat-source"
            value={sourceTool}
            onChange={(e) => setSourceTool(e.target.value)}
            disabled={pending}
          >
            {SOURCE_TOOL_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </Select>
        </div>
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <Label htmlFor="chat-transcript">Transcript</Label>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => fileInputRef.current?.click()}
            disabled={pending}
          >
            <Upload aria-hidden />
            Upload a file
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            accept=".jsonl,.json,.txt,.md,text/plain,application/json"
            className="hidden"
            onChange={(e) => void onFilePicked(e.target.files?.[0])}
          />
        </div>
        <textarea
          id="chat-transcript"
          value={transcript}
          rows={12}
          spellCheck={false}
          placeholder="Paste your session export here — Claude Code JSONL, a ChatGPT export, or plain text."
          onChange={(e) => setTranscript(e.target.value)}
          disabled={pending}
          className="flex w-full resize-y rounded-md border border-input bg-background px-3 py-2 font-mono text-xs leading-relaxed shadow-sm transition-colors placeholder:font-sans placeholder:text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50"
        />
        <p className="text-xs text-muted-foreground">
          The format is detected automatically. Very long exports keep their newest
          messages.
        </p>
      </div>

      <div className="flex items-start gap-2.5">
        <input
          id="chat-derive"
          type="checkbox"
          checked={deriveActions}
          onChange={(e) => setDeriveActions(e.target.checked)}
          disabled={pending}
          className="mt-0.5 size-4 accent-primary"
        />
        <div className="space-y-0.5">
          <Label htmlFor="chat-derive">Include activity annotations</Label>
          <p className="text-sm text-muted-foreground">
            Condense tool runs and file edits from the transcript into compact
            activity rows instead of dropping them.
          </p>
        </div>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="chat-site">Attach to a site (optional)</Label>
        <Select
          id="chat-site"
          value={siteId}
          onChange={(e) => setSiteId(e.target.value)}
          disabled={pending}
        >
          <option value="">Don&rsquo;t attach</option>
          {sites.map((s) => (
            <option key={s.id} value={s.id}>
              {s.slug}
            </option>
          ))}
        </Select>
        <p className="text-xs text-muted-foreground">
          An attached log can appear on the served site as a &ldquo;How this was
          made&rdquo; panel. Each site holds one log.
        </p>
      </div>

      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}

      <Button type="submit" disabled={pending} aria-busy={pending}>
        {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
        Share this session
      </Button>

      <UpgradeModal quota={quota} open={upgradeOpen} onOpenChange={setUpgradeOpen} />
    </form>
  );
}
