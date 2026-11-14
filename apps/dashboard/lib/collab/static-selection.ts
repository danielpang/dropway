import type { StaticSelection } from "./static-snapshot";

export interface StaticAnnotation {
  id: string;
  kind: string;
  anchor: unknown;
  label?: string;
}

/** Resolve against this exact snapshot, never by fuzzy quote matching. Rendering
 * overlays outside review-content keeps these stored text offsets unchanged. */
export function staticAnnotationRects(doc: Document, annotation: StaticAnnotation): DOMRect[] {
  const root = doc.getElementById("review-content");
  const anchor = annotation.anchor && typeof annotation.anchor === "object" ? annotation.anchor as Record<string, unknown> : null;
  if (!root || !anchor) return [];
  const component = (id: unknown) => typeof id === "string"
    ? [...root.querySelectorAll("[data-review-component]")].find((el) => el.getAttribute("data-review-component") === id)
    : undefined;
  if (annotation.kind === "component") {
    const target = component(anchor.stable_id);
    return target ? [...target.getClientRects()] : [];
  }
  if (annotation.kind === "text") {
    const scope = anchor.block_id == null ? root : component(anchor.block_id);
    const start = anchor.start_offset, end = anchor.end_offset;
    if (!scope || typeof start !== "number" || typeof end !== "number" || !Number.isInteger(start) || !Number.isInteger(end)
      || start < 0 || end <= start || typeof anchor.quote !== "string" || (scope.textContent ?? "").slice(start, end) !== anchor.quote) return [];
    const walker = doc.createTreeWalker(scope, 4 /* SHOW_TEXT */);
    let offset = 0, node = walker.nextNode(), startNode: Node | null = null, endNode: Node | null = null, startOffset = 0, endOffset = 0;
    while (node) {
      const length = node.textContent?.length ?? 0;
      if (!startNode && start < offset + length) { startNode = node; startOffset = start - offset; }
      if (startNode && end <= offset + length) { endNode = node; endOffset = end - offset; break; }
      offset += length;
      node = walker.nextNode();
    }
    if (!startNode || !endNode) return [];
    const range = doc.createRange();
    range.setStart(startNode, startOffset);
    range.setEnd(endNode, endOffset);
    return [...range.getClientRects()];
  }
  if (annotation.kind === "point" && anchor.image && typeof anchor.image === "object") {
    const image = anchor.image as Record<string, unknown>;
    const element = component(image.stable_id) as HTMLImageElement | undefined;
    if (!element || element.tagName !== "IMG" || element.naturalWidth !== image.intrinsic_width || element.naturalHeight !== image.intrinsic_height) return [];
    const x = image.x, y = image.y;
    if (typeof x !== "number" || typeof y !== "number" || !Number.isFinite(x) || !Number.isFinite(y)
      || element.naturalWidth <= 0 || element.naturalHeight <= 0 || x < 0 || x > element.naturalWidth || y < 0 || y > element.naturalHeight) return [];
    const imageRect = element.getBoundingClientRect();
    if (imageRect.width <= 0 || imageRect.height <= 0) return [];
    const left = imageRect.left + imageRect.width * x / element.naturalWidth;
    const top = imageRect.top + imageRect.height * y / element.naturalHeight;
    return [{ x: left, y: top, left, top, right: left + 1, bottom: top + 1, width: 1, height: 1, toJSON: () => ({ left, top }) } as DOMRect];
  }
  if (annotation.kind === "point" && !anchor.image) {
    const x = anchor.x, y = anchor.y;
    const rendition = anchor.rendition as { width?: unknown; height?: unknown } | undefined;
    const width = Math.max(doc.documentElement.scrollWidth, doc.body.scrollWidth);
    const height = Math.max(doc.documentElement.scrollHeight, doc.body.scrollHeight);
    // Point coordinates describe one rendition, not an arbitrary responsive
    // reflow. Only display them when that rendition's dimensions still match.
    if (typeof x !== "number" || typeof y !== "number" || !Number.isFinite(x) || !Number.isFinite(y) || x < 0 || x > 1 || y < 0 || y > 1
      || rendition?.width !== width || rendition?.height !== height) return [];
    const left = x * width - (doc.defaultView?.scrollX ?? 0), top = y * height - (doc.defaultView?.scrollY ?? 0);
    return [{ x: left, y: top, left, top, right: left + 1, bottom: top + 1, width: 1, height: 1, toJSON: () => ({ left, top }) } as DOMRect];
  }
  return [];
}

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
        x: Math.round(x * target.naturalWidth), y: Math.round(y * target.naturalHeight),
        ...(target.getAttribute("data-review-component") ? { stable_id: target.getAttribute("data-review-component") } : {}) },
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
