// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package markdown is the Go port of the serving Worker's Markdown viewer
// (edge/serving-worker/src/markdown.ts). A file-dump upload (no index.html) is
// browsed through the autoindex (internal/listing); clicking a Markdown file used
// to stream its raw bytes (text/markdown), which a browser renders as plain text
// or downloads. Instead, when a request resolves to a .md/.mdx blob, serve.go
// renders this self-contained HTML page: the formatted Markdown is shown by
// default, a toggle flips to the raw source, and a button copies the raw text to
// the clipboard.
//
// The converter supports the common CommonMark subset (headings, emphasis, code,
// lists, blockquotes, links, images, rules, paragraphs); it is intentionally NOT
// a full CommonMark implementation.
//
// PARITY: this port and the TypeScript original are kept in lockstep and produce
// identical output for typical (ASCII-whitespace) Markdown — a golden-fixture suite
// (markdown_test.go + markdown.test.ts read the shared testdata/markdown fixtures)
// guards against drift. They are NOT guaranteed identical on exotic Unicode
// whitespace (NBSP, vertical tab, form feed) at a block-marker boundary: Go's RE2
// `\s` and TrimLeft(" \t") are ASCII-only while JS's `\s`/trimStart() are
// Unicode-aware. That corner is documented, not relied upon.
//
// SECURITY: every byte of tenant source is HTML-escaped before any Markdown
// transform, so raw HTML in the source is shown literally and can never inject
// markup or script. Link/image URLs are scheme-checked (javascript:/data: etc.
// are neutralized).
package markdown

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/danielpang/dropway/services/serve/internal/servehttp"
)

// MarkdownMaxRenderBytes is the maximum source size (bytes) the serving paths will
// render into the viewer page. Rendering buffers the whole document into memory, so
// a larger file is streamed as raw bytes instead, bounding worst-case memory.
// Generous vs any human-written doc (1 MiB). Mirrors markdown.ts
// MARKDOWN_MAX_RENDER_BYTES.
const MarkdownMaxRenderBytes = 1024 * 1024

// nul is the placeholder sentinel (impossible in normal text) used to hold code
// spans out of the escape + emphasis passes. Mirrors markdown.ts NUL.
const nul = "\x00"

// htmlEscaper escapes the five HTML-significant characters, matching markdown.ts
// esc() exactly (note: "&#39;" for ', "&quot;" for " — NOT Go's html package
// defaults, which would diverge from the TS output). A Replacer is a single
// left-to-right pass, so an emitted "&amp;" is never re-escaped.
var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#39;",
)

func esc(s string) string { return htmlEscaper.Replace(s) }

// IsMarkdownPath reports whether a served path is a Markdown document we should
// render (.md / .mdx, case-insensitive). Mirrors isMarkdownPath.
func IsMarkdownPath(path string) bool {
	ext := servehttp.ExtensionOf(path)
	return ext == "md" || ext == "mdx"
}

// baseName returns the final path segment (basename) for the page title/header.
// Mirrors baseName.
func baseName(path string) string {
	trimmed := strings.TrimRight(path, "/")
	if i := strings.LastIndex(trimmed, "/"); i != -1 {
		seg := trimmed[i+1:]
		if seg != "" {
			return seg
		}
	}
	return trimmed
}

