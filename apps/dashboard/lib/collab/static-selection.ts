import type { StaticSelection } from "./static-snapshot";

/** Parent-side range inspection; only called on the trusted reconstructed scriptless document. */
export function captureStaticText(doc: Document): StaticSelection | null {
  const selection = doc.getSelection();
  const root = doc.getElementById("review-content");
  if (!root || !selection || selection.rangeCount !== 1 || selection.isCollapsed) return null;
  const range = selection.getRangeAt(0);
  if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) return null;
  const startElement = range.startContainer.nodeType === 1 ? range.startContainer as Element : range.startContainer.parentElement;
  const block = startElement?.closest("[data-review-component]");
  const scope = block && block.contains(range.endContainer) ? block : root;
  const prefixRange = doc.createRange();
  prefixRange.selectNodeContents(scope);
  prefixRange.setEnd(range.startContainer, range.startOffset);
  const raw = range.toString();
  const quote = raw.trim();
  if (!quote || new TextEncoder().encode(quote).length > 2000) return null;
  const start = prefixRange.toString().length + raw.length - raw.trimStart().length;
  const end = start + quote.length;
  const text = scope.textContent ?? "";
  if (text.slice(start, end) !== quote) return null;
  return { kind: "text", label: quote, anchor: {
    quote, context_before: text.slice(Math.max(0, start - 100), start), context_after: text.slice(end, end + 100),
    start_offset: start, end_offset: end,
    ...(scope === root ? {} : { block_id: scope.getAttribute("data-review-component") }),
  } };
}

export function captureStaticPoint(doc: Document, event: MouseEvent): StaticSelection | null {
  const win = doc.defaultView;
  if (!win) return null;
  const target = event.target as HTMLImageElement | null;
  if (target?.tagName === "IMG" && target.naturalWidth > 0 && target.naturalHeight > 0) {
    const rect = target.getBoundingClientRect();
    const x = (event.clientX - rect.left) / rect.width;
    const y = (event.clientY - rect.top) / rect.height;
    if (rect.width <= 0 || rect.height <= 0 || x < 0 || x > 1 || y < 0 || y > 1) return null;
    return { kind: "point", label: "Image point in intrinsic pixels", anchor: {
      x, y, viewport: { width: win.innerWidth, height: win.innerHeight },
      rendition: { width: Math.max(1, Math.round(rect.width)), height: Math.max(1, Math.round(rect.height)) },
      scroll: { x: Math.round(win.scrollX), y: Math.round(win.scrollY) },
      image: { intrinsic_width: target.naturalWidth, intrinsic_height: target.naturalHeight,
        x: Math.round(x * target.naturalWidth), y: Math.round(y * target.naturalHeight) },
    } };
  }
  const width = Math.max(doc.documentElement.scrollWidth, doc.body.scrollWidth);
  const height = Math.max(doc.documentElement.scrollHeight, doc.body.scrollHeight);
  if (width <= 0 || height <= 0 || width > 100000 || height > 100000) return null;
  const x = (event.clientX + win.scrollX) / width;
  const y = (event.clientY + win.scrollY) / height;
  if (x < 0 || x > 1 || y < 0 || y > 1) return null;
  return { kind: "point", label: "Point on the static rendition", anchor: {
    x, y, viewport: { width: win.innerWidth, height: win.innerHeight },
    rendition: { width, height }, scroll: { x: Math.round(win.scrollX), y: Math.round(win.scrollY) },
  } };
}
