// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the pure "Share This Session" helpers (no edge/KV/R2): the
// transcript-page renderer, its escape-first content rendering, and the chat
// pill injection/predicate. The end-to-end serve() behavior (reserved path,
// gating, caching, pill injection) is covered in serve.test.ts.

import { describe, expect, it } from "vitest";

import {
  CHAT_PILL_BYTE_LENGTH,
  CHAT_RESERVED_PATH,
  PILL_MARKUP,
  chatTranscriptKey,
  injectChatPill,
  renderChatContent,
  renderChatPage,
  shouldInjectChatPill,
} from "../src/chat";
import type { RouteValue } from "../src/route";

const ORG_ID = "11111111-1111-1111-1111-111111111111";
const CHAT_ID = "55555555-5555-5555-5555-555555555555";
const HOST = "acme.dropwaycontent.com";

const CHAT_ROUTE: RouteValue = {
  org_id: ORG_ID,
  site_id: "22222222-2222-2222-2222-222222222222",
  version_id: "33333333-3333-3333-3333-333333333333",
  access_mode: "public",
  schema_version: 4,
  chat_id: CHAT_ID,
};

/** A well-formed transcript object (already-parsed JSON, as index.ts passes it). */
function transcript(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    chat_id: CHAT_ID,
    title: "Build my landing page",
    source_tool: "claude_code",
    total_appended: 2,
    messages: [
      { seq: 1, role: "user", kind: "chat", content: "Make me a landing page" },
      { seq: 2, role: "assistant", kind: "chat", content: "Done!" },
    ],
    ...overrides,
  };
}

// --- reserved path + object key ----------------------------------------------

describe("chat constants", () => {
  it("builds the per-org transcript key", () => {
    expect(chatTranscriptKey(ORG_ID, CHAT_ID)).toBe(
      `chat-transcripts/${ORG_ID}/${CHAT_ID}.json`,
    );
  });

  it("reserves the cleaned __dropway/chat path", () => {
    expect(CHAT_RESERVED_PATH).toBe("__dropway/chat");
  });
});

// --- renderChatContent (escape-first) ------------------------------------------

describe("renderChatContent", () => {
  it("escapes ALL HTML — a <script> in a message renders as literal text", () => {
    const out = renderChatContent('<script>alert("xss")</script>');
    expect(out).not.toContain("<script>");
    expect(out).toContain("&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;");
  });

  it("renders fenced ``` blocks as <pre><code> with escaped contents", () => {
    const out = renderChatContent('before\n```\n<b>code</b> & stuff\n```\nafter');
    expect(out).toContain("<pre><code>&lt;b&gt;code&lt;/b&gt; &amp; stuff</code></pre>");
    expect(out).toContain("before");
    expect(out).toContain("after");
    expect(out).not.toContain("<b>");
  });

  it("renders `inline code` as <code> with escaped contents", () => {
    const out = renderChatContent("run `npm <install>` now");
    expect(out).toContain("<code>npm &lt;install&gt;</code>");
  });

  it("applies NO other markdown (links, images, emphasis stay literal)", () => {
    const out = renderChatContent("**bold** [x](https://evil) ![y](z)");
    expect(out).toContain("**bold**");
    expect(out).not.toContain("<a ");
    expect(out).not.toContain("<img");
    expect(out).not.toContain("<strong>");
  });

  it("an unclosed fence still escapes its contents", () => {
    const out = renderChatContent("```\n<script>x</script>");
    expect(out).toContain("<pre><code>&lt;script&gt;x&lt;/script&gt;</code></pre>");
  });
});

// --- renderChatPage -------------------------------------------------------------

