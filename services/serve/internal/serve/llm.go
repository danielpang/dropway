// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve

import (
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/serve/internal/manifest"
	"github.com/danielpang/dropway/services/serve/internal/route"
	"github.com/danielpang/dropway/services/serve/internal/servehttp"
)

// LLM-access friendliness (README "LLM-friendly access"):
//
//   - PUBLIC sites welcome AI crawlers, serve a permissive robots.txt, and expose a
//     generated /llms.txt index of their pages so agents can discover + read them.
//   - GATED sites (password / allowlist / org_only) stay off-limits to crawlers:
//     robots.txt disallows everything AND known AI user-agents are 403'd at the edge
//     (belt-and-suspenders with the normal 302→/authz gate). Their content is reachable
//     by LLMs only through the authenticated Dropway MCP server, never by crawling.
//
// This runs AFTER host resolution + org-status (so access_mode is known) and BEFORE
// the public/gated content dispatch.

// aiCrawlerUAs are case-insensitive substrings that identify AI / LLM crawler and
// fetcher user-agents. Matching is intentionally broad: on a GATED site we fail
// closed (block) for anything that looks like an AI crawler.
var aiCrawlerUAs = []string{
	"gptbot",            // OpenAI crawler
	"oai-searchbot",     // OpenAI SearchBot
	"chatgpt-user",      // ChatGPT browsing
	"claudebot",         // Anthropic crawler
	"claude-web",        // Anthropic
	"anthropic-ai",      // Anthropic
	"perplexitybot",     // Perplexity
	"perplexity-user",   // Perplexity user fetch
	"ccbot",             // Common Crawl (LLM training corpus)
	"google-extended",   // Google Gemini training token
	"googleother",       // Google non-search fetcher
	"bytespider",        // ByteDance / Doubao
	"amazonbot",         // Amazon (Alexa/LLM)
	"applebot-extended", // Apple AI training token
	"meta-externalagent",
	"meta-externalfetcher",
	"facebookbot",
	"cohere-ai",
	"diffbot",
	"timpibot",
	"omgili",
	"youbot",
}

// isAICrawler reports whether a User-Agent looks like an AI/LLM crawler or fetcher.
func isAICrawler(ua string) bool {
	if ua == "" {
		return false
	}
	ua = strings.ToLower(ua)
	for _, needle := range aiCrawlerUAs {
		if strings.Contains(ua, needle) {
			return true
		}
	}
	return false
}

// serveLLMMeta handles the LLM-access surface for a resolved route: robots.txt,
// AI-crawler gating, and the generated /llms.txt. Returns true when it has written
// the response (the caller must then stop). Returns false to fall through to the
// normal content dispatch.
func (h *Handler) serveLLMMeta(w http.ResponseWriter, r *http.Request, rt *Route, host string) bool {
	clean, ok := route.CleanPath(pathOf(r))
	if !ok {
		// Unsafe path — let the normal flow fail closed (404).
		return false
	}
	public := rt.AccessMode == projection.AccessPublic

	// robots.txt is served to EVERYONE (including crawlers) so they learn the rules —
	// permissive on public sites, "Disallow: /" on gated ones.
	if clean == "robots.txt" {
		h.serveRobots(w, r, public)
		return true
	}

	// AI-crawler gate: on a non-public site, refuse known AI user-agents outright
	// (403) rather than bouncing them through /authz. Public sites welcome crawlers.
	if !public && isAICrawler(r.Header.Get("User-Agent")) {
		h.blockedCrawler(w, r)
		return true
	}

	// /llms.txt index — public sites only. On a gated site it is treated like any
	// other content (gated/404), never exposed to discovery.
	if clean == "llms.txt" && public {
		h.serveLLMsTxt(w, r, rt, host)
		return true
	}

	return false
}

// serveRobots writes a generated robots.txt. Public sites allow all crawlers; gated
// sites disallow everything.
func (h *Handler) serveRobots(w http.ResponseWriter, r *http.Request, public bool) {
	var body string
	hd := w.Header()
	servehttp.ApplyHeaders(hd, servehttp.ContentSecurityHeaders())
	hd.Set("Content-Type", "text/plain; charset=utf-8")
	if public {
		body = "User-agent: *\nAllow: /\n"
		hd.Set("Cache-Control", "public, max-age=3600")
	} else {
		body = "User-agent: *\nDisallow: /\n"
		hd.Set("Cache-Control", "private, no-store, max-age=0, must-revalidate")
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// blockedCrawler refuses an AI crawler on a gated site with a 403 (no-store).
func (h *Handler) blockedCrawler(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	servehttp.ApplyHeaders(hd, servehttp.ContentSecurityHeaders())
	hd.Set("Content-Type", "text/plain; charset=utf-8")
	hd.Set("Cache-Control", "private, no-store, max-age=0, must-revalidate")
	hd.Set("Vary", "User-Agent")
	w.WriteHeader(http.StatusForbidden)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, "403 Forbidden: this site is not public; AI crawlers are not permitted. Use the Dropway MCP server for authorized access.\n")
	}
}

// serveLLMsTxt generates and writes the /llms.txt index for a public site from its
// deploy manifest. Fails closed to 404 if the manifest can't be loaded.
func (h *Handler) serveLLMsTxt(w http.ResponseWriter, r *http.Request, rt *Route, host string) {
	m, ok := h.loadManifest(r.Context(), rt)
	if !ok {
		h.notFound(w, r, rt, nil, false)
		return
	}
	body := generateLLMsTxt(host, m, func(p string) string { return h.contentURL(host, p) })

	hd := w.Header()
	servehttp.ApplyHeaders(hd, servehttp.ContentSecurityHeaders())
	hd.Set("Content-Type", "text/plain; charset=utf-8")
	hd.Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// generateLLMsTxt builds an llms.txt-format index (llmstxt.org): an H1 title, a short
// blockquote summary, then a "## Pages" list linking every HTML page in the manifest.
// urlFor maps an absolute same-host path ("/about") to a full URL.
func generateLLMsTxt(host string, m manifest.Manifest, urlFor func(path string) string) string {
	pages := make([]string, 0, len(m.Files))
	for path := range m.Files {
		if path == manifest.NotFoundPath {
			continue
		}
		if strings.HasSuffix(path, ".html") {
			pages = append(pages, path)
		}
	}
	sort.Strings(pages)

	var b strings.Builder
	b.WriteString("# " + host + "\n\n")
	b.WriteString("> Pages published on this site via Dropway. This index helps LLMs and agents discover the public content available here.\n\n")
	b.WriteString("## Pages\n")
	for _, p := range pages {
		urlPath := prettyURLPath(p)
		b.WriteString("- [" + urlPath + "](" + urlFor(urlPath) + ")\n")
	}
	return b.String()
}

// prettyURLPath turns a manifest HTML key into the clean URL path the serve layer
// resolves it under: "index.html" → "/", "blog/index.html" → "/blog/",
// "about.html" → "/about", anything else → "/<path>".
func prettyURLPath(p string) string {
	switch {
	case p == "index.html":
		return "/"
	case strings.HasSuffix(p, "/index.html"):
		return "/" + strings.TrimSuffix(p, "index.html")
	case strings.HasSuffix(p, ".html"):
		return "/" + strings.TrimSuffix(p, ".html")
	default:
		return "/" + p
	}
}
