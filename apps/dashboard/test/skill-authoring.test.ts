import { describe, expect, it } from "vitest";

import { composeSkillFiles, skillTemplate } from "@/lib/skill-authoring";
import { renderMarkdownToHtml } from "@/lib/markdown";

describe("composeSkillFiles", () => {
  it("puts SKILL.md first and preserves its body", async () => {
    const body = "---\nname: x\n---\n# X\n";
    const res = composeSkillFiles(body, []);
    expect(res.error).toBeNull();
    expect(res.files[0]!.path).toBe("SKILL.md");
    expect(await res.files[0]!.file.text()).toBe(body);
  });

  it("rejects an empty body", () => {
    expect(composeSkillFiles("   \n", []).error).toMatch(/empty/i);
  });

  it("appends valid extra files and ignores blank rows", () => {
    const res = composeSkillFiles(skillTemplate("y"), [
      { path: "references/checklist.md", content: "- do the thing" },
      { path: "  ", content: "ignored" },
    ]);
    expect(res.error).toBeNull();
    expect(res.files.map((f) => f.path)).toEqual(["SKILL.md", "references/checklist.md"]);
  });

  it("rejects an unsafe or reserved extra path", () => {
    expect(composeSkillFiles(skillTemplate("y"), [{ path: "../secret", content: "x" }]).error).toMatch(
      /invalid file path/i,
    );
    expect(composeSkillFiles(skillTemplate("y"), [{ path: "SKILL.md", content: "x" }]).error).toMatch(
      /invalid file path/i,
    );
  });

  it("rejects a duplicate extra path", () => {
    const res = composeSkillFiles(skillTemplate("y"), [
      { path: "a.md", content: "1" },
      { path: "a.md", content: "2" },
    ]);
    expect(res.error).toMatch(/duplicate/i);
  });
});

describe("renderMarkdownToHtml", () => {
  it("escapes raw HTML before transforming (XSS-safe)", () => {
    const html = renderMarkdownToHtml("<script>alert(1)</script>");
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });

  it("renders headings, emphasis, and code", () => {
    expect(renderMarkdownToHtml("# Title")).toContain("<h1>Title</h1>");
    expect(renderMarkdownToHtml("**bold**")).toContain("<strong>bold</strong>");
    expect(renderMarkdownToHtml("use `code` here")).toContain("<code>code</code>");
  });

  it("renders ordered and unordered lists", () => {
    const ul = renderMarkdownToHtml("- one\n- two");
    expect(ul).toContain("<ul>");
    expect(ul).toContain("<li>one</li>");
    const ol = renderMarkdownToHtml("1. first\n2. second");
    expect(ol).toContain("<ol>");
    expect(ol).toContain("<li>first</li>");
  });

  it("only emits safe link targets", () => {
    expect(renderMarkdownToHtml("[ok](https://example.com)")).toContain('href="https://example.com"');
    // javascript: scheme is not emitted as a link — the literal markdown survives.
    const evil = renderMarkdownToHtml("[x](javascript:alert(1))");
    expect(evil).not.toContain("<a ");
  });
});
