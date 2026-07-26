"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  ExternalLink,
  Info,
  Monitor,
  RefreshCw,
  Send,
  Smartphone,
  Sparkles,
  Tablet,
} from "lucide-react";
import { ThinkingOrb, type OrbState } from "thinking-orbs";

import { ChatMarkdown } from "@/components/ai/chat-markdown";
import { ModelPicker } from "@/components/ai/model-picker";
import { ToolCard } from "@/components/ai/tool-card";
import type { ChatItem, RawTranscriptMessage, ToolItem } from "@/components/ai/transcript";
import { itemsFromTranscript, maxSeq, parseToolArgs } from "@/components/ai/transcript";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { AiModel } from "@/lib/api";
import { buildEmbedUrl } from "@/lib/embed";
import { cn } from "@/lib/utils";

/**
 * The AI builder chat + live preview. It talks to the Go API through the
 * dashboard's SSE proxy (`/api/ai/...`), which adds the session JWT. A turn is a
 * single streamed POST: token deltas render live as markdown, tool activity
 * shows as expandable cards, and a final draft_ready swaps the preview iframe
 * to the new draft URL.
 *
 * The stream is not the source of truth — the persisted transcript is. When the
 * stream drops mid-turn (flaky network, proxy timeout) the turn keeps running
 * server-side, so instead of failing the UI enters recovery: it polls the
 * session's status and incrementally replays the persisted transcript from the
 * events endpoint (Last-Event-ID = message seq) until the turn ends, then picks
 * up the draft from the session response. A page loaded while a turn is already
 * running enters the same recovery path.
 *
 * Copy note: no em or en dashes in user-facing text.
 */

interface DraftInfo {
  versionId: string;
  previewUrl: string;
  expiresAt: string;
  // The preview's access mode ("public", "org_only", …). A public preview renders
  // inline; a gated one must open in a new tab (its cross-site auth cookie is
  // blocked inside a cross-origin iframe, which otherwise loops on /authz).
  accessMode?: string;
}

interface BuilderChatProps {
  siteId: string;
  initialModel: string;
  models: AiModel[];
  onPublish: (versionId: string) => Promise<{ ok: boolean; message?: string }>;
  // The most recent session for this site and its persisted transcript (raw
  // message shapes), so the chat resumes where the user left off instead of
  // starting blank each visit.
  initialSessionId?: string | null;
  initialTranscript?: RawTranscriptMessage[];
  // The session's still-previewable draft, so a reload doesn't lose the preview.
  initialDraft?: DraftInfo | null;
  // The session's status at load time; "running" enters recovery immediately.
  initialStatus?: string | null;
}

// How long recovery keeps polling before giving up. The server bounds a turn at
// 10 minutes; this adds slack for the final draft build + clock skew.
const RECOVERY_DEADLINE_MS = 12 * 60 * 1000;
const RECOVERY_POLL_MS = 2500;