// schemeRE matches a URL scheme (letters/digits/+-. up to the first ':').
var schemeRE = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*):`)

// safeURL neutralizes a link/image URL whose scheme could execute script or
// smuggle content (javascript:/vbscript:/data: ...). Relative URLs, fragments,
// and http(s)/mailto/tel pass through; anything else becomes "#". Mirrors safeUrl.
func safeURL(url string) string {
	u := strings.TrimSpace(url)
	if m := schemeRE.FindStringSubmatch(u); m != nil {
		s := strings.ToLower(m[1])
		if s != "http" && s != "https" && s != "mailto" && s != "tel" {
			return "#"
		}
	}
	return u
}

// --- Inline rendering -------------------------------------------------------

var (
	codeSpanRE    = regexp.MustCompile("`([^`]+)`")
	imgRE         = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	linkRE        = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	boldStarRE    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	boldUnderRE   = regexp.MustCompile(`__([^_]+)__`)
	emStarRE      = regexp.MustCompile(`\*([^*]+)\*`)
	emUnderRE     = regexp.MustCompile(`_([^_]+)_`)
	placeholderRE = regexp.MustCompile(nul + `(\d+)` + nul)
)

// renderInline renders the inline span syntax of one line of Markdown to HTML.
// Code spans are extracted FIRST (so their contents are never treated as
// emphasis/links), then the remaining text is HTML-escaped and the
// emphasis/link/image transforms run against the escaped text. Mirrors renderInline.
func renderInline(src string) string {
	var codes []string
	// Pull out `code spans` (single backtick pairs), leaving a NUL-delimited
	// placeholder the escape + emphasis passes leave untouched.
	withPlaceholders := codeSpanRE.ReplaceAllStringFunc(src, func(m string) string {
		code := codeSpanRE.FindStringSubmatch(m)[1]
		codes = append(codes, "<code>"+esc(code)+"</code>")
		return nul + strconv.Itoa(len(codes)-1) + nul
	})

	out := esc(withPlaceholders)

	// Images: ![alt](url) — before links, since both start with '['.
	out = imgRE.ReplaceAllStringFunc(out, func(m string) string {
		sub := imgRE.FindStringSubmatch(m)
		return `<img src="` + esc(safeURL(sub[2])) + `" alt="` + sub[1] + `">`
	})
	// Links: [text](url)
	out = linkRE.ReplaceAllStringFunc(out, func(m string) string {
		sub := linkRE.FindStringSubmatch(m)
		return `<a href="` + esc(safeURL(sub[2])) + `" rel="nofollow noopener noreferrer">` + sub[1] + `</a>`
	})
	// Bold then italic (bold first so ** isn't eaten by the * rule).
	out = boldStarRE.ReplaceAllString(out, "<strong>$1</strong>")
	out = boldUnderRE.ReplaceAllString(out, "<strong>$1</strong>")
	out = emStarRE.ReplaceAllString(out, "<em>$1</em>")
	out = emUnderRE.ReplaceAllString(out, "<em>$1</em>")

	// Restore code spans.
	out = placeholderRE.ReplaceAllStringFunc(out, func(m string) string {
		i, _ := strconv.Atoi(placeholderRE.FindStringSubmatch(m)[1])
		if i >= 0 && i < len(codes) {
			return codes[i]
		}
		return ""
	})
	return out
}

// --- Block rendering --------------------------------------------------------

var (
	headingRE    = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	fenceRE      = regexp.MustCompile("^\\s*(```|~~~)(.*)$")
	ulItemRE     = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	olItemRE     = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	blockquoteRE = regexp.MustCompile(`^\s*>\s?(.*)$`)
	wsRE         = regexp.MustCompile(`\s+`)
)

// isHR reports a thematic-break line: three or more of the SAME marker (- * _),
// spaces allowed between them and nothing else. Mirrors isHR.
func isHR(line string) bool {
	compact := wsRE.ReplaceAllString(line, "")
	if len(compact) < 3 {
		return false
	}
	c := compact[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 0; i < len(compact); i++ {
		if compact[i] != c {
			return false
		}
	}
	return true
}

// RenderMarkdown converts a full Markdown document to an HTML fragment (the body
// of the rendered pane). Single level of list nesting; fenced code blocks are
// emitted verbatim (escaped, no inline processing). Mirrors renderMarkdown.
func RenderMarkdown(source string) string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")
	lines := strings.Split(source, "\n")
	at := func(n int) string {
		if n >= 0 && n < len(lines) {
			return lines[n]
		}
		return ""
	}
	var out []string
	i := 0

	for i < len(lines) {
		line := at(i)

		// Blank line — separates blocks; skip.
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}

		// Fenced code block.
		if fence := fenceRE.FindStringSubmatch(line); fence != nil {
			marker := fence[1]
			lang := strings.Fields(strings.TrimSpace(fence[2]))
			langTok := ""
			if len(lang) > 0 {
				langTok = lang[0]
			}
			var code []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimLeft(at(i), " \t"), marker) {
				code = append(code, at(i))
				i++
			}
			if i < len(lines) {
				i++ // consume the closing fence
			}
			cls := ""
			if langTok != "" {
				cls = ` class="language-` + esc(langTok) + `"`
			}
			out = append(out, "<pre><code"+cls+">"+esc(strings.Join(code, "\n"))+"</code></pre>")
			continue
		}

		// Heading.
		if heading := headingRE.FindStringSubmatch(line); heading != nil {
			level := strconv.Itoa(len(heading[1]))
			out = append(out, "<h"+level+">"+renderInline(heading[2])+"</h"+level+">")
			i++
			continue
		}

		// Horizontal rule.
		if isHR(line) {
			out = append(out, "<hr>")
			i++
			continue
		}

		// Blockquote — consume consecutive '>' lines, then render their content.
		if blockquoteRE.MatchString(line) {
			var inner []string
			for i < len(lines) && blockquoteRE.MatchString(at(i)) {
				inner = append(inner, blockquoteRE.FindStringSubmatch(at(i))[1])
				i++
			}
			out = append(out, "<blockquote>"+RenderMarkdown(strings.Join(inner, "\n"))+"</blockquote>")
			continue
		}

		// List (unordered or ordered) — consume consecutive item lines.
		if ulItemRE.MatchString(line) || olItemRE.MatchString(line) {
			ordered := olItemRE.MatchString(line) && !ulItemRE.MatchString(line)
			pattern := ulItemRE
			tag := "ul"
			if ordered {
				pattern = olItemRE
				tag = "ol"
			}
			var items []string
			for i < len(lines) && pattern.MatchString(at(i)) {
				items = append(items, "<li>"+renderInline(pattern.FindStringSubmatch(at(i))[1])+"</li>")
				i++
			}
			out = append(out, "<"+tag+">"+strings.Join(items, "")+"</"+tag+">")
			continue
		}

		// Paragraph — consume consecutive lines until a blank line or a block start.
		var para []string
		for i < len(lines) &&
			strings.TrimSpace(at(i)) != "" &&
			!fenceRE.MatchString(at(i)) &&
			!headingRE.MatchString(at(i)) &&
			!isHR(at(i)) &&
			!blockquoteRE.MatchString(at(i)) &&
			!ulItemRE.MatchString(at(i)) &&
			!olItemRE.MatchString(at(i)) {
			para = append(para, renderInline(at(i)))
			i++
		}
		out = append(out, "<p>"+strings.Join(para, "<br>\n")+"</p>")
	}

	return strings.Join(out, "\n")
}

