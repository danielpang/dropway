// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Markdown viewer for `.md` / `.mdx` uploads. A file-dump upload (no index.html)
// is browsed through the autoindex (listing.ts); clicking a Markdown file used to
// stream its raw bytes (text/markdown), which a browser renders as plain text or
// downloads. Instead, when a request resolves to a `.md`/`.mdx` blob, index.ts
// renders this self-contained HTML page: the formatted Markdown is shown by
// default, a toggle flips to the raw source, a button copies the raw text to the
// clipboard, and a `?raw` link fetches the original source bytes directly.
//
// Why a hand-rolled renderer (no library): the serving Worker ships dependency-
// free, self-contained HTML the same way listing.ts does, and the logic is mirrored
// by the Go port (services/serve/internal/markdown). The converter supports the
// common CommonMark subset (headings, emphasis, code, lists, blockquotes, links,
// images, rules, paragraphs); it is intentionally NOT a full CommonMark
// implementation.
//
// PARITY: the two ports are kept in lockstep and produce identical output for
// typical (ASCII-whitespace) Markdown — a golden-fixture suite (test/markdown.test.ts
// + markdown_test.go read the shared testdata/markdown fixtures) guards against
// drift. They are NOT guaranteed identical on exotic Unicode whitespace (NBSP,
// vertical tab, form feed) at a block-marker boundary: JS regex `\s` / trimStart()
// are Unicode-aware while Go's RE2 `\s` / TrimLeft(" \t") are ASCII-only. That
// corner is documented, not relied upon.
//
// SECURITY: every byte of tenant source is HTML-escaped before any Markdown
// transform, so raw HTML in the source is shown literally and can never inject
// markup or script. Link/image URLs are scheme-checked (javascript:/data: etc.
// are neutralized). The page's own toggle/copy script is inline, which the tenant
// content CSP (security.ts CONTENT_CSP) already permits via script-src
// 'unsafe-inline'.

import { extensionOf } from "./http";

/**
 * Maximum source size (bytes) the serving paths will render into the viewer page.
 * Rendering buffers the whole document into memory (twice, counting the banner
 * pass), so a file larger than this is streamed as raw bytes instead — bounding
 * worst-case memory on a constrained edge isolate. Generous vs any human-written
 * doc (1 MiB). Mirrored by markdown.go markdownMaxRenderBytes.
 */
export const MARKDOWN_MAX_RENDER_BYTES = 1024 * 1024;

/** A placeholder sentinel (NUL is impossible in normal text) used to hold code
 * spans out of the escape + emphasis passes. */
const NUL = String.fromCharCode(0);

/** True when a served path is a Markdown document we should render (.md / .mdx). */
export function isMarkdownPath(path: string): boolean {
  const ext = extensionOf(path);
  return ext === "md" || ext === "mdx";
}

/** The final path segment (basename), used as the page title / header label. */
function baseName(path: string): string {
  const trimmed = path.replace(/\/+$/, "");
  const seg = trimmed.split("/").pop() ?? trimmed;
  return seg === "" ? trimmed : seg;
}