describe("renderChatPage", () => {
  it("renders header (title + source badge + count), bubbles, and msg anchors", () => {
    const html = renderChatPage(transcript(), HOST);
    expect(html).toContain("Build my landing page");
    expect(html).toContain("Claude Code");
    expect(html).toContain("2 messages");
    expect(html).toContain('id="msg-1"');
    expect(html).toContain('id="msg-2"');
    // user right/accent vs assistant left/neutral bubbles.
    expect(html).toContain('class="msg user"');
    expect(html).toContain('class="msg assistant"');
    // Self-contained: no external requests (the footer's dropway.dev link is the
    // only allowed href, and only on the free tier — absent here).
    expect(html).not.toContain("src=");
    expect(html).not.toContain("<script");
  });

  it("shows 'showing the last N of M' when messages were trimmed", () => {
    const html = renderChatPage(transcript({ total_appended: 40 }), HOST);
    expect(html).toContain("showing the last 2 of 40");
    expect(html).not.toContain("2 messages");
  });

  it("escapes HTML in the title and message content", () => {
    const t = transcript({
      title: '<img src=x onerror=alert(1)>',
      messages: [
        { seq: 1, role: "user", kind: "chat", content: '<script>alert("pwn")</script>' },
      ],
    });
    const html = renderChatPage(t, HOST);
    expect(html).not.toContain("<img src=x");
    expect(html).not.toContain('<script>alert("pwn")</script>');
    expect(html).toContain("&lt;script&gt;");
  });

  it("renders kind:'action' rows — ✎ file_edit with path chips", () => {
    const t = transcript({
      messages: [
        {
          seq: 1,
          role: "assistant",
          kind: "action",
          content: "Tweaked the hero section",
          meta: { action: "file_edit", paths: ["index.html", "css/site.css"] },
        },
      ],
    });
    const html = renderChatPage(t, HOST);
    expect(html).toContain("✎");
    expect(html).toContain('<span class="chip">index.html</span>');
    expect(html).toContain('<span class="chip">css/site.css</span>');
    expect(html).toContain("Tweaked the hero section");
    expect(html).toContain('class="action"');
  });

  it("renders kind:'action' rows — ⚙ tool_use with the tool name", () => {
    const t = transcript({
      messages: [
        {
          seq: 1,
          role: "assistant",
          kind: "action",
          content: "Searched the docs",
          meta: { action: "tool_use", tool: "WebSearch" },
        },
      ],
    });
    const html = renderChatPage(t, HOST);
    expect(html).toContain("⚙");
    expect(html).toContain('<span class="chip">WebSearch</span>');
    expect(html).toContain("Searched the docs");
  });

  it("inserts a version divider where a later message carries a NEW version_id", () => {
    const t = transcript({
      total_appended: 3,
      messages: [
        { seq: 1, role: "user", kind: "chat", content: "a", version_id: "v1" },
        { seq: 2, role: "assistant", kind: "chat", content: "b", version_id: "v1" },
        { seq: 3, role: "assistant", kind: "chat", content: "c", version_id: "v2" },
      ],
    });
    const html = renderChatPage(t, HOST);
    expect(html).toContain("↑ deployed as new version");
    // Exactly ONE divider (v1→v1 is no change; only v1→v2 divides).
    expect(html.split("↑ deployed as new version").length - 1).toBe(1);
    // It sits between msg-2 and msg-3.
    expect(html.indexOf('id="msg-2"')).toBeLessThan(html.indexOf("deployed as new version"));
    expect(html.indexOf("deployed as new version")).toBeLessThan(html.indexOf('id="msg-3"'));
  });

  it("does NOT divide when the later message's version_id is empty/absent", () => {
    const t = transcript({
      messages: [
        { seq: 1, role: "user", kind: "chat", content: "a", version_id: "v1" },
        { seq: 2, role: "assistant", kind: "chat", content: "b" },
      ],
    });
    expect(renderChatPage(t, HOST)).not.toContain("deployed as new version");
  });

  it("shows the 'Shared via Dropway' footer ONLY on the free tier", () => {
    const free = renderChatPage(transcript(), HOST, "free");
    expect(free).toContain("Shared via");
    expect(free).toContain('href="https://dropway.dev"');

    const pro = renderChatPage(transcript(), HOST, "pro");
    expect(pro).not.toContain("Shared via");
    const unknown = renderChatPage(transcript(), HOST, undefined);
    expect(unknown).not.toContain("Shared via");
  });

  it("normalizes the tier for the footer ('FREE', ' free ')", () => {
    for (const tier of ["FREE", " free ", "Free"]) {
      expect(renderChatPage(transcript(), HOST, tier)).toContain("Shared via");
    }
  });

  it("renders the minimal 'conversation unavailable' page for malformed input (never throws)", () => {
    for (const bad of [
      null,
      undefined,
      "nope",
      [],
      {},
      { messages: "not-an-array" },
      { messages: [{ seq: "1", role: "user", kind: "chat", content: "x" }] },
      { messages: [{ seq: 1, role: "user", kind: "chat", content: 7 }] },
      { messages: [{ seq: 1, role: "user", kind: "chat", content: "x", meta: "bad" }] },
    ]) {
      const html = renderChatPage(bad, HOST);
      expect(html).toContain("Conversation unavailable");
    }
  });

  it("renders an empty-but-valid transcript as an empty state, not 'unavailable'", () => {
    const html = renderChatPage(transcript({ messages: [], total_appended: 0 }), HOST);
    expect(html).not.toContain("Conversation unavailable");
    expect(html).toContain("No messages in this session yet");
  });
});

