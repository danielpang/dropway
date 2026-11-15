// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import "server-only";
import { createHash, randomUUID } from "node:crypto";
import { parse, type DefaultTreeAdapterMap } from "parse5";
import { staticAssetPath, type StaticSnapshot } from "./static-snapshot";

const ALLOWED = new Set(["article", "section", "main", "header", "footer", "nav", "aside", "div", "span", "p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "dl", "dt", "dd", "blockquote", "pre", "code", "strong", "em", "b", "i", "u", "s", "small", "sub", "sup", "br", "hr", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "figure", "figcaption"]);
const DROP = new Set(["script", "style", "noscript", "template", "iframe", "object", "embed", "svg", "math", "meta", "link", "base", "head", "input", "textarea", "select", "button", "video", "audio", "source", "track", "canvas"]);
const VOID = new Set(["br", "hr"]);
const CSS = "html{color-scheme:light;background:#fff;color:#17202a;font:16px/1.6 system-ui,sans-serif}body{margin:24px;overflow-wrap:anywhere}img{max-width:100%;height:auto}pre{white-space:pre-wrap}table{border-collapse:collapse}th,td{border:1px solid #bbb;padding:6px}.dw-selected{outline:3px solid #2563eb;outline-offset:2px}[data-review-component]:focus-visible{outline:3px solid #2563eb}";
const STYLE_HASH = createHash("sha256").update(CSS).digest("base64");
const CSP = `default-src 'none'; script-src 'none'; style-src 'sha256-${STYLE_HASH}'; img-src data:; font-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; media-src 'none'; base-uri 'none'; form-action 'none'`;
export const STATIC_IMAGE_MAX_BYTES = 2 * 1024 * 1024;
export const STATIC_IMAGES_TOTAL_MAX_BYTES = 4 * 1024 * 1024;

function escaped(value: string): string {
  return value.replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]!);
}

type Node = DefaultTreeAdapterMap["node"];
type Element = DefaultTreeAdapterMap["element"];
function isElement(node: Node): node is Element { return "tagName" in node; }
function attr(node: Element, name: string): string { return node.attrs.find((item) => item.name === name)?.value ?? ""; }

/** No browser parser sees the original bytes. Unknown markup is reconstructed as inert text/semantic tags. */
export function prepareStaticSnapshot(html: string, page: string): { render: (images: Map<string, string>, target: Omit<StaticSnapshot, "html" | "omittedAssets" | "renderer" | "snapshotId">) => StaticSnapshot; imagePaths: string[] } {
  if (Buffer.byteLength(html, "utf8") > 512 * 1024) throw new Error("This page is too large for static review.");
  const document = parse(html, { scriptingEnabled: false });
  const paths = new Set<string>();
  let visited = 0;
  function collect(node: Node, depth: number) {
    if (++visited > 20000 || depth > 80) throw new Error("This page is too complex for static review.");
    if (isElement(node)) {
      if (node.namespaceURI !== "http://www.w3.org/1999/xhtml" || DROP.has(node.tagName)) return;
      if (node.tagName === "img") {
        const path = staticAssetPath(page, attr(node, "src"));
        if (path && paths.size < 12) paths.add(path);
      }
    }
    if ("childNodes" in node) for (const child of node.childNodes) collect(child, depth + 1);
  }
  collect(document, 0);
  return { imagePaths: [...paths], render(images, target) {
    let count = 0;
    let omittedAssets = 0;
    let embeddedImageBytes = 0;
    const ids = new Set<string>();
    function renderNode(node: Node): string {
      if (node.nodeName === "#text" && "value" in node) return escaped(node.value);
      if (!isElement(node)) return "childNodes" in node ? node.childNodes.map(renderNode).join("") : "";
      if (node.namespaceURI !== "http://www.w3.org/1999/xhtml" || DROP.has(node.tagName)) return "";
      const children = () => node.childNodes.map(renderNode).join("");
      if (node.tagName === "html" || node.tagName === "body") return children();
      const index = ++count;
      const sourceID = attr(node, "data-component-id");
      const proposed = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,120}$/.test(sourceID) ? `static-v1:${sourceID}` : `static-v1:node-${index}`;
      let id = proposed;
      while (ids.has(id)) id += `-${index}`;
      ids.add(id);
      const attributes = ` data-review-component="${escaped(id)}"`;
      if (node.tagName === "img") {
        const path = staticAssetPath(page, attr(node, "src"));
        const data = path ? images.get(path) : undefined;
        const alt = attr(node, "alt").slice(0, 500);
        // Images are produced only by verifiedRasterDataURL, never from document attributes.
        if (data && data.length <= Math.ceil(STATIC_IMAGE_MAX_BYTES / 3) * 4 + 32 && /^data:image\/(?:png|jpeg|webp);base64,[A-Za-z0-9+/]+=*$/.test(data)) {
          const payload = data.slice(data.indexOf(",") + 1);
          const bytes = payload.length * 3 / 4 - (payload.endsWith("==") ? 2 : payload.endsWith("=") ? 1 : 0);
          // Count each embedded occurrence, not just unique paths. A hostile
          // page can repeat a large image thousands of times within 512 KiB.
          if (bytes <= STATIC_IMAGE_MAX_BYTES && embeddedImageBytes + bytes <= STATIC_IMAGES_TOTAL_MAX_BYTES) {
            embeddedImageBytes += bytes;
            return `<img${attributes} src="${data}" alt="${escaped(alt)}">`;
          }
        }
        omittedAssets++;
        return `<span${attributes}>[Image unavailable${alt ? `: ${escaped(alt)}` : ""}]</span>`;
      }
      const tag = ALLOWED.has(node.tagName) ? node.tagName : "span";
      return `<${tag}${attributes}>${VOID.has(tag) ? "" : `${children()}</${tag}>`}`;
    }
    const body = renderNode(document);
    const snapshotId = randomUUID();
    return { ...target, renderer: "static-v1", snapshotId, omittedAssets,
      html: `<!doctype html><html data-review-snapshot="${snapshotId}"><head><meta http-equiv="Content-Security-Policy" content="${escaped(CSP)}"><meta name="referrer" content="no-referrer"><style>${CSS}</style></head><body><main id="review-content">${body}</main></body></html>` };
  } };
}

export function verifiedRasterDataURL(bytes: Buffer, contentType: string): string | null {
  if (bytes.length > STATIC_IMAGE_MAX_BYTES || bytes.length < 12) return null;
  const mime = contentType.split(";")[0]!.trim().toLowerCase();
  const valid = mime === "image/png" ? bytes.subarray(0, 8).equals(Buffer.from([137,80,78,71,13,10,26,10]))
    : mime === "image/jpeg" ? bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255
      : mime === "image/webp" ? bytes.toString("ascii", 0, 4) === "RIFF" && bytes.toString("ascii", 8, 12) === "WEBP" : false;
  return valid ? `data:${mime};base64,${bytes.toString("base64")}` : null;
}