export function BuilderChat({
  siteId,
  initialModel,
  models,
  onPublish,
  initialSessionId = null,
  initialTranscript = [],
  initialDraft = null,
  initialStatus = null,
}: BuilderChatProps) {
  const [sessionId, setSessionId] = useState<string | null>(initialSessionId);
  const [model, setModel] = useState(initialModel);
  const [items, setItems] = useState<ChatItem[]>(() =>
    itemsFromTranscript(initialTranscript),
  );
  const [input, setInput] = useState("");
  const [running, setRunning] = useState(false);
  const [draft, setDraft] = useState<DraftInfo | null>(initialDraft);
  const [publishing, setPublishing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The sandbox tool the builder is running right now (null while it streams
  // prose). Drives which orb animation the activity line shows.
  const [activeTool, setActiveTool] = useState<string | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  // The persisted-transcript mirror recovery rebuilds from: every raw message
  // we have seen (initial hydration + events replays) and the highest seq, so a
  // replay resumes incrementally instead of re-downloading the conversation.
  const rawRef = useRef<RawTranscriptMessage[]>(initialTranscript);
  const lastSeqRef = useRef(maxSeq(initialTranscript));
  const liveToolSeq = useRef(0);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [items]);

  const pushStatus = useCallback((text: string) => {
    setItems((it) => [...it, { kind: "status", text }]);
  }, []);

  // Lazily create the session on the first send.
  const ensureSession = useCallback(async (): Promise<string> => {
    if (sessionId) return sessionId;
    const res = await fetch("/api/ai/sessions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ site_id: siteId, model }),
    });
    if (!res.ok) {
      const body = (await res.json().catch(() => ({}))) as { message?: string };
      throw new Error(body.message ?? "Could not start the AI builder.");
    }
    const body = (await res.json()) as { id: string };
    setSessionId(body.id);
    rawRef.current = [];
    lastSeqRef.current = 0;
    return body.id;
  }, [sessionId, siteId, model]);

  // syncTranscript replays the persisted transcript after our highest seen seq
  // (the events endpoint honors Last-Event-ID) into rawRef, and re-renders the
  // items from it. Incremental: a long conversation is never re-downloaded.
  const syncTranscript = useCallback(async (id: string): Promise<void> => {
    const res = await fetch(`/api/ai/sessions/${id}/events`, {
      headers: {
        Accept: "text/event-stream",
        "Last-Event-ID": String(lastSeqRef.current),
      },
    });
    if (!res.ok) throw new Error(`replay failed (${res.status})`);
    // The replay is finite (it ends after replay_done), so read it whole.
    const text = await res.text();
    for (const frame of text.split("\n\n")) {
      let seq = 0;
      let data = "";
      for (const line of frame.split("\n")) {
        if (line.startsWith("id:")) seq = Number(line.slice(3).trim()) || 0;
        else if (line.startsWith("data:")) data = line.slice(5).trim();
      }
      if (!data) continue;
      let ev: { type?: string; role?: string; content?: unknown };
      try {
        ev = JSON.parse(data);
      } catch {
        continue;
      }
      if (ev.type !== "message" || !ev.role || seq <= lastSeqRef.current) continue;
      rawRef.current = [...rawRef.current, { seq, role: ev.role, content: ev.content }];
      lastSeqRef.current = seq;
    }
    setItems(itemsFromTranscript(rawRef.current));
  }, []);

  // sessionStatus reads the session's current status from the (transcript-free)
  // list endpoint, so recovery polling stays cheap. A vanished session counts
  // as ended.
  const sessionStatus = useCallback(
    async (id: string): Promise<string> => {
      const res = await fetch(
        `/api/ai/sessions?site_id=${encodeURIComponent(siteId)}`,
      );
      if (!res.ok) throw new Error(`status check failed (${res.status})`);
      const body = (await res.json()) as {
        sessions?: { id: string; status: string }[];
      };
      return body.sessions?.find((s) => s.id === id)?.status ?? "active";
    },
    [siteId],
  );

  // fetchDraft resolves the session's current previewable draft from the session
  // endpoint (which derives the deterministic preview URL server-side). It is the
  // backstop for every path that can leave the preview panel empty after a turn
  // that DID produce a draft: a draft_ready whose preview_url came back blank
  // (preview-route write + fallback both failed), a dropped frame, or a reconnect
  // that never saw the live event. Best-effort: a failure just leaves the panel
  // as it was. Returns true when a draft was found and applied.
  const fetchDraft = useCallback(async (id: string): Promise<boolean> => {
    try {
      const res = await fetch(`/api/ai/sessions/${id}`);
      if (!res.ok) return false;
      const body = (await res.json()) as {
        draft?: {
          version_id: string;
          preview_url: string;
          expires_at?: string;
          access_mode?: string;
        };
      };
      if (!body.draft?.version_id || !body.draft.preview_url) return false;
      setDraft({
        versionId: body.draft.version_id,
        previewUrl: body.draft.preview_url,
        expiresAt: body.draft.expires_at ?? "",
        accessMode: body.draft.access_mode,
      });
      return true;
    } catch {
      return false;
    }
  }, []);

  // recoverTurn is the reconnect path: poll status + replay the transcript
  // until the turn ends, then pick the draft off the session response. Network
  // errors while polling are retried until the deadline (the connection may
  // still be down); only the deadline itself surfaces as an error.
  const recoverTurn = useCallback(
    async (id: string): Promise<void> => {
      // Recovery sees no live tool events, so drop whatever tool was mid-flight
      // when the stream died. The orb falls back to its generic working state
      // rather than claiming a command is still running for the whole reconnect.
      setActiveTool(null);
      pushStatus("Connection lost. Reconnecting...");
      const deadline = Date.now() + RECOVERY_DEADLINE_MS;
      for (;;) {
        await new Promise((r) => setTimeout(r, RECOVERY_POLL_MS));
        if (Date.now() > deadline) {
          setError(
            "Could not reconnect to this turn. Reload the page to see where it ended up.",
          );
          return;
        }
        let status: string;
        try {
          status = await sessionStatus(id);
          await syncTranscript(id);
        } catch {
          continue; // still offline; keep trying until the deadline
        }
        if (status === "running") continue;

        // Turn ended: one full session read for the draft (and final state).
        // The transcript is already synced; losing the draft lookup only means
        // the preview panel lags until the next turn or reload.
        await fetchDraft(id);
        pushStatus(
          status === "failed"
            ? "Reconnected. The turn ended with an error; see the transcript above."
            : "Reconnected. This turn finished while you were offline.",
        );
        return;
      }
    },
    [fetchDraft, pushStatus, sessionStatus, syncTranscript],
  );

  // A page opened while a turn is running (reload mid-build, second tab) joins
  // the running turn through the same recovery path. The ref guards React
  // strict-mode double-mount from starting two pollers.
  const joinedRunningTurn = useRef(false);
  useEffect(() => {
    if (initialStatus !== "running" || !initialSessionId) return;
    if (joinedRunningTurn.current) return;
    joinedRunningTurn.current = true;
    setRunning(true);
    void recoverTurn(initialSessionId).finally(() => setRunning(false));
  }, [initialStatus, initialSessionId, recoverTurn]);

  const send = useCallback(async () => {
    const text = input.trim();
    if (!text || running) return;
    setError(null);
    setInput("");
    setItems((it) => [...it, { kind: "user", text }]);
    setRunning(true);
    setActiveTool(null);

    try {
      const id = await ensureSession();
      const res = await fetch(`/api/ai/sessions/${id}/messages`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({ text }),
      });
      if (!res.ok || !res.body) {
        const body = (await res.json().catch(() => ({}))) as { message?: string };
        throw new Error(body.message ?? "The AI builder could not start.");
      }
      // The turn is now running server-side: from here on, a dropped stream is
      // recovered from the persisted transcript rather than surfaced as a
      // failure (the turn itself keeps going without us).
      let terminal = false;
      let sawDraft = false;
      try {
        terminal = await consumeStream(res.body, {
          onToken: (t) => setItems((it) => appendAssistant(it, t)),
          onStatus: pushStatus,
          onToolStarted: (tool, args) => {
            setActiveTool(tool);
            liveToolSeq.current += 1;
            setItems((it) => [
              ...it,
              {
                kind: "tool",
                callId: `live-${liveToolSeq.current}`,
                tool,
                args: parseToolArgs(args),
                result: null,
                done: false,
              },
            ]);
          },
          onToolFinished: (tool, result) => {
            setActiveTool(null);
            setItems((it) => finishTool(it, tool, result));
          },
          onDraft: (d) => {
            sawDraft = true;
            setDraft(d);
          },
          onError: (e) => setError(e),
        });
      } catch {
        terminal = false;
      }
      if (!terminal) {
        await recoverTurn(id);
      } else if (!sawDraft) {
        // The turn ended cleanly but no usable draft_ready reached us. If the
        // build produced a draft anyway, this recovers its preview URL so the
        // panel (and its open-in-a-new-tab button) still renders.
        await fetchDraft(id);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      setRunning(false);
      setActiveTool(null);
    }
  }, [input, running, ensureSession, fetchDraft, pushStatus, recoverTurn]);

  // Switching the model. The builder binds a model to a session for the session's
  // whole life (the message endpoint always runs on the session's model), so once
  // a conversation is underway a switch can't apply retroactively. Instead we start
  // a fresh session with the new model: clear the transcript + preview and drop the
  // session id so the next send creates a new one. The already-published live site
  // is untouched (only the unpublished preview is cleared).
  const handleModelChange = useCallback(
    (next: string) => {
      if (next === model || running) return;
      setModel(next);
      if (sessionId === null) return;
      setSessionId(null);
      setDraft(null);
      setError(null);
      rawRef.current = [];
      lastSeqRef.current = 0;
      const label = friendlyModelName(models.find((m) => m.id === next)?.name ?? next);
      setItems([
        {
          kind: "status",
          text: `Started a new conversation with ${label}. The new model applies to your next message.`,
        },
      ]);
    },
    [model, running, sessionId, models],
  );

  const publish = useCallback(async () => {
    if (!draft) return;
    setPublishing(true);
    setError(null);
    const result = await onPublish(draft.versionId);
    setPublishing(false);
    if (!result.ok) {
      setError(result.message ?? "Could not publish this version.");
      return;
    }
    // Publishing deletes the preview; clear the draft panel.
    setDraft(null);
    pushStatus("Published. This version is now live.");
  }, [draft, onPublish, pushStatus]);

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {/* Chat column */}
      <div className="flex h-[70vh] flex-col rounded-lg border bg-card">
        <div className="flex items-center justify-between gap-2 border-b px-4 py-3">
          <div className="flex items-center gap-2 text-sm font-medium">
            <Sparkles className="h-4 w-4 text-primary" />
            AI builder
          </div>
          <ModelPicker
            models={models}
            value={model}
            onChange={handleModelChange}
            disabled={running}
          />
        </div>

        <div ref={scrollRef} className="flex-1 space-y-3 overflow-y-auto p-4">
          {items.length === 0 && (
            <p className="text-sm text-muted-foreground">
              Describe the site you want, or the change you want to make. The
              builder will edit your files and give you a preview to review before
              you publish.
            </p>
          )}
          {items.map((item, i) => (
            <ChatEntry key={i} item={item} />
          ))}
          {running && <ActivityIndicator tool={activeTool} />}
        </div>

        {error && (
          <div className="border-t bg-destructive/10 px-4 py-2 text-xs text-destructive">
            {error}
          </div>
        )}

        <form
          className="flex items-center gap-2 border-t p-3"
          onSubmit={(e) => {
            e.preventDefault();
            void send();
          }}
        >
          <Input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Make the header dark and add a contact section"
            disabled={running}
          />
          <Button type="submit" size="icon" disabled={running || !input.trim()}>
            <Send className="h-4 w-4" />
          </Button>
        </form>

        {/* Usage note: the builder meters model usage and bills it after the fact,
            so the cost of a build isn't obvious mid-conversation. Keep it a quiet,
            always-visible line rather than a dismissable banner. */}
        <p className="flex items-center gap-1.5 border-t px-4 py-2 text-[0.7rem] leading-relaxed text-muted-foreground">
          <Info className="h-3 w-3 shrink-0" aria-hidden />
          AI builder usage is metered at cost, with no markup, and billed to your
          account at the end of your billing cycle.
        </p>
      </div>

      {/* Preview column */}
      <PreviewPanel draft={draft} publishing={publishing} onPublish={publish} />
    </div>
  );
}

