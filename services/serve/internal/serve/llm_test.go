// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/edgetoken"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/serve/internal/serve"
)

// --- robots.txt -------------------------------------------------------------

func TestLLM_PublicRobotsAllowsAll(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{{path: "index.html", body: []byte("<h1>home</h1>"), contentType: "text/html; charset=utf-8"}})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/robots.txt", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("public /robots.txt = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Allow: /") || strings.Contains(body, "Disallow: /") {
		t.Errorf("public robots.txt should allow all; got %q", body)
	}
}

func TestLLM_GatedRobotsDisallowsAll(t *testing.T) {
	store := storage.NewFake()
	// No signer needed: robots.txt is served before the gated auth dispatch.
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModeOrgOnly)}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/robots.txt", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("gated /robots.txt = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Disallow: /") {
		t.Errorf("gated robots.txt should disallow all; got %q", body)
	}
}

// --- AI-crawler gating ------------------------------------------------------

func TestLLM_GatedBlocksAICrawler(t *testing.T) {
	store := storage.NewFake()
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModeOrgOnly)}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", map[string]string{"User-Agent": "Mozilla/5.0 (compatible; GPTBot/1.1; +https://openai.com/gptbot)"}, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("gated site + AI crawler = %d, want 403", rec.Code)
	}
}

func TestLLM_PublicAllowsAICrawler(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{{path: "index.html", body: []byte("<h1>home</h1>"), contentType: "text/html; charset=utf-8"}})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", map[string]string{"User-Agent": "ClaudeBot/1.0 (+https://www.anthropic.com)"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("public site + AI crawler = %d, want 200 (crawlers welcome on public)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "home") {
		t.Errorf("expected content served to crawler on public site; got %q", rec.Body.String())
	}
}

func TestLLM_GatedCrawlerBlockedOnLLMsTxt(t *testing.T) {
	store := storage.NewFake()
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModeOrgOnly)}}, store, nil, nil)

	// A crawler must not be able to discover a gated site's pages via /llms.txt.
	rec := doRequest(h, http.MethodGet, testHost, "/llms.txt", map[string]string{"User-Agent": "CCBot/2.0"}, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("gated /llms.txt + crawler = %d, want 403", rec.Code)
	}
}

// --- /llms.txt --------------------------------------------------------------

func TestLLM_PublicServesLLMsTxt(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("<h1>home</h1>"), contentType: "text/html; charset=utf-8"},
		{path: "about.html", body: []byte("<h1>about</h1>"), contentType: "text/html; charset=utf-8"},
		{path: "docs/index.html", body: []byte("<h1>docs</h1>"), contentType: "text/html; charset=utf-8"},
		{path: "style.css", body: []byte("body{}"), contentType: "text/css"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/llms.txt", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("public /llms.txt = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "# "+testHost) {
		t.Errorf("llms.txt should start with an H1 of the host; got %q", body[:min(40, len(body))])
	}
	// HTML pages are listed with pretty URLs; non-HTML assets are not.
	for _, want := range []string{"https://" + testHost + "/", "https://" + testHost + "/about", "https://" + testHost + "/docs/"} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt missing page link %q; got:\n%s", want, body)
		}
	}
	if strings.Contains(body, "style.css") {
		t.Errorf("llms.txt should not list non-HTML assets; got:\n%s", body)
	}
}

func TestLLM_GatedLLMsTxtNotGenerated(t *testing.T) {
	store := storage.NewFake()
	// Gated site, normal (non-crawler) UA: /llms.txt is NOT generated; it falls through
	// to the gated flow → 302 to /authz (no nil-verifier serve).
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModeOrgOnly)}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/llms.txt", nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("gated /llms.txt (normal UA) = %d, want 302 to /authz", rec.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