// --- Full page --------------------------------------------------------------

// viewerScript is the inline toggle + copy-to-clipboard script. Kept identical to
// markdown.ts VIEWER_SCRIPT.
const viewerScript = `(function(){
  var raw=document.getElementById('md-raw');
  var rendered=document.getElementById('md-rendered');
  var toggle=document.getElementById('md-toggle');
  var copy=document.getElementById('md-copy');
  var showingRaw=false;
  toggle.addEventListener('click',function(){
    showingRaw=!showingRaw;
    raw.hidden=!showingRaw;
    rendered.hidden=showingRaw;
    toggle.textContent=showingRaw?'View rendered':'View raw';
  });
  copy.addEventListener('click',function(){
    var text=raw.textContent||'';
    function done(){var t=copy.textContent;copy.textContent='Copied!';setTimeout(function(){copy.textContent=t;},1500);}
    if(navigator.clipboard&&navigator.clipboard.writeText){
      navigator.clipboard.writeText(text).then(done,fallback);
    }else{fallback();}
    function fallback(){
      var ta=document.createElement('textarea');ta.value=text;ta.style.position='fixed';ta.style.opacity='0';
      document.body.appendChild(ta);ta.focus();ta.select();
      try{document.execCommand('copy');done();}catch(e){}
      document.body.removeChild(ta);
    }
  });
})();`