// The device presets the preview can be sized to. Widths match common breakpoints;
// "desktop" fills the panel. Height always fills the panel (the iframe scrolls).
const PREVIEW_DEVICES = [
  { id: "desktop", label: "Desktop", icon: Monitor, width: "100%" },
  { id: "tablet", label: "Tablet", icon: Tablet, width: "768px" },
  { id: "mobile", label: "Mobile", icon: Smartphone, width: "375px" },
] as const;

type PreviewDeviceId = (typeof PREVIEW_DEVICES)[number]["id"];

/**
 * The preview column: a toolbar (device-size toggle, refresh, open-in-new-tab, and
 * the Publish action) over the draft iframe. A PUBLIC draft renders inline through
 * the EMBED surface (?embed=1): normal serving sends `frame-ancestors 'none'` +
 * X-Frame-Options: DENY, so a plain preview URL is silently blocked by the browser
 * inside the iframe — the embed surface is the one framable rendering. A GATED
 * draft can't render inline at all (embeds fail closed to a "sign in" placeholder;
 * in-frame cookie auth is blocked cross-origin), so it shows an open-in-new-tab
 * fallback. Empty state before the first draft. The device toggle restyles the
 * iframe width without reloading it; Refresh remounts the iframe (via a bumped
 * key) to reload the same URL.
 */