// --- pill markup + injection -----------------------------------------------------

describe("PILL_MARKUP / injectChatPill", () => {
  it("is self-contained, dropway-chat-prefixed, and targets /__dropway/chat", () => {
    expect(PILL_MARKUP).toContain('id="dropway-chat-pill"');
    expect(PILL_MARKUP).toContain('id="dropway-chat-drawer"');
    expect(PILL_MARKUP).toContain('id="dropway-chat-backdrop"');
    expect(PILL_MARKUP).toContain("'/__dropway/chat'");
    expect(PILL_MARKUP).toContain("✨ How this was made");
    // Esc + reduced-motion affordances are wired in.
    expect(PILL_MARKUP).toContain("Escape");
    expect(PILL_MARKUP).toContain("prefers-reduced-motion");
    // No external requests: no http(s) URLs anywhere in the pill.
    expect(PILL_MARKUP).not.toMatch(/https?:\/\//);
  });

  it("inserts right after the opening <body> tag", () => {
    const out = injectChatPill("<html><body class='x'><h1>hi</h1></body></html>");
    expect(out.indexOf("<body class='x'>")).toBeLessThan(out.indexOf("dropway-chat-pill"));
    expect(out.indexOf("dropway-chat-backdrop")).toBeLessThan(out.indexOf("<h1>hi</h1>"));
  });

  it("prepends when there is no <body> (fragment)", () => {
    const out = injectChatPill("<h1>fragment</h1>");
    expect(out.startsWith(PILL_MARKUP)).toBe(true);
    expect(out.endsWith("<h1>fragment</h1>")).toBe(true);
  });

  it("injected byte length is exactly original + CHAT_PILL_BYTE_LENGTH", () => {
    const html = "<body>héllo 日本語</body>";
    const enc = new TextEncoder();
    expect(enc.encode(injectChatPill(html)).length).toBe(
      enc.encode(html).length + CHAT_PILL_BYTE_LENGTH,
    );
  });
});

describe("shouldInjectChatPill", () => {
  it("true for chat_id + HTML + injectable charset", () => {
    expect(shouldInjectChatPill(CHAT_ROUTE, "index.html", "text/html; charset=utf-8")).toBe(true);
    expect(shouldInjectChatPill(CHAT_ROUTE, "docs/page.htm", undefined)).toBe(true);
  });

  it("false without a chat_id", () => {
    const noChat: RouteValue = { ...CHAT_ROUTE, chat_id: undefined };
    expect(shouldInjectChatPill(noChat, "index.html", "text/html")).toBe(false);
  });

  it("false for non-HTML served paths", () => {
    expect(shouldInjectChatPill(CHAT_ROUTE, "style.css", "text/css")).toBe(false);
    expect(shouldInjectChatPill(CHAT_ROUTE, "app.js", "text/javascript")).toBe(false);
  });

  it("false for non-UTF-8 HTML (would corrupt on re-encode)", () => {
    expect(
      shouldInjectChatPill(CHAT_ROUTE, "index.html", "text/html; charset=shift_jis"),
    ).toBe(false);
  });
});
