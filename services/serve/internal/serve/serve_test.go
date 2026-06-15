// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielpang/shipped/internal/edgerevoke"
	"github.com/danielpang/shipped/internal/edgetoken"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/serve/internal/edgeverify"
	"github.com/danielpang/shipped/services/serve/internal/ratelimit"
	"github.com/danielpang/shipped/services/serve/internal/serve"
)

// --- Test fixtures ----------------------------------------------------------

const (
	testHost      = "acme.shippedusercontent.com"
	otherHost     = "evil.shippedusercontent.com"
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
		AppAuthzURL: "https://app.shipped.app/authz",
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

	rec := doRequest(h, http.MethodGet, "nobody.shippedusercontent.com", "/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	// Platform 404 uses the strict platform CSP.
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Errorf("platform 404 must use strict CSP, got %q", got)
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
