// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The AI builder's transcript transform: raw persisted OpenRouter messages →
// the chat items the UI renders. This is the shared path for initial hydration
// AND reconnect replay, so its edge cases (tool_call pairing, unparseable args,
// orphan tool results) decide what a restored conversation looks like.

import { describe, expect, it } from "vitest";

import {
  itemsFromTranscript,
  maxSeq,
  parseToolArgs,
  toolLabel,
  toolSummary,
} from "@/components/ai/transcript";

describe("itemsFromTranscript", () => {
  it("maps user and assistant text to bubbles, skipping system", () => {
    const items = itemsFromTranscript([
      { seq: 1, role: "system", content: { role: "system", content: "prompt" } },
      { seq: 2, role: "user", content: { role: "user", content: "make it blue" } },
      { seq: 3, role: "assistant", content: { role: "assistant", content: "Done." } },
    ]);
    expect(items).toEqual([
      { kind: "user", text: "make it blue" },
      { kind: "assistant", text: "Done." },
    ]);
  });

  it("renders tool calls as cards and attaches results by tool_call_id", () => {
    const items = itemsFromTranscript([
      {
        role: "assistant",
        content: {
          role: "assistant",
          content: "",
          tool_calls: [
            {
              id: "call_1",
              function: { name: "write_file", arguments: '{"path":"index.html","content":"<h1>"}' },
            },
            {
              id: "call_2",
              function: { name: "run_command", arguments: '{"command":"ls"}' },
            },
          ],
        },
      },
      { role: "tool", content: { role: "tool", tool_call_id: "call_2", content: "exit_code: 0" } },
      { role: "tool", content: { role: "tool", tool_call_id: "call_1", content: "wrote index.html" } },
    ]);
    expect(items).toHaveLength(2);
    expect(items[0]).toMatchObject({
      kind: "tool",
      tool: "write_file",
      args: { path: "index.html", content: "<h1>" },
      result: "wrote index.html",
      done: true,
    });
    expect(items[1]).toMatchObject({
      kind: "tool",
      tool: "run_command",
      result: "exit_code: 0",
      done: true,
    });
  });

  it("keeps an unanswered tool call visible as still running", () => {
    const items = itemsFromTranscript([
      {
        role: "assistant",
        content: {
          role: "assistant",
          tool_calls: [{ id: "c1", function: { name: "run_command", arguments: "{}" } }],
        },
      },
    ]);
    expect(items[0]).toMatchObject({ kind: "tool", done: false, result: null });
  });

  it("ignores orphan tool results and empty text", () => {
    const items = itemsFromTranscript([
      { role: "tool", content: { role: "tool", tool_call_id: "ghost", content: "boo" } },
      { role: "assistant", content: { role: "assistant", content: "" } },
    ]);
    expect(items).toEqual([]);
  });

  it("interleaves text and tool cards in transcript order", () => {
    const items = itemsFromTranscript([
      { role: "user", content: { role: "user", content: "go" } },
      {
        role: "assistant",
        content: {
          role: "assistant",
          content: "Let me look first.",
          tool_calls: [{ id: "c1", function: { name: "list_files", arguments: "{}" } }],
        },
      },
      { role: "tool", content: { role: "tool", tool_call_id: "c1", content: "file\tindex.html\t10" } },
      { role: "assistant", content: { role: "assistant", content: "All set." } },
    ]);
    expect(items.map((i) => i.kind)).toEqual(["user", "assistant", "tool", "assistant"]);
  });

  it("reads content-part arrays as concatenated text", () => {
    const items = itemsFromTranscript([
      {
        role: "user",
        content: { role: "user", content: [{ type: "text", text: "a" }, { type: "text", text: "b" }] },
      },
    ]);
    expect(items).toEqual([{ kind: "user", text: "ab" }]);
  });
});

describe("parseToolArgs", () => {
  it("parses objects and rejects everything else", () => {
    expect(parseToolArgs('{"path":"a"}')).toEqual({ path: "a" });
    expect(parseToolArgs('{"path":"a", TRUNCATED')).toBeNull();
    expect(parseToolArgs('"just a string"')).toBeNull();
    expect(parseToolArgs("[1,2]")).toBeNull();
    expect(parseToolArgs(undefined)).toBeNull();
  });
});

describe("maxSeq", () => {
  it("returns the highest seq, 0 when none", () => {
    expect(maxSeq([])).toBe(0);
    expect(maxSeq([{ role: "user", content: {} }])).toBe(0);
    expect(
      maxSeq([
        { seq: 2, role: "user", content: {} },
        { seq: 7, role: "assistant", content: {} },
      ]),
    ).toBe(7);
  });
});

describe("tool card copy", () => {
  it("summarizes by tool", () => {
    expect(toolSummary("run_command", { command: "npm run build" })).toBe("npm run build");
    expect(toolSummary("write_file", { path: "index.html", content: "x" })).toBe("index.html");
    expect(toolSummary("list_files", {})).toBe("site root");
    expect(toolSummary("run_command", null)).toBe("");
  });

  it("labels by lifecycle", () => {
    expect(toolLabel("write_file", false)).toBe("Writing file");
    expect(toolLabel("write_file", true)).toBe("Wrote file");
    expect(toolLabel("custom_tool", true)).toBe("Ran custom_tool");
  });
});
