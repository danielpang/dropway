/**
 * Pure transforms from the AI builder's persisted transcript (raw OpenRouter
 * message shapes in `content`) to the chat items the UI renders. Shared by the
 * server page (initial hydration) and the client (reconnect replay), so a
 * reloaded conversation and a live one produce the same rendering — including
 * tool activity, which the old hydration path dropped entirely.
 */

/** One chat entry the builder UI renders. */
export type ChatItem =
  | { kind: "user"; text: string }
  | { kind: "assistant"; text: string }
  | { kind: "status"; text: string }
  | ToolItem;

/** One tool call's lifecycle: running (no result yet) → done. */
export interface ToolItem {
  kind: "tool";
  /** tool_call_id from the transcript, or a synthetic id for live events. */
  callId: string;
  tool: string;
  /** Parsed arguments, or null when they can't be parsed (e.g. truncated). */
  args: Record<string, unknown> | null;
  result: string | null;
  done: boolean;
}

/**
 * A raw transcript entry as the API returns it (GET session / events replay):
 * the persisted OpenRouter message in `content`, plus its seq when present.
 */
export interface RawTranscriptMessage {
  seq?: number;
  role: string;
  content: unknown;
}

interface RawToolCall {
  id?: string;
  function?: { name?: string; arguments?: string };
}

// contentText extracts the text of an OpenRouter message content field, which
// is a plain string in our transcripts but may be a content-part array for
// other providers' shapes. Anything else renders as empty.
function contentText(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((p) =>
        p && typeof p === "object" && typeof (p as { text?: unknown }).text === "string"
          ? (p as { text: string }).text
          : "",
      )
      .join("");
  }
  return "";
}

/** parseToolArgs parses a tool call's JSON arguments, null when unparseable. */
export function parseToolArgs(raw: string | undefined): Record<string, unknown> | null {
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    // Truncated or malformed arguments: the card falls back to the tool name.
  }
  return null;
}

/**
 * itemsFromTranscript rebuilds the chat item list from persisted transcript
 * messages: user/assistant text becomes bubbles, assistant tool_calls become
 * tool cards, and tool results attach to their card by tool_call_id. System
 * messages and unknown roles are skipped.
 */
export function itemsFromTranscript(messages: RawTranscriptMessage[]): ChatItem[] {
  const items: ChatItem[] = [];
  const toolByCallId = new Map<string, ToolItem>();

  for (const m of messages) {
    const c = (m.content ?? {}) as Record<string, unknown>;
    switch (m.role) {
      case "user": {
        const text = contentText(c.content);
        if (text) items.push({ kind: "user", text });
        break;
      }
      case "assistant": {
        const text = contentText(c.content);
        if (text) items.push({ kind: "assistant", text });
        const calls = Array.isArray(c.tool_calls) ? (c.tool_calls as RawToolCall[]) : [];
        for (const call of calls) {
          const item: ToolItem = {
            kind: "tool",
            callId: call.id ?? `call-${items.length}`,
            tool: call.function?.name ?? "tool",
            args: parseToolArgs(call.function?.arguments),
            result: null,
            done: false,
          };
          items.push(item);
          toolByCallId.set(item.callId, item);
        }
        break;
      }
      case "tool": {
        const callId = typeof c.tool_call_id === "string" ? c.tool_call_id : "";
        const item = toolByCallId.get(callId);
        if (item) {
          item.result = contentText(c.content) || null;
          item.done = true;
        }
        break;
      }
      default:
        break; // system + anything unknown: not rendered
    }
  }
  return items;
}

/** maxSeq returns the highest seq in a raw transcript (0 when none carry one). */
export function maxSeq(messages: RawTranscriptMessage[]): number {
  let max = 0;
  for (const m of messages) {
    if (typeof m.seq === "number" && m.seq > max) max = m.seq;
  }
  return max;
}

/**
 * toolSummary is the one-line description a tool card shows next to its label:
 * the command being run, the file being written/read, or the directory being
 * listed. Empty when the args don't carry it (unparseable/foreign tool).
 */
export function toolSummary(tool: string, args: Record<string, unknown> | null): string {
  const str = (k: string): string =>
    args && typeof args[k] === "string" ? (args[k] as string) : "";
  switch (tool) {
    case "run_command":
      return str("command");
    case "write_file":
    case "read_file":
      return str("path");
    case "list_files":
      return str("dir") || "site root";
    default:
      return "";
  }
}

/** toolLabel is the human verb for a tool card, by lifecycle state. */
export function toolLabel(tool: string, done: boolean): string {
  switch (tool) {
    case "run_command":
      return done ? "Ran command" : "Running command";
    case "write_file":
      return done ? "Wrote file" : "Writing file";
    case "read_file":
      return done ? "Read file" : "Reading file";
    case "list_files":
      return done ? "Listed files" : "Listing files";
    default:
      return done ? `Ran ${tool}` : `Running ${tool}`;
  }
}