function PreviewPanel({
  draft,
  publishing,
  onPublish,
}: {
  draft: DraftInfo | null;
  publishing: boolean;
  onPublish: () => void | Promise<void>;
}) {
  const [device, setDevice] = useState<PreviewDeviceId>("desktop");
  const [reloadKey, setReloadKey] = useState(0);

  const isPublic = (draft?.accessMode ?? "public") === "public";
  const canInline = Boolean(draft) && isPublic;
  const selected = PREVIEW_DEVICES.find((d) => d.id === device) ?? PREVIEW_DEVICES[0];

  const openInNewTab = useCallback(() => {
    if (draft) window.open(draft.previewUrl, "_blank", "noopener,noreferrer");
  }, [draft]);

  return (
    <div className="flex h-[70vh] flex-col rounded-lg border bg-card">
      <div className="flex items-center justify-between gap-2 border-b px-3 py-2.5">
        <div className="flex items-center gap-2">
          <span className="pl-1 text-sm font-medium">Preview</span>
          {/* Device-size toggle: only meaningful for an inline (public) draft. */}
          {canInline && (
            <div
              role="group"
              aria-label="Preview size"
              className="flex items-center gap-0.5 rounded-md border bg-muted/40 p-0.5"
            >
              {PREVIEW_DEVICES.map((d) => {
                const Icon = d.icon;
                const active = d.id === device;
                return (
                  <button
                    key={d.id}
                    type="button"
                    onClick={() => setDevice(d.id)}
                    aria-pressed={active}
                    aria-label={d.label}
                    title={d.label}
                    className={cn(
                      "grid size-7 place-items-center rounded transition-colors",
                      "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                      active
                        ? "bg-background text-foreground shadow-sm"
                        : "text-muted-foreground hover:text-foreground",
                    )}
                  >
                    <Icon className="size-4" aria-hidden />
                  </button>
                );
              })}
            </div>
          )}
        </div>

        <div className="flex items-center gap-1.5">
          {draft && (
            <>
              {canInline && (
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="size-8"
                  onClick={() => setReloadKey((k) => k + 1)}
                  aria-label="Refresh preview"
                  title="Refresh preview"
                >
                  <RefreshCw className="size-4" aria-hidden />
                </Button>
              )}
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-8 gap-1.5"
                onClick={openInNewTab}
                aria-label="Open preview in a new tab"
                title="Open in a new tab"
              >
                <ExternalLink className="size-4" aria-hidden />
                <span className="hidden sm:inline">Open</span>
              </Button>
              <Button size="sm" onClick={() => void onPublish()} disabled={publishing}>
                {publishing ? "Publishing..." : "Publish this version"}
              </Button>
            </>
          )}
        </div>
      </div>

      <div className="flex-1 overflow-hidden">
        {!draft ? (
          <div className="flex h-full items-center justify-center p-6 text-center text-sm text-muted-foreground">
            Your preview appears here after the builder makes a change.
          </div>
        ) : canInline ? (
          <div className="flex h-full justify-center overflow-auto bg-muted/20">
            {/* Framable embed rendering of the draft. badge removal is requested
                unconditionally; the serving layer only honors it for entitled
                (Pro+) orgs, so this is a no-op elsewhere. */}
            <iframe
              key={reloadKey}
              title="Preview"
              src={buildEmbedUrl(draft.previewUrl, true)}
              style={{ width: selected.width }}
              className={cn(
                "h-full border-0 bg-white",
                // A constrained device sits as a centered "device" with a frame;
                // desktop fills the panel edge to edge.
                device !== "desktop" && "my-3 max-w-full rounded-md border shadow-sm",
              )}
            />
          </div>
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
            <p className="max-w-xs text-sm text-muted-foreground">
              This site is private. Private sites can&rsquo;t be embedded in the
              preview panel, so the preview opens in a new tab where you can sign
              in.
            </p>
            <Button size="sm" onClick={openInNewTab}>
              Open preview
            </Button>
          </div>
        )}
      </div>

      {draft && (
        <div className="border-t px-4 py-2 text-xs text-muted-foreground">
          {previewLifetimeCopy(draft.expiresAt)} Publishing removes it.
        </div>
      )}
    </div>
  );
}

