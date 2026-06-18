// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// LLM-friendly access for the serving Worker — the Cloudflare parity of the Go
// services/serve `llm.go`:
//
//   - PUBLIC sites welcome AI crawlers, serve a permissive robots.txt, and expose a
//     generated /llms.txt index of their pages so agents can discover + read them.
//   - GATED sites (password / allowlist / org_only) stay off-limits to crawlers:
//     robots.txt disallows everything AND known AI user-agents are 403'd at the edge.
//     Their content is reachable by LLMs only through the authenticated Dropway MCP
//     server, never by crawling.

import { type Manifest, NOT_FOUND_PATH } from "./manifest";

// AI / LLM crawler + fetcher user-agent substrings (lowercase, matched broadly: on a
// GATED site we fail closed for anything that looks like an AI crawler). Kept in sync
// with services/serve/internal/serve/llm.go.
const AI_CRAWLER_UAS = [
  "gptbot",
  "oai-searchbot",
  "chatgpt-user",
  "claudebot",
  "claude-web",
  "anthropic-ai",
  "perplexitybot",
  "perplexity-user",
  "ccbot",
  "google-extended",
  "googleother",
  "bytespider",
  "amazonbot",
  "applebot-extended",
  "meta-externalagent",
  "meta-externalfetcher",
  "facebookbot",
  "cohere-ai",
  "diffbot",
  "timpibot",
  "omgili",
  "youbot",
];

/** isAICrawler reports whether a User-Agent looks like an AI/LLM crawler or fetcher. */
export function isAICrawler(ua: string | null): boolean {
  if (!ua) return false;
  const lower = ua.toLowerCase();
  return AI_CRAWLER_UAS.some((needle) => lower.includes(needle));
}

/** robotsTxtBody: permissive on public sites, "Disallow: /" on gated ones. */
export function robotsTxtBody(isPublic: boolean): string {
  return isPublic ? "User-agent: *\nAllow: /\n" : "User-agent: *\nDisallow: /\n";
}

/** The 403 body served to AI crawlers on gated sites. */
export const CRAWLER_BLOCKED_BODY =
  "403 Forbidden: this site is not public; AI crawlers are not permitted. Use the Dropway MCP server for authorized access.\n";

/**
 * llmsTxtBody builds an llms.txt-format index (llmstxt.org): an H1 title, a short
 * blockquote summary, then a "## Pages" list linking every HTML page in the manifest.
 * `origin` is the request origin (e.g. "https://acme.dropwaycontent.com").
 */
export function llmsTxtBody(host: string, manifest: Manifest, origin: string): string {
  const pages = Object.keys(manifest.files)
    .filter((p) => p !== NOT_FOUND_PATH && p.endsWith(".html"))
    .sort();

  let out = `# ${host}\n\n`;
  out +=
    "> Pages published on this site via Dropway. This index helps LLMs and agents discover the public content available here.\n\n";
  out += "## Pages\n";
  for (const p of pages) {
    const urlPath = prettyUrlPath(p);
    out += `- [${urlPath}](${origin}${urlPath})\n`;
  }
  return out;
}

/**
 * prettyUrlPath turns a manifest HTML key into the clean URL path the serve layer
 * resolves it under: "index.html" → "/", "blog/index.html" → "/blog/",
 * "about.html" → "/about", anything else → "/<path>".
 */
function prettyUrlPath(p: string): string {
  if (p === "index.html") return "/";
  if (p.endsWith("/index.html")) return "/" + p.slice(0, -"index.html".length);
  if (p.endsWith(".html")) return "/" + p.slice(0, -".html".length);
  return "/" + p;
}
