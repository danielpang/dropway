// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import "server-only";
import { createHash } from "node:crypto";
import { parse, serialize, type DefaultTreeAdapterMap } from "parse5";
import type { ReviewCandidate } from "./contract";

type Node = DefaultTreeAdapterMap["node"];
type Element = DefaultTreeAdapterMap["element"];
type Files = Map<string, Buffer>;
const mime: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  webp: "image/webp",
  woff: "font/woff",
  woff2: "font/woff2",
};
const ext = (path: string) => path.split(".").at(-1)?.toLowerCase() ?? "";
const esc = (s: string) =>
  s.replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ]!,
  );
const CSP =
  "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; media-src 'none'; base-uri 'none'; form-action 'none'";
export interface VisualComparison {
  candidateId: string;
  baseContentHash: string;
  contentHash: string;
  pages: {
    path: string;
    label: string;
    before: string | null;
    after: string | null;
  }[];
}
function files(value: unknown): Files {
  if (!Array.isArray(value) || value.length > 500)
    throw new Error("Invalid preview");
  let total = 0;
  return new Map(
    value.map((f) => {
      if (
        !f ||
        typeof f.path !== "string" ||
        typeof f.data !== "string" ||
        typeof f.sha256 !== "string" ||
        f.data.length > 2800000
      )
        throw new Error("Invalid preview");
      const data = Buffer.from(f.data, "base64");
      total += data.length;
      if (
        total > 10 * 1024 * 1024 ||
        data.toString("base64") !== f.data ||
        createHash("sha256").update(data).digest("hex") !== f.sha256
      )
        throw new Error("Invalid preview");
      return [f.path, data];
    }),
  );
}
function resolve(base: string, ref: string): string | null {
  try {
    const url = new URL(ref, `https://preview.invalid/${base}`);
    if (
      url.origin !== "https://preview.invalid" ||
      /[\\\u0000-\u001f]/.test(ref)
    )
      return null;
    return decodeURIComponent(url.pathname).slice(1);
  } catch {
    return null;
  }
}
function asset(
  all: Files,
  path: string | null,
  budget: { bytes: number },
): string {
  const bytes = path ? all.get(path) : null,
    type = path ? mime[ext(path)] : null;
  if (bytes) {
    budget.bytes += Math.ceil(bytes.length / 3) * 4;
    if (budget.bytes > 12 * 1024 * 1024) return "";
  }
  return bytes && type ? `data:${type};base64,${bytes.toString("base64")}` : "";
}
function css(
  all: Files,
  path: string,
  text: string,
  budget: { bytes: number },
  depth = 0,
): string {
  if (depth > 4) return "";
  budget.bytes += Buffer.byteLength(text);
  if (budget.bytes > 12 * 1024 * 1024) throw new Error("Preview too large");
  return text
    .replace(
      /@import\s+(?:url\(\s*)?["']([^"']+)["']\s*\)?\s*[^;]*;/gi,
      (_m, ref: string) => {
        const target = resolve(path, ref),
          bytes = target ? all.get(target) : null;
        return bytes && target && ext(target) === "css"
          ? css(all, target, bytes.toString("utf8"), budget, depth + 1)
          : "";
      },
    )
    .replace(
      /url\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*))\s*\)/gi,
      (_m, a, b, c) => {
        const ref = String(a ?? b ?? c).trim();
        if (
          /^data:(?:image\/(?:png|jpeg|webp)|font\/woff2?);base64,[A-Za-z0-9+/]+=*$/.test(
            ref,
          )
        ) {
          budget.bytes += ref.length;
          return `url("${budget.bytes <= 12 * 1024 * 1024 ? ref : ""}")`;
        }
        return `url("${asset(all, resolve(path, ref), budget)}")`;
      },
    )
    .replace(/</g, "\\3c ");
}
function document(all: Files, path: string): string | null {
  const data = all.get(path);
  if (!data) return null;
  let body: string;
  const budget = { bytes: 0 };
  if (mime[ext(path)]?.startsWith("image/")) {
    body = `<html><body style="margin:0;display:grid;place-items:center;min-height:100vh;background:#fff"><img alt="Image preview" style="max-width:100%;height:auto" src="${asset(all, path, budget)}"></body></html>`;
  } else {
    if (data.length > 512 * 1024) throw new Error("Page too large");
    const doc = parse(data.toString("utf8"));
    let visited = 0;
    const drop = new Set([
      "script",
      "iframe",
      "object",
      "embed",
      "base",
      "meta",
      "noscript",
      "template",
      "svg",
      "math",
    ]);
    function walk(node: Node, depth: number) {
      if (++visited > 20000 || depth > 80) throw new Error("Page too complex");
      if (!("childNodes" in node)) return;
      node.childNodes = node.childNodes.filter(
        (child) => !("tagName" in child && drop.has(child.tagName)),
      );
      for (const child of node.childNodes) {
        if ("tagName" in child) {
          const el = child as Element;
          const attr = (name: string) =>
            el.attrs.find((a) => a.name === name)?.value ?? "";
          if (el.tagName === "link") {
            const target = resolve(path, attr("href"));
            if (
              attr("rel").toLowerCase() === "stylesheet" &&
              target &&
              all.has(target) &&
              ext(target) === "css"
            ) {
              el.tagName = "style";
              el.nodeName = "style";
              el.childNodes = [
                {
                  nodeName: "#text",
                  value: css(
                    all,
                    target,
                    all.get(target)!.toString("utf8"),
                    budget,
                  ),
                  parentNode: el,
                },
              ];
            } else {
              el.tagName = "span";
              el.nodeName = "span";
            }
            el.attrs = [];
          } else if (el.tagName === "style") {
            for (const text of el.childNodes)
              if ("value" in text)
                text.value = css(all, path, text.value, budget);
          }
          el.attrs = el.attrs.flatMap((a) => {
            if (
              a.name.startsWith("on") ||
              [
                "href",
                "action",
                "formaction",
                "srcdoc",
                "srcset",
                "ping",
                "autofocus",
                "background",
                "poster",
                "xlink:href",
              ].includes(a.name)
            )
              return [];
            if (a.name === "src")
              return el.tagName === "img"
                ? [{ ...a, value: asset(all, resolve(path, a.value), budget) }]
                : [];
            if (a.name === "style")
              return [{ ...a, value: css(all, path, a.value, budget) }];
            return [a];
          });
          if (["input", "button", "select", "textarea"].includes(el.tagName))
            el.attrs.push({ name: "disabled", value: "" });
        }
        walk(child, depth + 1);
      }
    }
    walk(doc, 0);
    body = serialize(doc);
  }
  // The policy precedes every byte of uploaded markup. The frame also has an
  // empty sandbox: no scripts, same-origin access, forms, popups or navigation.
  return `<!doctype html><meta http-equiv="Content-Security-Policy" content="${esc(CSP)}"><meta name="referrer" content="no-referrer">${body}`;
}
export function buildVisualComparison(
  value: unknown,
  candidate: ReviewCandidate,
): VisualComparison {
  const row = value as Record<string, unknown>;
  if (
    !row ||
    row.candidate_id !== candidate.ID ||
    row.base_content_hash !== candidate.BaseContentHash ||
    row.content_hash !== candidate.ContentHash
  )
    throw new Error("Preview identity mismatch");
  const before = files(row.before),
    after = files(row.after);
  const changed = (path: string) =>
    !before.has(path) ||
    !after.has(path) ||
    !before.get(path)!.equals(after.get(path)!);
  const rank = (path: string) =>
    changed(path)
      ? /\.html?$/i.test(path)
        ? 0
        : 1
      : path === "index.html"
        ? 2
        : 3;
  const paths = [...new Set([...before.keys(), ...after.keys()])]
    .filter(
      (p) =>
        /\.html?$/i.test(p) || (/\.(png|jpe?g|webp)$/i.test(p) && changed(p)),
    )
    .sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
  // Render only the chosen page in the action, not every asset embedded into
  // every document. This also keeps image-heavy sites within response limits.
  return {
    candidateId: candidate.ID,
    baseContentHash: candidate.BaseContentHash,
    contentHash: candidate.ContentHash,
    pages: paths.map((path) => ({
      path,
      label:
        path === "index.html"
          ? "Home page"
          : /\.html?$/i.test(path)
            ? `/${path.replace(/index\.html?$/i, "")}`
            : path,
      before: null,
      after: null,
    })),
  };
}
export function renderComparisonPage(
  value: unknown,
  candidate: ReviewCandidate,
  path: string,
) {
  const comparison = buildVisualComparison(value, candidate);
  if (!comparison.pages.some((p) => p.path === path))
    throw new Error("Unknown page");
  const row = value as Record<string, unknown>;
  return {
    ...comparison,
    pages: comparison.pages,
    before: document(files(row.before), path),
    after: document(files(row.after), path),
    path,
  };
}
