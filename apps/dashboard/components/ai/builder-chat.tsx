"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  ExternalLink,
  Info,
  Loader2,
  Monitor,
  RefreshCw,
  Send,
  Smartphone,
  Sparkles,
  Tablet,
} from "lucide-react";

import { ModelPicker } from "@/components/ai/model-picker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { AiModel } from "@/lib/api";
import { buildEmbedUrl } from "@/lib/embed";
import { cn } from "@/lib/utils";

/**
 * The AI builder chat + live preview. It talks to the Go API through the
 * dashboard's SSE proxy (`/api/ai/...`), which adds the session JWT. A turn is a
 * single streamed POST: token deltas render live, tool activity shows as status
 * lines, and a final draft_ready swaps the preview iframe to the new draft URL.
 *
 * Copy note: no em or en dashes in user-facing text.
 */

type ChatRole = "user" | "assistant" | "tool" | "status";

interface ChatMessage {
  role: ChatRole;
  text: string;
}

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
  // The most recent session for this site and its persisted transcript, so the
  // chat resumes where the user left off instead of starting blank each visit.
  initialSessionId?: string | null;
  initialMessages?: ChatMessage[];
}

export function BuilderChat({
  siteId,
  initialModel,
  models,
  onPublish,
  initialSessionId = null,
  initialMessages = [],
}: BuilderChatProps) {
  const [sessionId, setSessionId] = useState<string | null>(initialSessionId);
  const [model, setModel] = useState(initialModel);
  const [messages, setMessages] = useState<ChatMessage[]>(initialMessages);
  const [input, setInput] = useState("");
  const [running, setRunning] = useState(false);
  const [draft, setDraft] = useState<DraftInfo | null>(null);
  const [publishing, setPublishing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);


  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [messages]);

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
    return body.id;
  }, [sessionId, siteId, model]);

  const send = useCallback(async () => {
    const text = input.trim();
    if (!text || running) return;
    setError(null);
    setInput("");
    setMessages((m) => [...m, { role: "user", text }]);
    setRunning(true);

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
      await consumeStream(res.body, {
        onToken: (t) =>
          setMessages((m) => appendAssistant(m, t)),
        onStatus: (s) => setMessages((m) => [...m, { role: "status", text: s }]),
        onDraft: (d) => setDraft(d),
        onError: (e) => setError(e),
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      setRunning(false);
    }
  }, [input, running, ensureSession]);

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
      const label = friendlyModelName(models.find((m) => m.id === next)?.name ?? next);
      setMessages([
        {
          role: "status",
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
    setMessages((m) => [
      ...m,
      { role: "status", text: "Published. This version is now live." },
    ]);
  }, [draft, onPublish]);

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
          {messages.length === 0 && (
            <p className="text-sm text-muted-foreground">
              Describe the site you want, or the change you want to make. The
              builder will edit your files and give you a preview to review before
              you publish.
            </p>
          )}
          {messages.map((m, i) => (
            <MessageBubble key={i} message={m} />
          ))}
          {running && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" /> Working...
            </div>
          )}
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
          AI builder usage is metered and billed to your account at the end of
          your billing cycle.
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
                size="icon"
                className="size-8"
                onClick={openInNewTab}
                aria-label="Open preview in a new tab"
                title="Open in a new tab"
              >
                <ExternalLink className="size-4" aria-hidden />
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

function MessageBubble({ message }: { message: ChatMessage }) {
  if (message.role === "status") {
    return (
      <p className="text-xs italic text-muted-foreground">{message.text}</p>
    );
  }
  const isUser = message.role === "user";
  return (
    <div className={isUser ? "flex justify-end" : "flex justify-start"}>
      <div
        className={
          "max-w-[85%] whitespace-pre-wrap rounded-lg px-3 py-2 text-sm " +
          (isUser ? "bg-primary text-primary-foreground" : "bg-muted")
        }
      >
        {message.text}
      </div>
    </div>
  );
}

// appendAssistant folds a token delta into the trailing assistant bubble,
// starting a new one when the previous message was not the assistant.
function appendAssistant(messages: ChatMessage[], token: string): ChatMessage[] {
  const last = messages[messages.length - 1];
  if (last && last.role === "assistant") {
    const copy = messages.slice();
    copy[copy.length - 1] = { role: "assistant", text: last.text + token };
    return copy;
  }
  return [...messages, { role: "assistant", text: token }];
}

// consumeStream parses the SSE frames of a builder turn and dispatches events.
async function consumeStream(
  body: ReadableStream<Uint8Array>,
  handlers: {
    onToken: (t: string) => void;
    onStatus: (s: string) => void;
    onDraft: (d: DraftInfo) => void;
    onError: (e: string) => void;
  },
): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
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
          handlers.onStatus(`Running ${ev.tool ?? "a tool"}...`);
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
          break;
      }
    }
  }
}
