/**
 * A small, dependency-free Markdown → HTML renderer for the skill editor's
 * preview pane. It is deliberately minimal (headings, emphasis, code, lists,
 * links, blockquotes, rules) — enough to preview a SKILL.md, not a full CommonMark
 * implementation — and it is XSS-safe by construction: the input is HTML-escaped
 * FIRST, then a fixed set of block/inline transforms are applied, so no raw HTML
 * in the source can reach the DOM. Only http(s) and relative link targets are
 * emitted; anything else (javascript:, data:) renders as plain text.
 */

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function safeHref(url: string): string | null {
  const trimmed = url.trim();
  if (/^https?:\/\//i.test(trimmed) || trimmed.startsWith("/") || trimmed.startsWith("#")) {
    return trimmed;
  }
  // Relative path without a scheme (e.g. references/foo.md) is fine too.
  if (!/^[a-z][a-z0-9+.-]*:/i.test(trimmed)) return trimmed;
  return null;
}

// Inline transforms applied to already-escaped text: code, bold, italic, links.
function renderInline(text: string): string {
  let out = text;
  // Inline code first so its contents aren't further transformed.
  out = out.replace(/`([^`]+)`/g, (_m, code) => `<code>${code}</code>`);
  out = out.replace(/\*\*([^*]+)\*\*/g, (_m, b) => `<strong>${b}</strong>`);
  out = out.replace(/(^|[^*])\*([^*]+)\*/g, (_m, pre, i) => `${pre}<em>${i}</em>`);
  out = out.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (m, label, url) => {
    const href = safeHref(url);
    return href ? `<a href="${href}" rel="noreferrer noopener nofollow" target="_blank">${label}</a>` : m;
  });
  return out;
}

/** Render a Markdown string to a safe HTML string for dangerouslySetInnerHTML. */
export function renderMarkdownToHtml(md: string): string {
  const escaped = escapeHtml(md.replace(/\r\n/g, "\n"));
  const lines = escaped.split("\n");
  const html: string[] = [];

  let inCode = false;
  let listType: "ul" | "ol" | null = null;
  let para: string[] = [];

  const flushPara = () => {
    if (para.length > 0) {
      html.push(`<p>${renderInline(para.join(" "))}</p>`);
      para = [];
    }
  };
  const closeList = () => {
    if (listType) {
      html.push(`</${listType}>`);
      listType = null;
    }
  };

  for (const line of lines) {
    if (line.trim().startsWith("```")) {
      flushPara();
      closeList();
      if (inCode) {
        html.push("</code></pre>");
        inCode = false;
      } else {
        html.push("<pre><code>");
        inCode = true;
      }
      continue;
    }
    if (inCode) {
      html.push(line + "\n");
      continue;
    }

    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      flushPara();
      closeList();
      const level = heading[1]!.length;
      html.push(`<h${level}>${renderInline(heading[2]!)}</h${level}>`);
      continue;
    }
    if (/^\s*([-*_])\1{2,}\s*$/.test(line)) {
      flushPara();
      closeList();
      html.push("<hr />");
      continue;
    }
    const quote = /^&gt;\s?(.*)$/.exec(line);
    if (quote) {
      flushPara();
      closeList();
      html.push(`<blockquote>${renderInline(quote[1]!)}</blockquote>`);
      continue;
    }
    const ul = /^\s*[-*]\s+(.*)$/.exec(line);
    const ol = /^\s*\d+\.\s+(.*)$/.exec(line);
    if (ul || ol) {
      flushPara();
      const want: "ul" | "ol" = ul ? "ul" : "ol";
      if (listType !== want) {
        closeList();
        html.push(`<${want}>`);
        listType = want;
      }
      html.push(`<li>${renderInline((ul ?? ol)![1]!)}</li>`);
      continue;
    }
    if (line.trim() === "") {
      flushPara();
      closeList();
      continue;
    }
    para.push(line.trim());
  }
  flushPara();
  closeList();
  if (inCode) html.push("</code></pre>");
  return html.join("\n");
}