/** Escape the five HTML-significant characters for safe interpolation. */
function esc(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/**
 * Neutralize a link/image URL whose scheme could execute script or smuggle
 * content (javascript:, vbscript:, data: ...). Relative URLs, fragments, and the
 * http(s)/mailto/tel schemes pass through; anything else becomes "#". The URL is
 * matched BEFORE HTML-escaping, so we test the raw scheme.
 */
function safeUrl(url: string): string {
  const u = url.trim();
  // A scheme is letters/digits/+-. up to the first ':' with no slash/space before it.
  const scheme = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(u);
  if (scheme) {
    const s = scheme[1]!.toLowerCase();
    if (s !== "http" && s !== "https" && s !== "mailto" && s !== "tel") return "#";
  }
  return u;
}

// --- Inline rendering -------------------------------------------------------

/**
 * Render the inline span syntax of one line of Markdown to HTML. Code spans are
 * extracted FIRST (so their contents are never treated as emphasis/links), then
 * the remaining text is HTML-escaped and the emphasis/link/image transforms run
 * against the escaped text — the Markdown delimiters (* _ [ ] ( ) ! `) are not
 * escaped, so the patterns still match.
 */
function renderInline(src: string): string {
  const codes: string[] = [];
  // Pull out `code spans` (single backtick pairs), leaving a NUL-delimited
  // placeholder that the escape + emphasis passes leave untouched (NUL never
  // appears in real text, so the placeholder can never collide with content).
  // Single-backtick spans only — kept simple so the Go port (whose RE2 engine
  // has no backreferences) matches this byte-for-byte.
  const withPlaceholders = src.replace(/`([^`]+)`/g, (_m, code) => {
    const i = codes.push(`<code>${esc(String(code))}</code>`) - 1;
    return `${NUL}${i}${NUL}`;
  });

  let out = esc(withPlaceholders);

  // Images: ![alt](url) — before links, since both start with `[`.
  out = out.replace(
    /!\[([^\]]*)\]\(([^)\s]+)\)/g,
    (_m, alt, url) => `<img src="${esc(safeUrl(url))}" alt="${alt}">`,
  );
  // Links: [text](url)
  out = out.replace(
    /\[([^\]]+)\]\(([^)\s]+)\)/g,
    (_m, text, url) =>
      `<a href="${esc(safeUrl(url))}" rel="nofollow noopener noreferrer">${text}</a>`,
  );
  // Bold then italic (bold first so ** isn't eaten by the * rule).
  out = out.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  out = out.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  out = out.replace(/_([^_]+)_/g, "<em>$1</em>");

  // Restore code spans.
  out = out.replace(new RegExp(`${NUL}(\\d+)${NUL}`, "g"), (_m, i) => codes[Number(i)] ?? "");
  return out;
}

// --- Block rendering --------------------------------------------------------

const HEADING = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const FENCE = /^\s*(```|~~~)(.*)$/;
const UL_ITEM = /^\s*[-*+]\s+(.*)$/;
const OL_ITEM = /^\s*\d+[.)]\s+(.*)$/;
const BLOCKQUOTE = /^\s*>\s?(.*)$/;

/**
 * True for a thematic-break line: three or more of the SAME marker (- * _),
 * spaces allowed between them and nothing else. A function rather than a regex
 * because the "same marker" rule needs a backreference, which the Go port's RE2
 * engine lacks — so both implementations share this explicit form.
 */
function isHR(line: string): boolean {
  const compact = line.replace(/\s+/g, "");
  if (compact.length < 3) return false;
  const c = compact[0]!;
  if (c !== "-" && c !== "*" && c !== "_") return false;
  return [...compact].every((ch) => ch === c);
}

/**
 * Convert a full Markdown document to an HTML fragment (the body of the rendered
 * pane). Single level of list nesting; fenced code blocks are emitted verbatim
 * (escaped, no inline processing). Mirrors the Go RenderMarkdown.
 */
export function renderMarkdown(source: string): string {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const at = (n: number): string => lines[n] ?? "";
  const out: string[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = at(i);

    // Blank line — separates blocks; skip.
    if (line.trim() === "") {
      i++;
      continue;
    }

    // Fenced code block.
    const fence = FENCE.exec(line);
    if (fence) {
      const marker = fence[1]!;
      const lang = fence[2]!.trim().split(/\s+/)[0] ?? "";
      const code: string[] = [];
      i++;
      while (i < lines.length && !at(i).trimStart().startsWith(marker)) {
        code.push(at(i));
        i++;
      }
      if (i < lines.length) i++; // consume the closing fence
      const cls = lang ? ` class="language-${esc(lang)}"` : "";
      out.push(`<pre><code${cls}>${esc(code.join("\n"))}</code></pre>`);
      continue;
    }

    // Heading.
    const heading = HEADING.exec(line);
    if (heading) {
      const level = heading[1]!.length;
      out.push(`<h${level}>${renderInline(heading[2]!)}</h${level}>`);
      i++;
      continue;
    }

    // Horizontal rule.
    if (isHR(line)) {
      out.push("<hr>");
      i++;
      continue;
    }

    // Blockquote — consume consecutive `>` lines, then render their content.
    if (BLOCKQUOTE.test(line)) {
      const inner: string[] = [];
      while (i < lines.length && BLOCKQUOTE.test(at(i))) {
        inner.push(BLOCKQUOTE.exec(at(i))![1]!);
        i++;
      }
      out.push(`<blockquote>${renderMarkdown(inner.join("\n"))}</blockquote>`);
      continue;
    }

    // List (unordered or ordered) — consume consecutive item lines.
    if (UL_ITEM.test(line) || OL_ITEM.test(line)) {
      const ordered = OL_ITEM.test(line) && !UL_ITEM.test(line);
      const pattern = ordered ? OL_ITEM : UL_ITEM;
      const items: string[] = [];
      while (i < lines.length && pattern.test(at(i))) {
        items.push(`<li>${renderInline(pattern.exec(at(i))![1]!)}</li>`);
        i++;
      }
      const tag = ordered ? "ol" : "ul";
      out.push(`<${tag}>${items.join("")}</${tag}>`);
      continue;
    }

    // Paragraph — consume consecutive lines until a blank line or a block start.
    const para: string[] = [];
    while (
      i < lines.length &&
      at(i).trim() !== "" &&
      !FENCE.test(at(i)) &&
      !HEADING.test(at(i)) &&
      !isHR(at(i)) &&
      !BLOCKQUOTE.test(at(i)) &&
      !UL_ITEM.test(at(i)) &&
      !OL_ITEM.test(at(i))
    ) {
      para.push(at(i));
      i++;
    }
    out.push(`<p>${para.map(renderInline).join("<br>\n")}</p>`);
  }

  return out.join("\n");
}