// RenderMarkdownPage renders a complete, self-contained HTML page for a Markdown
// file: a sticky toolbar (file name + Raw toggle + Copy), the rendered Markdown,
// and the raw source in a hidden <pre>. Inline styles + one inline script only;
// every piece of tenant content is escaped. Mirrors renderMarkdownPage.
func RenderMarkdownPage(path, source string) string {
	name := esc(baseName(path))
	rendered := RenderMarkdown(source)
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + name + `</title>
<style>
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body { font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; margin: 0; background: #fafafa; color: #1a1a1a; }
header { position: sticky; top: 0; z-index: 10; display: flex; align-items: center; gap: 0.75rem; padding: 0.6rem 1.25rem; background: rgba(250,250,250,0.85); backdrop-filter: saturate(1.8) blur(8px); border-bottom: 1px solid #e3e3e3; }
header .name { font-size: 0.9rem; font-weight: 600; word-break: break-all; margin-right: auto; }
header button { font: inherit; font-size: 0.82rem; font-weight: 500; cursor: pointer; padding: 0.35rem 0.7rem; border: 1px solid #d4d4d4; border-radius: 0.4rem; background: #fff; color: #1a1a1a; }
header button:hover { background: #f0f0f0; }
header a.raw { font-size: 0.82rem; font-weight: 500; text-decoration: none; color: #1a56db; white-space: nowrap; }
header a.raw:hover { text-decoration: underline; }
main { max-width: 820px; margin: 0 auto; padding: 2rem 1.25rem 4rem; }
.markdown-body h1, .markdown-body h2 { border-bottom: 1px solid #e3e3e3; padding-bottom: 0.3rem; }
.markdown-body h1 { font-size: 1.9rem; } .markdown-body h2 { font-size: 1.5rem; }
.markdown-body h3 { font-size: 1.25rem; } .markdown-body h4 { font-size: 1.05rem; }
.markdown-body img { max-width: 100%; }
.markdown-body a { color: #1a56db; }
.markdown-body code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.9em; background: rgba(135,131,120,0.15); padding: 0.15em 0.35em; border-radius: 0.3rem; }
.markdown-body pre { background: #f4f4f5; padding: 1rem; border-radius: 0.5rem; overflow: auto; }
.markdown-body pre code { background: none; padding: 0; }
.markdown-body blockquote { margin: 0 0 1rem; padding: 0 1rem; color: #666; border-left: 0.25rem solid #e3e3e3; }
#md-raw { white-space: pre-wrap; word-break: break-word; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.875rem; line-height: 1.5; background: #f4f4f5; padding: 1rem; border-radius: 0.5rem; overflow: auto; }
@media (prefers-color-scheme: dark) {
  body { background: #16181c; color: #e6e6e6; }
  header { background: rgba(22,24,28,0.85); border-bottom-color: #2a2d34; }
  header button { background: #1f2228; border-color: #34383f; color: #e6e6e6; }
  header button:hover { background: #2a2d34; }
  .markdown-body h1, .markdown-body h2 { border-bottom-color: #2a2d34; }
  .markdown-body a, header a.raw { color: #6ea8ff; }
  .markdown-body code { background: rgba(110,118,129,0.25); }
  .markdown-body pre, #md-raw { background: #1f2228; }
  .markdown-body blockquote { color: #9aa0a8; border-left-color: #2a2d34; }
}
</style>
</head>
<body>
<header>
<span class="name">` + name + `</span>
<button type="button" id="md-toggle">View raw</button>
<button type="button" id="md-copy">Copy</button>
<a class="raw" href="?raw=1" title="Open the original source — append ?raw to the URL">Open raw ↗</a>
</header>
<main>
<article class="markdown-body" id="md-rendered">
` + rendered + `
</article>
<pre id="md-raw" hidden>` + esc(source) + `</pre>
</main>
<script>` + viewerScript + `</script>
</body>
</html>
`
}
