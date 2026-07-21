// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/edgerevoke"
	"github.com/danielpang/dropway/internal/edgetoken"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/serve/internal/edgeverify"
	"github.com/danielpang/dropway/services/serve/internal/ratelimit"
	"github.com/danielpang/dropway/services/serve/internal/serve"
)

// --- Test fixtures ----------------------------------------------------------

const (
	testHost      = "acme.dropwaycontent.com"
	otherHost     = "evil.dropwaycontent.com"
	testOrgID     = "11111111-1111-1111-1111-111111111111"
	testSiteID    = "22222222-2222-2222-2222-222222222222"
	otherSiteID   = "33333333-3333-3333-3333-333333333333"
	testVersionID = "44444444-4444-4444-4444-444444444444"
)

// fakeResolver maps a normalized host → Route, returning ErrHostNotFound otherwise.
type fakeResolver struct {
	routes map[string]serve.Route
}

func (f fakeResolver) Resolve(_ context.Context, host string) (serve.Route, error) {
	rt, ok := f.routes[host]
	if !ok {
		return serve.Route{}, serve.ErrHostNotFound
	}
	return rt, nil
}

// fakeRevoked is an in-memory revocation reader. minIATs maps "kind:id" → min_iat.
// errOn forces a read error for a given key (to exercise fail-closed).
type fakeRevoked struct {
	minIATs map[string]int64
	errOn   map[string]bool
}