// previewLifetimeCopy describes how long the preview link lives, derived from the
// server's expires_at (the TTL is operator-configurable, so a hardcoded "7 days"
// would be wrong whenever PREVIEW_TTL_HOURS is changed). Falls back to a generic
// line if the timestamp is missing or unparseable.
function previewLifetimeCopy(expiresAt: string): string {
  const ts = Date.parse(expiresAt);
  if (!expiresAt || Number.isNaN(ts)) {
    return "This preview link expires after a while.";
  }
  const remainingMs = ts - Date.now();
  // Already expired (clock skew or a very short TTL): don't promise a lifetime.
  if (remainingMs <= 0) {
    return "This preview link has expired. Re-run the change to get a fresh one.";
  }
  const days = Math.round(remainingMs / (24 * 60 * 60 * 1000));
  if (days >= 2) return `This preview link is live for about ${days} days.`;
  if (days === 1) return "This preview link is live for about a day.";
  const hours = Math.max(1, Math.round(remainingMs / (60 * 60 * 1000)));
  return `This preview link is live for about ${hours} hour${hours === 1 ? "" : "s"}.`;
}

// friendlyModelName strips the "Provider: " prefix off a catalog name so status
// copy reads "Claude Opus 4" rather than "Anthropic: Claude Opus 4". Falls back to
// the raw value (which may be a model id) when there's no prefix.
function friendlyModelName(name: string): string {
  const colon = name.indexOf(": ");
  return colon === -1 ? name : name.slice(colon + 2);
}

