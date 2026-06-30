// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the Markdown viewer pure logic, mirroring the TypeScript suite
// (edge/serving-worker/test/markdown.test.ts) so both ports stay byte-for-byte
// aligned. The serve-path integration is covered in serve_test.go.
package markdown

import (
	"strings"
	"testing"
)

func TestIsMarkdownPath(t *testing.T) {
	yes := []string{"README.md", "docs/guide.MDX", "a/b/notes.Md"}
	for _, p := range yes {
		if !IsMarkdownPath(p) {
			t.Errorf("IsMarkdownPath(%q) = false, want true", p)
		}
	}
	no := []string{"index.html", "style.css", "README", "notes.markdown", "archive.md.gz"}
	for _, p := range no {
		if IsMarkdownPath(p) {
			t.Errorf("IsMarkdownPath(%q) = true, want false", p)
		}
	}
}

func TestRenderMarkdownBlocks(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# Title", "<h1>Title</h1>"},
		{"### Sub", "<h3>Sub</h3>"},
		{"hello world", "<p>hello world</p>"},
		{"one\ntwo", "<p>one<br>\ntwo</p>"},
		{"one\n\ntwo", "<p>one</p>\n<p>two</p>"},
		{"- a\n- b", "<ul><li>a</li><li>b</li></ul>"},
		{"* a\n+ b", "<ul><li>a</li><li>b</li></ul>"},
		{"1. a\n2. b", "<ol><li>a</li><li>b</li></ol>"},
		{"---", "<hr>"},
		{"***", "<hr>"},
		{"> quoted", "<blockquote><p>quoted</p></blockquote>"},
		{"```js\nconst x = 1 < 2 && *y*;\n```", `<pre><code class="language-js">const x = 1 &lt; 2 &amp;&amp; *y*;</code></pre>`},
	}
	for _, c := range cases {
		if got := RenderMarkdown(c.in); got != c.want {
			t.Errorf("RenderMarkdown(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderMarkdownInline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**b** and *i*", "<p><strong>b</strong> and <em>i</em></p>"},
		{"__b__ and _i_", "<p><strong>b</strong> and <em>i</em></p>"},
		{"use `a * b` here", "<p>use <code>a * b</code> here</p>"},
		{"[text](https://example.com)", `<p><a href="https://example.com" rel="nofollow noopener noreferrer">text</a></p>`},
		{"![alt](https://example.com/a.png)", `<p><img src="https://example.com/a.png" alt="alt"></p>`},
	}
	for _, c := range cases {
		if got := RenderMarkdown(c.in); got != c.want {
			t.Errorf("RenderMarkdown(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderMarkdownSecurity(t *testing.T) {
	if got := RenderMarkdown("<script>alert(1)</script>"); got != "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>" {
		t.Errorf("raw HTML not escaped: %q", got)
	}
	if got := RenderMarkdown("`<b>`"); got != "<p><code>&lt;b&gt;</code></p>" {
		t.Errorf("code HTML not escaped: %q", got)
	}
	if got := RenderMarkdown("[x](javascript:alert(1))"); !contains(got, `href="#"`) {
		t.Errorf("javascript: URL not neutralized: %q", got)
	}
	if got := RenderMarkdown("[x](../other.md)"); !contains(got, `href="../other.md"`) {
		t.Errorf("relative URL not kept: %q", got)
	}
	if got := RenderMarkdown("[x](#section)"); !contains(got, `href="#section"`) {
		t.Errorf("fragment URL not kept: %q", got)
	}
}

func TestRenderMarkdownPage(t *testing.T) {
	page := RenderMarkdownPage("docs/README.md", "# Hi\n\nbody")
	checks := []string{
		"<!doctype html>",
		"<title>README.md</title>",
		`<article class="markdown-body" id="md-rendered">`,
		"<h1>Hi</h1>",
		`<pre id="md-raw" hidden># Hi`,
		`id="md-toggle"`,
		`id="md-copy"`,
		"navigator.clipboard",
		`<a class="brand" href="https://dropway.dev"`,
		"<span>Dropway</span>",
	}
	for _, c := range checks {
		if !contains(page, c) {
			t.Errorf("page missing %q", c)
		}
	}

	// The raw source is escaped so it cannot break out of the <pre>.
	bad := RenderMarkdownPage("x.md", "</pre><script>bad</script>")
	if !contains(bad, "&lt;/pre&gt;&lt;script&gt;bad&lt;/script&gt;") {
		t.Errorf("raw source not escaped: %q", bad)
	}
	if contains(bad, "<script>bad</script>") {
		t.Errorf("raw source broke out of pre: %q", bad)
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
