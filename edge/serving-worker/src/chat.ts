// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// "Share This Session" viewer surface — the edge side of the attached chat log.
//
// When a route carries a `chat_id` (contract v4, RouteValue.chat_id) the Worker:
//   1. injects a small fixed-position pill ("✨ How this was made") into every
//      served HTML page. The pill opens a right-side drawer (bottom sheet on
//      narrow screens) whose iframe loads the reserved same-host path
//      `/__dropway/chat`;
//   2. serves that reserved path as a self-contained transcript page, rendered
//      from the compiled transcript JSON the Go API writes to the content
//      bucket at `chat-transcripts/<org_id>/<chat_id>.json`.
//
// ACCESS: the transcript is exactly as gated as the site — index.ts serves the
// reserved path only inside the access-controlled serving paths (public arm, or
// after the gated authz passes), never in a pre-auth hook.
//
// SECURITY: the transcript body is untrusted (operator-authored chat text), so
// rendering is escape-first exactly like src/markdown.ts: every byte is
// HTML-escaped before the ONLY two transforms we apply (fenced ``` blocks →
// <pre><code>, `inline code` → <code>). No links, no images, no raw HTML — a
// `<script>` in a message renders as literal text. The page ships inline CSS
// only (no scripts, no external requests) and adapts to light/dark via
// prefers-color-scheme.
//
// The pill mirrors src/banner.ts's injection mechanics (insert right after the
// opening <body> tag via injectAfterBodyOpen); index.ts composes it with the
// attribution banner and recomputes Content-Length the same way. All of the
// pill's ids/classes are prefixed `dropway-chat-` to avoid colliding with
// tenant DOM, and its drawer/backdrop JS is inline (the tenant content CSP
// permits inline script/style).

import type { RouteValue } from "./route";
import { isHtml } from "./http";
import { injectAfterBodyOpen, isInjectableContentType } from "./banner";

// --- Reserved path + transcript object key ----------------------------------

/**
 * The reserved, cleaned (prefix-relative, as returned by cleanPath) request path
 * the transcript page is served under when the route has a chat_id.
 */
export const CHAT_RESERVED_PATH = "__dropway/chat";

/**
 * The R2 object key of a site's compiled transcript JSON. The Go API is the
 * sole writer; the object is MUTABLE (rewritten on every append/delete), which
 * is why index.ts serves the page with `Cache-Control: no-store`.
 */
export function chatTranscriptKey(orgId: string, chatId: string): string {
  return `chat-transcripts/${orgId}/${chatId}.json`;
}

// --- Transcript parsing -------------------------------------------------------

/** One rendered message of the compiled transcript. */
interface ChatMessage {
  seq: number;
  role: string;
  kind: string;
  content: string;
  action?: string;
  tool?: string;
  paths: string[];
  versionId: string;
}

/** The validated, renderer-ready shape of the compiled transcript. */
interface ChatTranscript {
  title: string;
  sourceTool: string;
  totalAppended: number;
  messages: ChatMessage[];
}

/**
 * Validate the untrusted transcript JSON into the renderer's shape. Returns
 * null on ANY structural mismatch so the caller falls back to the minimal
 * "conversation unavailable" page instead of throwing (the page must never
 * 500 on a corrupt/mid-rewrite object).
 */
function parseTranscript(input: unknown): ChatTranscript | null {
  if (typeof input !== "object" || input === null || Array.isArray(input)) return null;
  const obj = input as Record<string, unknown>;
  if (!Array.isArray(obj.messages)) return null;

  const messages: ChatMessage[] = [];
  for (const raw of obj.messages) {
    if (typeof raw !== "object" || raw === null || Array.isArray(raw)) return null;
    const m = raw as Record<string, unknown>;
    if (typeof m.seq !== "number" || !Number.isFinite(m.seq)) return null;
    if (typeof m.role !== "string" || typeof m.kind !== "string") return null;
    if (typeof m.content !== "string") return null;

    let action: string | undefined;
    let tool: string | undefined;
    let paths: string[] = [];
    if (m.meta !== undefined && m.meta !== null) {
      if (typeof m.meta !== "object" || Array.isArray(m.meta)) return null;
      const meta = m.meta as Record<string, unknown>;
      if (typeof meta.action === "string") action = meta.action;
      if (typeof meta.tool === "string") tool = meta.tool;
      if (Array.isArray(meta.paths)) {
        paths = meta.paths.filter((p): p is string => typeof p === "string");
      }
    }

    messages.push({
      seq: m.seq,
      role: m.role,
      kind: m.kind,
      content: m.content,
      action,
      tool,
      paths,
      versionId: typeof m.version_id === "string" ? m.version_id : "",
    });
  }

  const total =
    typeof obj.total_appended === "number" && Number.isFinite(obj.total_appended)
      ? obj.total_appended
      : messages.length;

  return {
    title: typeof obj.title === "string" && obj.title !== "" ? obj.title : "Shared session",
    sourceTool: typeof obj.source_tool === "string" ? obj.source_tool : "",
    totalAppended: total,
    messages,
  };
}

