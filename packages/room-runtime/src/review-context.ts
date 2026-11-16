// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { parse, type DefaultTreeAdapterMap } from "parse5";
import type { HostedSource } from "./hosted-client.js";

// Must match static-v1's traversal in dashboard/lib/collab/static-renderer.ts.
// Generated review IDs do not exist in the original HTML.
const DROP = new Set("script style noscript template iframe object embed svg math meta link base head input textarea select button video audio source track canvas".split(" "));
type Node = DefaultTreeAdapterMap["node"];
function record(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}
function string(value: unknown): string { return typeof value === "string" ? value : ""; }
function pagePath(page: string): string {
  if (!page || page === "/") return "index.html";
  try {
    const path = decodeURIComponent(page.replace(/^\//, ""));
    return path.endsWith("/") ? `${path}index.html` : path;
  } catch { return ""; }
}

/** Bounded, valid JSON with target first; never silently cut an arbitrary JSON string. */
export function reviewContext(source: HostedSource): string {
  const thread = record(source.thread), anchor = record(thread.Anchor);
  const page = string(thread.Page), path = pagePath(page);
  const selectedID = string(anchor.block_id) || string(anchor.stable_id) || string(record(anchor.image).stable_id);
  const file = source.files.find(file => file.path === path && /\.html?$/i.test(file.path));
  let sourceExcerpt: unknown;
  if (file) {
    const html = Buffer.from(file.content_base64, "base64").toString("utf8");
    // Same page limit as the review renderer; do not parse arbitrary large files.
    if (Buffer.byteLength(html) <= 512 * 1024) {
      const doc = parse(html, { scriptingEnabled: false, sourceCodeLocationInfo: true });
      let count = 0, visited = 0;
      const ids = new Set<string>();
      let selected: Node | undefined;
      function visit(node: Node, depth: number) {
        if (++visited > 20000 || depth > 80) return;
        if ("tagName" in node) {
          if (node.namespaceURI !== "http://www.w3.org/1999/xhtml" || DROP.has(node.tagName)) return;
          if (node.tagName !== "html" && node.tagName !== "body") {
            const index = ++count;
            const original = node.attrs.find(attr => attr.name === "data-component-id")?.value ?? "";
            let id = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,120}$/.test(original) ? `static-v1:${original}` : `static-v1:node-${index}`;
            while (ids.has(id)) id += `-${index}`;
            ids.add(id);
            if (id === selectedID) selected = node;
          }
        }
        if ("childNodes" in node) for (const child of node.childNodes) visit(child, depth + 1);
      }
      visit(doc, 0);
      const location = selected && "sourceCodeLocation" in selected ? selected.sourceCodeLocation : undefined;
      const start = location?.startOffset ?? 0;
      const end = location?.endOffset ?? Math.min(html.length, 12000);
      sourceExcerpt = {
        path, targetResolved: Boolean(location), startLine: location?.startLine,
        selectedHTML: location ? html.slice(start, Math.min(end, start + 12000)) : undefined,
        surroundingHTML: html.slice(Math.max(0, start - 1500), Math.min(html.length, start + 16000)),
        excerptOnly: end - start > 12000 || html.length > 16000,
      };
    }
  }
  const replies = Array.isArray(thread.Replies) ? thread.Replies : [];
  // Preserve original request plus recent discussion; the current request is also
  // supplied separately, so large histories cannot displace the action to take.
  const retained = replies.length > 9 ? [replies[0], ...replies.slice(-8)] : replies;
  return JSON.stringify({
    page, anchorKind: thread.AnchorKind, anchor, anchorState: thread.AnchorState,
    sourceExcerpt,
    replies: retained.map(value => { const reply = record(value); return { author: reply.AuthorID, body: string(reply.Body).slice(0, 4000), createdAt: reply.CreatedAt }; }),
    historyOmitted: replies.length > retained.length || Boolean(thread.NextCursor),
  });
}