// Each sandbox tool gets its own orb animation and label, so the wait reads as
// "it is doing a specific thing" rather than one undifferentiated spinner. The
// null case covers the model streaming prose or thinking between tool calls.
const TOOL_ACTIVITY: Record<string, { state: OrbState; label: string }> = {
  read_file: { state: "searching", label: "Reading your files" },
  list_files: { state: "searching", label: "Looking through your files" },
  write_file: { state: "composing", label: "Writing your files" },
  run_command: { state: "solving", label: "Running a command" },
};

const DEFAULT_ACTIVITY = { state: "working", label: "Thinking" } as const;

/**
 * The mid-turn activity line: an animated thinking orb (thinking-orbs, a canvas
 * indicator that follows the dashboard's light/dark theme and respects
 * prefers-reduced-motion) plus a label describing the current step. The tool
 * cards below it carry the detail; this line is just "something is happening".
 */
function ActivityIndicator({ tool }: { tool: string | null }) {
  const { state, label } = (tool && TOOL_ACTIVITY[tool]) || DEFAULT_ACTIVITY;
  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <ThinkingOrb state={state} size={20} aria-label={label} />
      {label}
    </div>
  );
}

function ChatEntry({ item }: { item: ChatItem }) {
  switch (item.kind) {
    case "status":
      return <p className="text-xs italic text-muted-foreground">{item.text}</p>;
    case "tool":
      return <ToolCard item={item} />;
    case "user":
      return (
        <div className="flex justify-end">
          <div className="max-w-[85%] whitespace-pre-wrap rounded-lg bg-primary px-3 py-2 text-sm text-primary-foreground">
            {item.text}
          </div>
        </div>
      );
    case "assistant":
      return (
        <div className="flex justify-start">
          <div className="max-w-[85%] rounded-lg bg-muted px-3 py-2 text-sm">
            <ChatMarkdown text={item.text} />
          </div>
        </div>
      );
  }
}

