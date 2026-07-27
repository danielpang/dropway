// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/edgetoken"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/serve/internal/serve"
)

func stagePublicIndex(t *testing.T, store *storage.Fake, body string) {
	t.Helper()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte(body), contentType: "text/html; charset=utf-8"},
	})
}

// --- Public embed: framable, chrome-stripped, no badge ----------------------

func TestEmbed_PublicIsFramable(t *testing.T) {
	store := storage.NewFake()
	stagePublicIndex(t, store, "<h1>hello</h1>")
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/?embed=1", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("public embed should 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "<h1>hello</h1>" {
		t.Errorf("body = %q, want the site content", rec.Body.String())
	}
	// X-Frame-Options must be dropped and the CSP widened to frame-ancestors *.
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options = %q, want empty (embed is framable)", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors *") {
		t.Errorf("CSP = %q, want frame-ancestors *", csp)
	}
	if strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP still forbids framing: %q", csp)
	}
	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Errorf("CORP = %q, want cross-origin", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff header dropped: %q", got)
	}
	// The Go self-host serve injects NO Dropway attribution badge.
	if strings.Contains(rec.Body.String(), "Powered by Dropway") {
		t.Errorf("self-host embed must not inject a Dropway badge")
	}
}

func TestEmbed_NonEmbedStaysUnframable(t *testing.T) {
	store := storage.NewFake()
	stagePublicIndex(t, store, "<h1>hello</h1>")
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("non-embed X-Frame-Options = %q, want DENY", got)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("non-embed CSP should forbid framing")
	}
}

// --- Gated embed: placeholder, never the bytes ------------------------------

func TestEmbed_GatedShowsPlaceholderNotBytes(t *testing.T) {
	for _, mode := range []string{edgetoken.ModePassword, edgetoken.ModeAllowlist, edgetoken.ModeOrgOnly} {
		store := storage.NewFake()
		stageVersion(t, store, []fileSpec{
			{path: "index.html", body: []byte("SECRET"), contentType: "text/html; charset=utf-8"},
		})
		h, _ := gatedHandler(t, mode, store)

		rec := doRequest(h, http.MethodGet, testHost, "/?embed=1", nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("mode %s: gated embed should 200 the placeholder, got %d", mode, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Sign in to view") {
			t.Errorf("mode %s: placeholder missing CTA; body=%s", mode, body)
		}
		if strings.Contains(body, "SECRET") {
			t.Errorf("mode %s: gated embed leaked tenant content", mode)
		}
		// Framable so it renders inside the parent iframe, and never a 302.
		if got := rec.Header().Get("X-Frame-Options"); got != "" {
			t.Errorf("mode %s: placeholder X-Frame-Options = %q, want empty", mode, got)
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors *") {
			t.Errorf("mode %s: placeholder must be framable", mode)
		}
		if got := rec.Header().Get("Location"); got != "" {
			t.Errorf("mode %s: gated embed must not 302 (got Location %q)", mode, got)
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Errorf("mode %s: placeholder Cache-Control should be no-store", mode)
		}
		// Links back to the site root (for sign-in) without ?embed=1.
		if !strings.Contains(body, `href="https://`+testHost+`/"`) {
			t.Errorf("mode %s: placeholder should link to the site root; body=%s", mode, body)
		}
	}
}

// --- Failure pages inside an embed are framable ------------------------------
//
// A framing-blocked response renders as a BLANK iframe in Notion/Linear/etc., so
// under ?embed=1 even the failure pages (404/410) must be framable to say what's
// wrong. Outside an embed they stay unframable (clickjacking defense).

func assertFramable(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("%s: X-Frame-Options = %q, want empty (must be framable)", what, got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors *") {
		t.Errorf("%s: CSP = %q, want frame-ancestors *", what, csp)
	}
	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Errorf("%s: CORP = %q, want cross-origin", what, got)
	}
}

func TestEmbed_UnknownHost404IsFramable(t *testing.T) {
	h := newHandler(fakeResolver{map[string]serve.Route{}}, storage.NewFake(), nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/?embed=1", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown host embed should 404, got %d", rec.Code)
	}
	assertFramable(t, rec, "unknown-host 404")

	// Control: without ?embed the 404 stays unframable.
	rec = doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("non-embed 404 X-Frame-Options = %q, want DENY", got)
	}
}

func TestEmbed_MissingPath404IsFramable(t *testing.T) {
	store := storage.NewFake()
	stagePublicIndex(t, store, "<h1>hello</h1>")
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/nope.html?embed=1", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing path embed should 404, got %d", rec.Code)
	}
	assertFramable(t, rec, "missing-path 404")
}

func TestEmbed_ExpiredLink410IsFramable(t *testing.T) {
	store := storage.NewFake()
	stagePublicIndex(t, store, "<h1>hello</h1>")
	rt := publicRoute()
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rt.ExpiresAt = &past
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: rt}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/?embed=1", nil, "")
	if rec.Code != http.StatusGone {
		t.Fatalf("expired link embed should 410, got %d", rec.Code)
	}
	assertFramable(t, rec, "expired 410")
}

// A valid edge cookie does NOT unlock content in an embed — the embed is always the
// placeholder for a gated site (MVP: no in-frame authenticated serving).
func TestEmbed_GatedIgnoresValidCookie(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("SECRET"), contentType: "text/html; charset=utf-8"},
	})
	h, s := gatedHandler(t, edgetoken.ModePassword, store)
	tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, 60_000_000_000) // 1m in ns

	rec := doRequest(h, http.MethodGet, testHost, "/?embed=1", nil, tok)
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Errorf("gated embed must show the placeholder even with a valid cookie")
	}
	if !strings.Contains(rec.Body.String(), "Sign in to view") {
		t.Errorf("gated embed should show the placeholder; body=%s", rec.Body.String())
	}
}
