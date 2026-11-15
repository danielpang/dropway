import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MentionExecutionControls } from "@/components/project-room/mention-execution";
import { describe, expect, it } from "vitest";
import { activeMention, filterMentions, insertMention, mentionOptions } from "@/lib/project-room/mentions";
import { hasDropwayMention } from "@/lib/project-room/hosted";

describe("project mention suggestions", () => {
  const options = mentionOptions([
    { userId: "alice", name: "Alice Smith", role: "editor" },
    { userId: "bob", name: "Bob", role: "reviewer" },
  ]);
  it("hides the removed hosted-execution warning", () => {
    expect(renderToStaticMarkup(createElement(MentionExecutionControls, {
      body: "@dropway update this", allowed: false, onChange: () => undefined,
    }))).toBe("");
  });
  it("shows the assistant and project members for a bare @", () => {
    expect(activeMention("Hello @", 7)).toEqual({ start: 6, end: 7, query: "" });
    expect(filterMentions(options, "").map((option) => option.label)).toEqual(["Dropway", "Alice Smith", "Bob"]);
    expect(filterMentions(options, "ALI").map((option) => option.id)).toEqual(["member:alice"]);
  });
  it("does not suggest for email addresses, selections or a completed word", () => {
    expect(activeMention("alice@example.com", 17)).toBeNull();
    expect(activeMention("hello @a", 6, 8)).toBeNull();
    expect(activeMention("@alice hello", 12)).toBeNull();
    expect(activeMention("(@dr", 4)?.query).toBe("dr");
  });
  it("replaces only the active token and preserves following text", () => {
    expect(insertMention("Please @al review", { start: 7, end: 10 }, "@Alice Smith"))
      .toEqual({ value: "Please @Alice Smith review", caret: 19 });
    expect(insertMention("@dr", { start: 0, end: 3 }, "@dropway"))
      .toEqual({ value: "@dropway ", caret: 9 });
  });
  it("keeps unmatched queries empty and excludes non-project members", () => {
    expect(filterMentions(options, "charlie")).toEqual([]);
    expect(options).toHaveLength(3);
  });
  it("does not invoke AI when a member has the reserved assistant name", () => {
    for (const name of ["Dropway", "Dropway Admin", "dropway, team"]) {
      const person = mentionOptions([{ userId: "person", role: "editor", name }])[1]!;
      expect(hasDropwayMention(`${person.text} please review`)).toBe(false);
    }
    expect(hasDropwayMention(`${options[0]!.text} please review`)).toBe(true);
  });
});
