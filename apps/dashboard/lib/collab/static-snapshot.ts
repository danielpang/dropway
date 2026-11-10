// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// The immutable identity travels outside the rendered content. Snapshot coordinates
// refer to this simplified rendition, never to source code or the live layout.
export interface StaticSnapshot {
  snapshotId: string;
  siteId: string;
  versionId: string;
  contentHash: string;
  page: string;
  renderer: "static-v1";
  html: string;
  omittedAssets: number;
}

export type StaticSelection = {
  kind: "point" | "text" | "component";
  anchor: Record<string, unknown>;
  label: string;
};

export function staticAssetPath(page: string, source: string): string | null {
  if (!source || source.length > 1024 || /[\u0000-\u0020\\]/.test(source) || /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(source)) return null;
  try {
    const base = new URL(page.startsWith("/") ? page : `/${page}`, "https://snapshot.invalid");
    const url = new URL(source, base);
    if (url.origin !== base.origin || url.search || url.hash) return null;
    const path = decodeURIComponent(url.pathname).slice(1);
    if (!path || /[\\\u0000-\u001f]/.test(path) || path.split("/").some((part) => part === ".." || part === ".")) return null;
    return path;
  } catch { return null; }
}

export function snapshotPagePath(page: string): string | null {
  if (page === "/") return "index.html";
  const path = staticAssetPath("/", page);
  return path?.endsWith("/") ? `${path}index.html` : path;
}