// --- Full page --------------------------------------------------------------

/**
 * Inline script for the viewer: a Rendered⇄Raw toggle and a copy-to-clipboard
 * button. The raw source lives, HTML-escaped, in the hidden <pre id="md-raw">, so
 * its textContent is the exact original text — copied with the async Clipboard API
 * and a textarea/execCommand fallback for non-secure contexts.
 */
const VIEWER_SCRIPT = `(function(){
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
})();`;

/**
 * Render a complete, self-contained HTML page for a Markdown file: a sticky
 * toolbar (file name + Raw toggle + Copy), the rendered Markdown, and the raw
 * source in a hidden <pre>. Inline styles + one inline script only; every piece
 * of tenant content is escaped. Mirrors the Go RenderMarkdownPage.
 */
export function renderMarkdownPage(path: string, source: string): string {
  const name = baseName(path);
  const rendered = renderMarkdown(source);
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(name)}</title>
<style>
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body { font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; margin: 0; background: #fafafa; color: #1a1a1a; }
header { position: sticky; top: 0; z-index: 10; display: flex; align-items: center; gap: 0.75rem; min-height: 3.4rem; padding: 0.6rem 1.25rem; background: rgba(250,250,250,0.85); backdrop-filter: saturate(1.8) blur(8px); border-bottom: 1px solid #e3e3e3; }
header a.brand { display: inline-flex; align-items: center; gap: 0.45rem; text-decoration: none; color: inherit; font-size: 0.9rem; font-weight: 700; white-space: nowrap; }
header a.brand svg { width: 1.3rem; height: 1.3rem; display: block; }
header .sep { color: #c4c4c4; }
header .name { font-size: 0.85rem; font-weight: 500; color: #555; word-break: break-all; margin-right: auto; }
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
  header .sep { color: #41454d; }
  header .name { color: #9aa0a8; }
  .markdown-body a, header a.raw { color: #6ea8ff; }
  .markdown-body code { background: rgba(110,118,129,0.25); }
  .markdown-body pre, #md-raw { background: #1f2228; }
  .markdown-body blockquote { color: #9aa0a8; border-left-color: #2a2d34; }
}
</style>
</head>
<body>
<header>
<a class="brand" href="https://dropway.dev" target="_blank" rel="noopener noreferrer">
<svg viewBox="0 0 100 100" aria-hidden="true"><rect width="100" height="100" rx="18" fill="#5647e1"></rect><g transform="translate(17 17) scale(0.66)"><path fill="#ffffff" fill-rule="evenodd" d="M50 7 L85 55 L62 92 L38 92 L15 55 Z M50 7 L34 51 L46 58 Z"></path></g></svg>
<span>Dropway</span>
</a>
<span class="sep">/</span>
<span class="name">${esc(name)}</span>
<button type="button" id="md-toggle">View raw</button>
<button type="button" id="md-copy">Copy</button>
<a class="raw" href="?raw=1" title="Open the original source — append ?raw to the URL">Open raw ↗</a>
</header>
<main>
<article class="markdown-body" id="md-rendered">
${rendered}
</article>
<pre id="md-raw" hidden>${esc(source)}</pre>
</main>
<script>${VIEWER_SCRIPT}</script>
</body>
</html>
`;
}
