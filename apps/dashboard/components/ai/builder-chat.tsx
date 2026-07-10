"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Loader2, Send, Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

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
}

interface BuilderChatProps {
  siteId: string;
  initialModel: string;
  models: { id: string; name?: string }[];
  onPublish: (versionId: string) => Promise<{ ok: boolean; message?: string }>;
}

export function BuilderChat({
  siteId,
  initialModel,
  models,
  onPublish,
}: BuilderChatProps) {
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [model, setModel] = useState(initialModel);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
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
          <select
            className="rounded-md border bg-background px-2 py-1 text-xs"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            disabled={running || sessionId !== null}
          >
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name ?? m.id}
              </option>
            ))}
          </select>
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
      </div>

      {/* Preview column */}
      <div className="flex h-[70vh] flex-col rounded-lg border bg-card">
        <div className="flex items-center justify-between gap-2 border-b px-4 py-3">
          <span className="text-sm font-medium">Preview</span>
          {draft && (
            <Button size="sm" onClick={() => void publish()} disabled={publishing}>
              {publishing ? "Publishing..." : "Publish this version"}
            </Button>
          )}
        </div>
        <div className="flex-1 overflow-hidden">
          {draft ? (
            <iframe
              title="Preview"
              src={draft.previewUrl}
              className="h-full w-full border-0"
            />
          ) : (
            <div className="flex h-full items-center justify-center p-6 text-center text-sm text-muted-foreground">
              Your preview appears here after the builder makes a change.
            </div>
          )}
        </div>
        {draft && (
          <div className="border-t px-4 py-2 text-xs text-muted-foreground">
            Preview link is live for 7 days. Publishing removes it.
          </div>
        )}
      </div>
    </div>
  );
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
        case "tool_started":
          handlers.onStatus(`Running ${ev.tool ?? "a tool"}...`);
          break;
        case "draft_ready":
          if (ev.preview_url && ev.version_id) {
            handlers.onDraft({
              versionId: ev.version_id,
              previewUrl: ev.preview_url,
              expiresAt: ev.expires_at ?? "",
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
