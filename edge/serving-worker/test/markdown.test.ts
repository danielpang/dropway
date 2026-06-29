// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the Markdown viewer pure logic: which paths render as Markdown,
// the block/inline → HTML conversion (and its escaping/sanitization), and the
// self-contained viewer page. The serve-path integration (a .md request returns
// the rendered HTML page instead of raw bytes) is covered in serve.test.ts.

import { describe, expect, it } from "vitest";

import { isMarkdownPath, renderMarkdown, renderMarkdownPage } from "../src/markdown";

describe("isMarkdownPath", () => {
  it("matches .md and .mdx, case-insensitively", () => {
    expect(isMarkdownPath("README.md")).toBe(true);
    expect(isMarkdownPath("docs/guide.MDX")).toBe(true);
    expect(isMarkdownPath("a/b/notes.Md")).toBe(true);
  });

  it("rejects non-Markdown paths", () => {
    expect(isMarkdownPath("index.html")).toBe(false);
    expect(isMarkdownPath("style.css")).toBe(false);
    expect(isMarkdownPath("README")).toBe(false);
    expect(isMarkdownPath("notes.markdown")).toBe(false);
    expect(isMarkdownPath("archive.md.gz")).toBe(false);
  });
});

describe("renderMarkdown — blocks", () => {
  it("renders ATX headings", () => {
    expect(renderMarkdown("# Title")).toBe("<h1>Title</h1>");
    expect(renderMarkdown("### Sub")).toBe("<h3>Sub</h3>");
  });

  it("wraps loose text in a paragraph", () => {
    expect(renderMarkdown("hello world")).toBe("<p>hello world</p>");
  });

  it("joins wrapped paragraph lines with a soft break", () => {
    expect(renderMarkdown("one\ntwo")).toBe("<p>one<br>\ntwo</p>");
  });

  it("separates paragraphs on a blank line", () => {
    expect(renderMarkdown("one\n\ntwo")).toBe("<p>one</p>\n<p>two</p>");
  });

  it("renders unordered lists", () => {
    expect(renderMarkdown("- a\n- b")).toBe("<ul><li>a</li><li>b</li></ul>");
    expect(renderMarkdown("* a\n+ b")).toBe("<ul><li>a</li><li>b</li></ul>");
  });

  it("renders ordered lists", () => {
    expect(renderMarkdown("1. a\n2. b")).toBe("<ol><li>a</li><li>b</li></ol>");
  });

  it("renders a horizontal rule", () => {
    expect(renderMarkdown("---")).toBe("<hr>");
    expect(renderMarkdown("***")).toBe("<hr>");
  });

  it("renders blockquotes", () => {
    expect(renderMarkdown("> quoted")).toBe("<blockquote><p>quoted</p></blockquote>");
  });

  it("renders a fenced code block verbatim (no inline processing, escaped)", () => {
    const md = "```js\nconst x = 1 < 2 && *y*;\n```";
    expect(renderMarkdown(md)).toBe(
      '<pre><code class="language-js">const x = 1 &lt; 2 &amp;&amp; *y*;</code></pre>',
    );
  });
});

describe("renderMarkdown — inline", () => {
  it("renders bold and italic", () => {
    expect(renderMarkdown("**b** and *i*")).toBe(
      "<p><strong>b</strong> and <em>i</em></p>",
    );
    expect(renderMarkdown("__b__ and _i_")).toBe(
      "<p><strong>b</strong> and <em>i</em></p>",
    );
  });

  it("renders inline code without applying emphasis inside it", () => {
    expect(renderMarkdown("use `a * b` here")).toBe(
      "<p>use <code>a * b</code> here</p>",
    );
  });

  it("renders links and images", () => {
    expect(renderMarkdown("[text](https://example.com)")).toBe(
      '<p><a href="https://example.com" rel="nofollow noopener noreferrer">text</a></p>',
    );
    expect(renderMarkdown("![alt](https://example.com/a.png)")).toBe(
      '<p><img src="https://example.com/a.png" alt="alt"></p>',
    );
  });
});

describe("renderMarkdown — security", () => {
  it("escapes raw HTML in the source (no markup injection)", () => {
    expect(renderMarkdown("<script>alert(1)</script>")).toBe(
      "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>",
    );
  });

  it("escapes HTML inside inline code", () => {
    expect(renderMarkdown("`<b>`")).toBe("<p><code>&lt;b&gt;</code></p>");
  });

  it("neutralizes a javascript: link URL", () => {
    expect(renderMarkdown("[x](javascript:alert(1))")).toContain('href="#"');
  });

  it("keeps relative and fragment link URLs", () => {
    expect(renderMarkdown("[x](../other.md)")).toContain('href="../other.md"');
    expect(renderMarkdown("[x](#section)")).toContain('href="#section"');
  });
});

describe("renderMarkdownPage", () => {
  const page = renderMarkdownPage("docs/README.md", "# Hi\n\nbody");

  it("is a complete HTML document titled by the file's basename", () => {
    expect(page.startsWith("<!doctype html>")).toBe(true);
    expect(page).toContain("<title>README.md</title>");
  });

  it("embeds the rendered Markdown by default", () => {
    expect(page).toContain('<article class="markdown-body" id="md-rendered">');
    expect(page).toContain("<h1>Hi</h1>");
  });

  it("ships the raw source, escaped, in a hidden pre", () => {
    expect(page).toContain('<pre id="md-raw" hidden># Hi\n\nbody</pre>');
  });

  it("ships the toggle + copy controls and their inline script", () => {
    expect(page).toContain('id="md-toggle"');
    expect(page).toContain('id="md-copy"');
    expect(page).toContain("navigator.clipboard");
  });

  it("escapes the raw source so it cannot break out of the pre", () => {
    const p = renderMarkdownPage("x.md", "</pre><script>bad</script>");
    expect(p).toContain("&lt;/pre&gt;&lt;script&gt;bad&lt;/script&gt;");
    expect(p).not.toContain("<script>bad</script>");
  });
});