// --- Escape-first content rendering ------------------------------------------

/** Escape the five HTML-significant characters (same table as markdown.ts). */
function esc(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

const FENCE = /^\s*```/;

/**
 * Render one message's content: escape EVERYTHING, then apply exactly two
 * transforms — fenced ``` blocks become <pre><code> and `inline code` becomes
 * <code>. Nothing else (no links/images/emphasis/raw HTML); text blocks keep
 * their line breaks via `white-space: pre-wrap` on the paragraph class.
 * Deliberately minimal + XSS-safe, mirroring src/markdown.ts's approach.
 */
export function renderChatContent(src: string): string {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: string[] = [];
  let text: string[] = [];

  const flushText = (): void => {
    const joined = text.join("\n");
    text = [];
    if (joined.trim() === "") return;
    // Escape FIRST; the inline-code swap then runs over already-escaped text,
    // so the <code> contents can never carry live markup.
    const escaped = esc(joined).replace(/`([^`\n]+)`/g, "<code>$1</code>");
    out.push(`<p class="txt">${escaped}</p>`);
  };

  let i = 0;
  while (i < lines.length) {
    if (FENCE.test(lines[i] ?? "")) {
      flushText();
      const code: string[] = [];
      i++;
      while (i < lines.length && !FENCE.test(lines[i] ?? "")) {
        code.push(lines[i] ?? "");
        i++;
      }
      if (i < lines.length) i++; // consume the closing fence
      out.push(`<pre><code>${esc(code.join("\n"))}</code></pre>`);
      continue;
    }
    text.push(lines[i] ?? "");
    i++;
  }
  flushText();
  return out.join("");
}

// --- Page rendering -----------------------------------------------------------

/** Human label for the transcript's source_tool badge. */
function sourceToolLabel(tool: string): string {
  switch (tool) {
    case "claude_code":
      return "Claude Code";
    case "chatgpt":
      return "ChatGPT";
    case "cursor":
      return "Cursor";
    case "other":
    case "":
      return "AI session";
    default:
      // A tool string this build doesn't know — show it escaped rather than hide it.
      return esc(tool);
  }
}

/** The header's message-count line ("N messages" / "showing the last N of M"). */
function countLine(shown: number, total: number): string {
  if (shown < total) {
    return `showing the last ${shown} of ${total}`;
  }
  return shown === 1 ? "1 message" : `${shown} messages`;
}

/** One kind:"action" row — a compact, full-width activity line. */
function renderActionRow(m: ChatMessage): string {
  let icon: string;
  let what: string;
  if (m.action === "file_edit") {
    icon = "✎";
    what = m.paths.map((p) => `<span class="chip">${esc(p)}</span>`).join("");
  } else {
    icon = "⚙";
    what = m.tool ? `<span class="chip">${esc(m.tool)}</span>` : "";
  }
  const commentary = renderChatContent(m.content);
  return (
    `<div class="action" id="msg-${m.seq}">` +
    `<span class="icon" aria-hidden="true">${icon}</span>` +
    `<div class="act-body">${what}${commentary}</div>` +
    `</div>`
  );
}

/** One kind:"chat" bubble — user right/accent, assistant left/neutral. */
function renderChatBubble(m: ChatMessage): string {
  const side = m.role === "user" ? "user" : "assistant";
  return (
    `<div class="msg ${side}" id="msg-${m.seq}">` +
    `<div class="bubble">${renderChatContent(m.content)}</div>` +
    `</div>`
  );
}

/** The divider inserted where a message carries a NEW non-empty version_id. */
const VERSION_DIVIDER =
  '<div class="divider" role="separator"><span>↑ deployed as new version</span></div>';

/** The minimal fallback page for a missing/corrupt transcript object. */
const UNAVAILABLE_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Conversation unavailable</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 1.4rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>Conversation unavailable</h1>
    <p>The session transcript for this site can't be shown right now.</p>
  </main>
</body>
</html>
`;

const PAGE_CSS = `
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body { font: 15px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
       margin: 0; background: #fafafa; color: #1a1a1a; }
header { position: sticky; top: 0; z-index: 10; display: flex; flex-wrap: wrap; align-items: baseline;
         gap: 0.4rem 0.6rem; padding: 0.7rem 1.1rem; background: rgba(250,250,250,0.9);
         backdrop-filter: saturate(1.8) blur(8px); border-bottom: 1px solid #e3e3e3; }
header h1 { font-size: 1rem; margin: 0; font-weight: 700; word-break: break-word; }
header .badge { font-size: 0.72rem; font-weight: 600; padding: 0.15rem 0.5rem; border-radius: 999px;
                background: #eef2ff; color: #4f46e5; white-space: nowrap; }
header .count { font-size: 0.78rem; color: #6b7280; margin-left: auto; white-space: nowrap; }
main { max-width: 760px; margin: 0 auto; padding: 1.25rem 1rem 3rem; }
.msg { display: flex; margin: 0.6rem 0; }
.msg.user { justify-content: flex-end; }
.msg.assistant { justify-content: flex-start; }
.bubble { max-width: 85%; padding: 0.55rem 0.85rem; border-radius: 1rem; overflow-wrap: anywhere; }
.msg.user .bubble { background: #4f46e5; color: #fff; border-bottom-right-radius: 0.3rem; }
.msg.assistant .bubble { background: #ececf0; color: #1a1a1a; border-bottom-left-radius: 0.3rem; }
.txt { margin: 0.35rem 0; white-space: pre-wrap; }
.txt:first-child { margin-top: 0; } .txt:last-child { margin-bottom: 0; }
pre { background: rgba(0,0,0,0.28); color: #f4f4f5; padding: 0.6rem 0.8rem; border-radius: 0.5rem;
      overflow-x: auto; margin: 0.4rem 0; }
.msg.assistant pre, .action pre { background: #24262b; }
pre code { background: none; padding: 0; color: inherit; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.88em;
       background: rgba(135,131,120,0.2); padding: 0.1em 0.3em; border-radius: 0.3rem; }
.msg.user code { background: rgba(255,255,255,0.22); }
.action { display: flex; gap: 0.55rem; align-items: baseline; width: 100%; margin: 0.45rem 0;
          padding: 0.45rem 0.7rem; border: 1px dashed #d6d6dc; border-radius: 0.6rem;
          background: #f4f4f6; font-size: 0.85rem; color: #4b5563; }
.action .icon { flex: none; }
.action .act-body { min-width: 0; overflow-wrap: anywhere; }
.chip { display: inline-block; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        font-size: 0.75rem; background: #e5e7eb; color: #374151; padding: 0.1rem 0.45rem;
        border-radius: 0.35rem; margin: 0 0.3rem 0.2rem 0; }
.divider { display: flex; align-items: center; gap: 0.6rem; margin: 1rem 0; color: #6b7280;
           font-size: 0.78rem; }
.divider::before, .divider::after { content: ""; flex: 1; border-top: 1px solid #d6d6dc; }
footer { text-align: center; padding: 1rem 0 2rem; font-size: 0.8rem; color: #6b7280; }
footer a { color: #4f46e5; font-weight: 600; text-decoration: none; }
.empty { text-align: center; color: #6b7280; padding: 2rem 0; }
@media (prefers-color-scheme: dark) {
  body { background: #16181c; color: #e6e6e6; }
  header { background: rgba(22,24,28,0.9); border-bottom-color: #2a2d34; }
  header .badge { background: #272a4a; color: #a5b4fc; }
  header .count { color: #9aa0a8; }
  .msg.assistant .bubble { background: #23262c; color: #e6e6e6; }
  .action { background: #1d1f24; border-color: #33363d; color: #9aa0a8; }
  .chip { background: #2a2d34; color: #c3c7ce; }
  .divider { color: #9aa0a8; } .divider::before, .divider::after { border-top-color: #33363d; }
  footer { color: #9aa0a8; } footer a { color: #a5b4fc; }
  code { background: rgba(110,118,129,0.3); }
}
`;

/**
 * Render the self-contained transcript page. `transcript` is the untrusted,
 * parsed JSON of the compiled transcript object; any structural mismatch (or a
 * null for a missing object) yields the minimal "conversation unavailable"
 * page — this function NEVER throws. `host` is the serving content host (used
 * for the page title); `planTier` is RouteValue.plan_tier — when it normalizes
 * to "free" the footer carries the "Shared via Dropway" attribution link.
 */
export function renderChatPage(
  transcript: unknown,
  host: string,
  planTier?: string,
): string {
  const t = parseTranscript(transcript);
  if (t === null) return UNAVAILABLE_HTML;

  const parts: string[] = [];
  let prevVersion = "";
  let first = true;
  for (const m of t.messages) {
    // Version divider: the version pointer moved between the previous message
    // and this one (and the new value is non-empty) → the work above shipped.
    if (!first && m.versionId !== "" && m.versionId !== prevVersion) {
      parts.push(VERSION_DIVIDER);
    }
    if (m.versionId !== "") prevVersion = m.versionId;
    else if (first) prevVersion = "";
    parts.push(m.kind === "action" ? renderActionRow(m) : renderChatBubble(m));
    first = false;
  }
  const body =
    parts.length === 0 ? '<p class="empty">No messages in this session yet.</p>' : parts.join("\n");

  const isFree = (planTier ?? "").trim().toLowerCase() === "free";
  const footer = isFree
    ? '<footer>Shared via <a href="https://dropway.dev" target="_blank" rel="noopener noreferrer">Dropway</a></footer>'
    : "";

  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(t.title)} — ${esc(host)}</title>
<style>${PAGE_CSS}</style>
</head>
<body>
<header>
<h1>${esc(t.title)}</h1>
<span class="badge">${sourceToolLabel(t.sourceTool)}</span>
<span class="count">${countLine(t.messages.length, t.totalAppended)}</span>
</header>
<main>
${body}
</main>
${footer}
</body>
</html>
`;
}

// --- The "How this was made" pill ---------------------------------------------

/**
 * The injected pill markup: a fixed bottom-right button that opens a right-side
 * drawer (bottom sheet under 640px) whose iframe lazily loads the same-host
 * `/__dropway/chat` transcript page. Esc and a backdrop click close it;
 * prefers-reduced-motion disables the slide transition. All CSS/JS is inline
 * (no external requests) and every id/class is `dropway-chat-`-prefixed so it
 * can't collide with tenant DOM. The iframe src is set on FIRST open so pages
 * don't pay for the transcript fetch unless the viewer asks for it.
 */
export const PILL_MARKUP =
  "<style>" +
  "#dropway-chat-pill{position:fixed;bottom:16px;right:16px;z-index:2147483645;display:inline-flex;align-items:center;gap:7px;padding:10px 15px;border:0;border-radius:999px;background:#4f46e5;color:#fff;font:600 13px/1 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;cursor:pointer;box-shadow:0 4px 14px rgba(0,0,0,.28);}" +
  "#dropway-chat-pill:hover{background:#4338ca;}" +
  "#dropway-chat-backdrop{position:fixed;inset:0;z-index:2147483646;background:rgba(0,0,0,.45);opacity:0;transition:opacity .2s ease;}" +
  "#dropway-chat-backdrop.dropway-chat-open{opacity:1;}" +
  "#dropway-chat-drawer{position:fixed;top:0;right:0;bottom:0;z-index:2147483647;width:min(420px,100vw);display:flex;flex-direction:column;background:#fff;box-shadow:-8px 0 28px rgba(0,0,0,.3);transform:translateX(100%);transition:transform .25s ease;}" +
  "#dropway-chat-drawer.dropway-chat-open{transform:translateX(0);}" +
  "#dropway-chat-bar{display:flex;align-items:center;justify-content:space-between;padding:10px 14px;border-bottom:1px solid #e5e7eb;font:600 14px/1.3 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1f2937;}" +
  "#dropway-chat-close{border:0;background:none;font-size:20px;line-height:1;color:#6b7280;cursor:pointer;padding:2px 6px;}" +
  "#dropway-chat-frame{flex:1;width:100%;border:0;}" +
  "@media (max-width:640px){#dropway-chat-drawer{top:auto;left:0;right:0;width:100%;height:80vh;border-radius:16px 16px 0 0;transform:translateY(100%);}#dropway-chat-drawer.dropway-chat-open{transform:translateY(0);}}" +
  "@media (prefers-reduced-motion:reduce){#dropway-chat-drawer,#dropway-chat-backdrop{transition:none;}}" +
  "@media (prefers-color-scheme:dark){#dropway-chat-drawer{background:#16181c;}#dropway-chat-bar{border-bottom-color:#2a2d34;color:#e6e6e6;}}" +
  "</style>" +
  '<div id="dropway-chat-backdrop" hidden></div>' +
  '<div id="dropway-chat-drawer" role="dialog" aria-modal="true" aria-label="How this site was made" hidden>' +
  '<div id="dropway-chat-bar"><span>How this was made</span>' +
  '<button type="button" id="dropway-chat-close" aria-label="Close">&times;</button></div>' +
  '<iframe id="dropway-chat-frame" title="Session transcript"></iframe>' +
  "</div>" +
  '<button type="button" id="dropway-chat-pill" aria-haspopup="dialog" aria-controls="dropway-chat-drawer">✨ How this was made</button>' +
  "<script>(function(){" +
  "var pill=document.getElementById('dropway-chat-pill');" +
  "var drawer=document.getElementById('dropway-chat-drawer');" +
  "var backdrop=document.getElementById('dropway-chat-backdrop');" +
  "var frame=document.getElementById('dropway-chat-frame');" +
  "var close=document.getElementById('dropway-chat-close');" +
  "if(!pill||!drawer||!backdrop||!frame||!close)return;" +
  "function open(){" +
  "if(!frame.getAttribute('src'))frame.setAttribute('src','/__dropway/chat');" +
  "backdrop.hidden=false;drawer.hidden=false;" +
  "requestAnimationFrame(function(){requestAnimationFrame(function(){" +
  "drawer.classList.add('dropway-chat-open');backdrop.classList.add('dropway-chat-open');});});}" +
  "function shut(){" +
  "drawer.classList.remove('dropway-chat-open');backdrop.classList.remove('dropway-chat-open');" +
  "drawer.hidden=true;backdrop.hidden=true;pill.focus();}" +
  "pill.addEventListener('click',open);" +
  "close.addEventListener('click',shut);" +
  "backdrop.addEventListener('click',shut);" +
  "document.addEventListener('keydown',function(e){if(e.key==='Escape'&&!drawer.hidden)shut();});" +
  "})();</script>";

/**
 * UTF-8 byte length of the pill markup. Like BANNER_BYTE_LENGTH: injectChatPill
 * only ever INSERTS this markup and the inject path is UTF-8-only, so a HEAD's
 * Content-Length is derived arithmetically without buffering. The markup is NOT
 * pure ASCII ("✨"/"↑"-free but the pill text has "✨"), so encoding is required.
 */
export const CHAT_PILL_BYTE_LENGTH = new TextEncoder().encode(PILL_MARKUP).length;

/**
 * Insert the pill immediately after the opening <body> tag (prepending when the
 * document has none) — identical mechanics to injectBanner, sharing its
 * comment-skipping, quote-aware <body> scan.
 */
export function injectChatPill(html: string): string {
  return injectAfterBodyOpen(html, PILL_MARKUP);
}

/**
 * Whether to inject the chat pill for this response: the route carries a
 * chat_id AND the served document is HTML AND the body is safely re-encodable
 * (UTF-8/ASCII/absent charset — same rule as the banner). Unlike the banner
 * there is no env flag and no tier gate: the pill rides purely on the site
 * owner having attached + panel-enabled a chat log (projected as chat_id).
 */
export function shouldInjectChatPill(
  route: RouteValue,
  servedPath: string,
  contentType: string | undefined,
): boolean {
  return (
    typeof route.chat_id === "string" &&
    route.chat_id !== "" &&
    isHtml(servedPath) &&
    isInjectableContentType(contentType)
  );
}