// appendAssistant folds a token delta into the trailing assistant bubble,
// starting a new one when the previous item was not an assistant bubble (so
// text interleaved with tool cards renders in order, as separate bubbles).
function appendAssistant(items: ChatItem[], token: string): ChatItem[] {
  const last = items[items.length - 1];
  if (last && last.kind === "assistant") {
    const copy = items.slice();
    copy[copy.length - 1] = { kind: "assistant", text: last.text + token };
    return copy;
  }
  return [...items, { kind: "assistant", text: token }];
}

// finishTool attaches a result to the oldest still-running tool card for the
// tool. The loop dispatches a message's tool calls in order (started/finished
// pairs are emitted sequentially per call), so first-unfinished matches the
// call that actually finished even when one message carries several calls to
// the same tool.
function finishTool(items: ChatItem[], tool: string, result: string): ChatItem[] {
  const idx = items.findIndex(
    (it): it is ToolItem => it.kind === "tool" && it.tool === tool && !it.done,
  );
  if (idx === -1) return items;
  const copy = items.slice();
  copy[idx] = { ...(items[idx] as ToolItem), result: result || null, done: true };
  return copy;
}

// consumeStream parses the SSE frames of a builder turn and dispatches events.
// Returns true when the stream ended with a TERMINAL event (done or error); a
// false return means the connection dropped mid-turn and the caller should
// recover from the persisted transcript.
async function consumeStream(
  body: ReadableStream<Uint8Array>,
  handlers: {
    onToken: (t: string) => void;
    onStatus: (s: string) => void;
    onToolStarted: (tool: string, args?: string) => void;
    onToolFinished: (tool: string, result: string) => void;
    onDraft: (d: DraftInfo) => void;
    onError: (e: string) => void;
  },
): Promise<boolean> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let terminal = false;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const frames = buffer.split("\n\n");
    buffer = frames.pop() ?? "";
    for (const frame of frames) {
      const dataLine = frame
        .split("\n")
        .find((l) => l.startsWith("data:"));
      if (!dataLine) continue;
      const json = dataLine.slice("data:".length).trim();
      if (!json) continue;
      let ev: {
        type: string;
        text?: string;
        tool?: string;
        tool_args?: string;
        tool_result?: string;
        version_id?: string;
        preview_url?: string;
        expires_at?: string;
        access_mode?: string;
        error?: string;
      };
      try {
        ev = JSON.parse(json);
      } catch {
        continue;
      }
      switch (ev.type) {
        case "token":
          if (ev.text) handlers.onToken(ev.text);
          break;
        case "status":
          if (ev.text) handlers.onStatus(ev.text);
          break;
        case "tool_started":
          handlers.onToolStarted(ev.tool ?? "tool", ev.tool_args);
          break;
        case "tool_finished":
          handlers.onToolFinished(ev.tool ?? "tool", ev.tool_result ?? "");
          break;
        case "draft_ready":
          if (ev.preview_url && ev.version_id) {
            handlers.onDraft({
              versionId: ev.version_id,
              previewUrl: ev.preview_url,
              expiresAt: ev.expires_at ?? "",
              accessMode: ev.access_mode,
            });
          }
          break;
        case "error":
          handlers.onError(ev.error ?? "The builder hit an error.");
          terminal = true;
          break;
        case "done":
          terminal = true;
          break;
      }
    }
  }
  return terminal;
}