func (f fakeRevoked) MinIAT(_ context.Context, kind edgerevoke.Kind, id string) (int64, bool, error) {
	key := string(kind) + ":" + id
	if f.errOn[key] {
		return 0, false, fmt.Errorf("revocation read error")
	}
	v, ok := f.minIATs[key]
	return v, ok, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// buildManifest stages blobs in the fake store and returns the manifest JSON for
// a single version, mapping each path → {sha256, content_type}.
type fileSpec struct {
	path        string
	body        []byte
	contentType string
}

func stageVersion(t *testing.T, store *storage.Fake, files []fileSpec) []byte {
	t.Helper()
	manifestFiles := map[string]map[string]any{}
	for _, f := range files {
		sha := sha256Hex(f.body)
		if err := store.PutBlobBytes(context.Background(), testOrgID, sha, f.body); err != nil {
			t.Fatalf("PutBlobBytes: %v", err)
		}
		manifestFiles[f.path] = map[string]any{
			"sha256":       sha,
			"content_type": f.contentType,
			"size":         len(f.body),
		}
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"files":          manifestFiles,
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := store.PutManifest(context.Background(), testOrgID, testSiteID, testVersionID, raw); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	return raw
}

// testSigner returns a fresh edge signer (a dedicated Ed25519 keypair).
func testSigner(t *testing.T) *edgetoken.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s, err := edgetoken.NewSigner(priv)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

// mint produces an edge token bound to the given host/site/mode with the given TTL.
func mint(t *testing.T, s *edgetoken.Signer, host, siteID, mode string, ttl time.Duration) string {
	t.Helper()
	sub := "viewer-123"
	if mode == edgetoken.ModePassword {
		var err error
		sub, err = edgetoken.AnonSubject()
		if err != nil {
			t.Fatalf("AnonSubject: %v", err)
		}
	}
	tok, err := s.Mint(edgetoken.MintParams{
		ContentHost: host, Subject: sub, SiteID: siteID, Mode: mode, TTL: ttl,
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return tok
}

// newHandler builds a Handler over a fake resolver, fake store, and a verifier
// over the signer's static key + the given revocation reader.
func newHandler(resolver serve.RouteResolver, store storage.Store, s *edgetoken.Signer,
	revoked edgeverify.RevocationReader) *serve.Handler {
	var verifier *edgeverify.Verifier
	if s != nil {
		verifier = edgeverify.NewForSigner(s, revoked)
	}
	limiter := ratelimit.New(0, 0) // disabled (fail open) by default
	return serve.New(resolver, store, verifier, limiter, nil, serve.Config{
		AppAuthzURL: "https://app.dropway.dev/authz",
	})
}

// doRequest runs a GET against the handler with Host set, returning the recorder.
func doRequest(h http.Handler, method, host, target string, headers map[string]string, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+host+target, nil)
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if cookie != "" {
		req.Header.Set("Cookie", "__Host-edge="+cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func publicRoute() serve.Route {
	return serve.Route{OrgID: testOrgID, SiteID: testSiteID, VersionID: testVersionID, AccessMode: projection.AccessPublic}
}

// TestGated_HTTPContentUsesInsecureCookie asserts that over a LOCAL http content origin
// (CONTENT_SCHEME=http) the gated callback sets the UNPREFIXED, non-Secure `edge`
// cookie and redirects to http://host:port — a __Host-/Secure cookie is rejected by
// browsers over http, so emitting one loops the viewer back to /authz forever
// (ERR_TOO_MANY_REDIRECTS). The read side must then accept that `edge` cookie and serve.
func TestGated_HTTPContentUsesInsecureCookie(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	s := testSigner(t)
	revoked := fakeRevoked{minIATs: map[string]int64{}, errOn: map[string]bool{}}
	h := serve.New(
		fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModeOrgOnly)}},
		store, edgeverify.NewForSigner(s, revoked), ratelimit.New(0, 0), nil,
		serve.Config{AppAuthzURL: "http://localhost:3000/authz", ContentScheme: "http", ContentPort: "8090"},
	)

	tok := mint(t, s, testHost, testSiteID, edgetoken.ModeOrgOnly, time.Minute)

	// 1) Callback → unprefixed `edge` cookie WITHOUT Secure + an http://host:8090/ redirect.
	rec := doRequest(h, http.MethodGet, testHost, "/__authz/callback?token="+tok+"&next=%2F", nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("callback should 302, got %d", rec.Code)
	}
	sc := rec.Header().Get("Set-Cookie")
	if !strings.Contains(sc, "edge="+tok) || strings.Contains(sc, "__Host-edge=") {
		t.Errorf("want unprefixed edge cookie over http, got %q", sc)
	}
	if strings.Contains(sc, "Secure") {
		t.Errorf("http origin must NOT set Secure (browsers reject it over http); got %q", sc)
	}
	for _, want := range []string{"Path=/", "HttpOnly", "SameSite=Lax", "Max-Age=900"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie missing %q; got %q", want, sc)
		}
	}
	if got := rec.Header().Get("Location"); got != "http://"+testHost+":8090/" {
		t.Errorf("callback Location = %q, want http://%s:8090/", got, testHost)
	}

	// 2) Read side accepts the insecure `edge` cookie → serves 200 (no redirect loop).
	req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/", nil)
	req.Host = testHost
	req.Header.Set("Cookie", "edge="+tok)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("valid edge cookie should serve 200, got %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "secret") {
		t.Errorf("expected served content, got %q", rec2.Body.String())
	}
}

func gatedRoute(mode string) serve.Route {
	return serve.Route{OrgID: testOrgID, SiteID: testSiteID, VersionID: testVersionID, AccessMode: mode}
}

// --- PUBLIC serve -----------------------------------------------------------

func TestPublic_ServesIndexHTML(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("<h1>home</h1>"), contentType: "text/html; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	// HTML is short-TTL, must-revalidate (never immutable).
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60, must-revalidate" {
		t.Errorf("Cache-Control = %q, want public, max-age=60, must-revalidate", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Errorf("expected content CSP, got %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff missing, got %q", got)
	}
	if rec.Body.String() != "<h1>home</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestPublic_AutoindexForUploadWithoutIndex(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "notes.md", body: []byte("# notes"), contentType: "text/markdown"},
		{path: "readme.txt", body: []byte("hello"), contentType: "text/plain"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	// A listing is treated as HTML: short-TTL, must-revalidate.
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60, must-revalidate" {
		t.Errorf("Cache-Control = %q, want public, max-age=60, must-revalidate", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"Index of /", `href="/notes.md"`, `href="/readme.txt"`} {
		if !strings.Contains(body, want) {
			t.Errorf("listing body missing %q; got:\n%s", want, body)
		}
	}
}

func TestPublic_AutoindexSubdirectory(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("<h1>home</h1>"), contentType: "text/html"},
		{path: "docs/a.md", body: []byte("a"), contentType: "text/markdown"},
		{path: "docs/b.md", body: []byte("b"), contentType: "text/markdown"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/docs/", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Index of /docs/", `href="/docs/a.md"`, `href="/docs/b.md"`, "Parent directory"} {
		if !strings.Contains(body, want) {
			t.Errorf("listing body missing %q; got:\n%s", want, body)
		}
	}
}

func TestPublic_IndexHTMLWinsOverAutoindex(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("<h1>home</h1>"), contentType: "text/html"},
		{path: "notes.md", body: []byte("# notes"), contentType: "text/markdown"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "<h1>home</h1>" {
		t.Fatalf("want served index.html, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestPublic_RendersMarkdownAsViewerPage(t *testing.T) {
	store := storage.NewFake()
	source := "# Title\n\nsome **bold** text"
	stageVersion(t, store, []fileSpec{
		{path: "notes.md", body: []byte(source), contentType: "text/markdown; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/notes.md", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Served as HTML, not the raw text/markdown the manifest recorded.
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	// Treated as an HTML entry doc → short-TTL cache policy.
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60, must-revalidate" {
		t.Errorf("Cache-Control = %q, want public, max-age=60, must-revalidate", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"<h1>Title</h1>",
		"<strong>bold</strong>",
		`<pre id="md-raw" hidden># Title`,
		`id="md-toggle"`,
		`id="md-copy"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("viewer page missing %q; got:\n%s", want, body)
		}
	}
}

func TestPublic_RendersMDXAsViewerPage(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "guide.mdx", body: []byte("# Guide"), contentType: "text/markdown"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/guide.mdx", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	if !strings.Contains(rec.Body.String(), "<h1>Guide</h1>") {
		t.Errorf("MDX not rendered; got:\n%s", rec.Body.String())
	}
}

func TestPublic_MarkdownRawQueryServesSource(t *testing.T) {
	store := storage.NewFake()
	source := "# Title\n\nbody"
	stageVersion(t, store, []fileSpec{
		{path: "notes.md", body: []byte(source), contentType: "text/markdown; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/notes.md?raw=1", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// ?raw opts out of rendering: the manifest content_type is served verbatim.
	if got := rec.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/markdown; charset=utf-8", got)
	}
	if rec.Body.String() != source {
		t.Errorf("body = %q, want raw source", rec.Body.String())
	}
}

func TestPublic_MarkdownOversizedServedRaw(t *testing.T) {
	store := storage.NewFake()
	// Just over MarkdownMaxRenderBytes (1 MiB) → streamed raw, never buffered+rendered.
	big := strings.Repeat("#", 1024*1024+1)
	stageVersion(t, store, []fileSpec{
		{path: "huge.md", body: []byte(big), contentType: "text/markdown; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/huge.md", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/markdown; charset=utf-8 (raw)", got)
	}
	if rec.Body.String() != big {
		t.Errorf("oversized markdown not served raw")
	}
}

func TestPublic_AutoindexStill404sEmptyDirectory(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "notes.md", body: []byte("# notes"), contentType: "text/markdown"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/missing/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPublic_Custom404WinsOverAutoindex(t *testing.T) {
	// A site that ships a custom 404.html has opted into its own miss handling,
	// so a populated subdirectory must serve that 404 page, not an autoindex.
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("<h1>home</h1>"), contentType: "text/html"},
		{path: "404.html", body: []byte("<h1>custom missing</h1>"), contentType: "text/html"},
		{path: "assets/app.js", body: []byte("console.log(1)"), contentType: "text/javascript"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/assets/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rec.Body.String() != "<h1>custom missing</h1>" {
		t.Errorf("want custom 404 body, got %q", rec.Body.String())
	}
}

func TestPublic_HashedAssetImmutable(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "assets/app.4f3a9c2b.js", body: []byte("console.log(1)"), contentType: "text/javascript; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/assets/app.4f3a9c2b.js", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want immutable", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestPublic_HEADStripsBody(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("<h1>home</h1>"), contentType: "text/html; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodHead, testHost, "/", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body should be empty, got %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("HEAD must keep headers; Content-Type = %q", got)
	}
}

// --- Path traversal / unsafe paths -----------------------------------------

func TestPathTraversalRejected(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("home"), contentType: "text/html; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	cases := []string{
		"/../etc/passwd",
		"/foo/../../bar",
		"/%2e%2e/secret",   // encoded ".." segment
		"/%2e%2e%2fsecret", // encoded "../" (slash also encoded)
		"/foo%00bar",       // NUL
		"/foo%5cbar",       // backslash (encoded)
		"/a/%2e%2e/%2e%2e", // encoded traversal
	}
	for _, target := range cases {
		rec := doRequest(h, http.MethodGet, testHost, target, nil, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: status = %d, want 404", target, rec.Code)
		}
	}
}

// --- Unknown host -----------------------------------------------------------

func TestUnknownHost404(t *testing.T) {
	store := storage.NewFake()
	h := newHandler(fakeResolver{map[string]serve.Route{}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, "nobody.dropwaycontent.com", "/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	// Platform 404 uses the strict platform CSP.
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Errorf("platform 404 must use strict CSP, got %q", got)
	}
}

// errResolver always fails with a generic (non-ErrHostNotFound) backend error.
type errResolver struct{}

func (errResolver) Resolve(_ context.Context, _ string) (serve.Route, error) {
	return serve.Route{}, errors.New("resolve backend down")
}

// TestResolverBackendError500 asserts a SERVER-SIDE resolve failure is a 500 (our
// problem), distinct from the 404 a genuinely unknown host gets — mirroring the
// serving Worker's 404-vs-500 split.
func TestResolverBackendError500(t *testing.T) {
	store := storage.NewFake()
	h := newHandler(errResolver{}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, "anyhost.dropwaycontent.com", "/", nil, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a resolver backend error must be 500 (not 404), got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("500 must be no-store, got %q", got)
	}
	// Generic platform page → strict CSP, no structure leak.
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Errorf("platform 500 must use strict CSP, got %q", got)
	}
}

// --- Method gate ------------------------------------------------------------

func TestMethodNotAllowed(t *testing.T) {
	store := storage.NewFake()
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := doRequest(h, m, testHost, "/", nil, "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", m, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s: Allow = %q, want GET, HEAD", m, got)
		}
		// 405 uses the CONTENT CSP, per index.ts.
		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
			t.Errorf("%s: 405 should use content CSP, got %q", m, got)
		}
	}
}

// --- Service-worker block ---------------------------------------------------

func TestServiceWorkerHeaderBlocked(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("home"), contentType: "text/html; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", map[string]string{"Service-Worker": "script"}, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Service-Worker:script should 404, got %d", rec.Code)
	}
}

func TestServiceWorkerScriptFilenameBlocked(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "sw.js", body: []byte("self.addEventListener"), contentType: "text/javascript; charset=utf-8"},
		{path: "index.html", body: []byte("home"), contentType: "text/html; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/sw.js", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("sw.js should 404 even when in manifest, got %d", rec.Code)
	}
}

// --- Manifest validation ----------------------------------------------------

func TestManifestSchemaVersionMismatchFailsClosed(t *testing.T) {
	store := storage.NewFake()
	// Stage a manifest with schema_version 2 (unsupported by the manifest contract).
	raw, _ := json.Marshal(map[string]any{
		"schema_version": 2,
		"files": map[string]any{
			"index.html": map[string]any{"sha256": sha256Hex([]byte("x")), "content_type": "text/html"},
		},
	})
	_ = store.PutManifest(context.Background(), testOrgID, testSiteID, testVersionID, raw)
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad schema_version should fail closed (404), got %d", rec.Code)
	}
}

func TestMissingBlob404(t *testing.T) {
	store := storage.NewFake()
	// Manifest references a sha256 with no staged blob (projection drift).
	raw, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"files": map[string]any{
			"index.html": map[string]any{"sha256": sha256Hex([]byte("missing")), "content_type": "text/html"},
		},
	})
	_ = store.PutManifest(context.Background(), testOrgID, testSiteID, testVersionID, raw)
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing blob should 404, got %d", rec.Code)
	}
}

// --- Custom 404.html --------------------------------------------------------

func TestCustom404Page(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("home"), contentType: "text/html; charset=utf-8"},
		{path: "404.html", body: []byte("<h1>custom nope</h1>"), contentType: "text/html; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/does-not-exist", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "custom nope") {
		t.Errorf("expected custom 404 body, got %q", rec.Body.String())
	}
	// Custom 404 is tenant content → content CSP, not platform.
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Errorf("custom 404 should use content CSP, got %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=30" {
		t.Errorf("custom 404 Cache-Control = %q", got)
	}
}

// --- Link expiry ------------------------------------------------------------

func TestLinkExpired410(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("home"), contentType: "text/html; charset=utf-8"},
	})
	past := time.Now().Add(-time.Hour)
	rt := publicRoute()
	rt.ExpiresAt = &past
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: rt}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusGone {
		t.Fatalf("expired link should be 410, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("410 Cache-Control = %q, want no-store", got)
	}
}

// --- Rate limit -------------------------------------------------------------

func TestRateLimit429(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("home"), contentType: "text/html; charset=utf-8"},
	})
	limiter := ratelimit.New(2, time.Minute) // 2 req / window
	h := serve.New(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil, limiter, nil, serve.Config{})

	// First 2 allowed, 3rd blocked (per-host identity; no IP header).
	for i := 0; i < 2; i++ {
		rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d should be 200, got %d", i, rec.Code)
		}
	}
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request should be 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Errorf("429 should carry Retry-After")
	}
}

// --- Org status -------------------------------------------------------------

type fakeOrgStatus struct {
	status string
	err    error
}

func (f fakeOrgStatus) OrgStatus(context.Context, string) (string, error) { return f.status, f.err }

func TestOrgSuspended503(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("home"), contentType: "text/html; charset=utf-8"},
	})
	h := serve.New(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil,
		ratelimit.New(0, 0), fakeOrgStatus{status: "suspended"}, serve.Config{})

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("suspended org should 503, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "300" {
		t.Errorf("503 Retry-After = %q, want 300", got)
	}
}

func TestOrgStatusFailsOpenOnError(t *testing.T) {
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("home"), contentType: "text/html; charset=utf-8"},
	})
	h := serve.New(fakeResolver{map[string]serve.Route{testHost: publicRoute()}}, store, nil,
		ratelimit.New(0, 0), fakeOrgStatus{err: fmt.Errorf("kv down")}, serve.Config{})

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("org-status read error should fail OPEN (serve), got %d", rec.Code)
	}
}

// --- Legacy `--` host redirects ---------------------------------------------
//
// Hosts minted before the single-dash migration used `--` as the org/app
// separator. The serving path 301s an unresolved `--` host to its single-dash
// rewrite when (and only when) that rewrite resolves.

func TestLegacyHost_RedirectsToSingleDash(t *testing.T) {
	store := storage.NewFake()
	h := serve.New(fakeResolver{map[string]serve.Route{"acme-blog.dropwaycontent.com": publicRoute()}},
		store, nil, ratelimit.New(0, 0), nil,
		serve.Config{ContentScheme: "http", ContentPort: "8090"})

	rec := doRequest(h, http.MethodGet, "acme--blog.dropwaycontent.com", "/some/page?q=1", nil, "")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("legacy host with live counterpart should 301, got %d", rec.Code)
	}
	// Location preserves path+query and uses the CONFIGURED scheme/port (the
	// request's Host header carries no usable scheme).
	want := "http://acme-blog.dropwaycontent.com:8090/some/page?q=1"
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestLegacyHost_NoCounterpart404(t *testing.T) {
	store := storage.NewFake()
	h := newHandler(fakeResolver{map[string]serve.Route{}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, "ghost--site.dropwaycontent.com", "/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("legacy host with no counterpart should 404, got %d", rec.Code)
	}
}

func TestLegacyHost_OwnRouteServesWithoutRedirect(t *testing.T) {
	// A host that CONTAINS `--` but has its own route (e.g. a custom domain)
	// resolves on the primary lookup and must never be rewritten.
	store := storage.NewFake()
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("mine"), contentType: "text/html; charset=utf-8"},
	})
	h := newHandler(fakeResolver{map[string]serve.Route{"my--legacy.example.com": publicRoute()}},
		store, nil, nil)

	rec := doRequest(h, http.MethodGet, "my--legacy.example.com", "/", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("host with its own route should serve, got %d", rec.Code)
	}
	if rec.Body.String() != "mine" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestPlainMissWithoutDoubleDash404(t *testing.T) {
	store := storage.NewFake()
	h := newHandler(fakeResolver{map[string]serve.Route{}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, "nope.dropwaycontent.com", "/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("plain miss should 404, got %d", rec.Code)
	}
}
