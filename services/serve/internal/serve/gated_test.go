// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/edgetoken"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/serve/internal/serve"
)

// gatedHandler builds a handler for a gated route with a working signer + a
// permissive (empty) revocation reader.
func gatedHandler(t *testing.T, mode string, store *storage.Fake) (*serve.Handler, *edgetoken.Signer) {
	t.Helper()
	s := testSigner(t)
	revoked := fakeRevoked{minIATs: map[string]int64{}, errOn: map[string]bool{}}
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(mode)}}, store, s, revoked)
	return h, s
}

func stageGatedIndex(t *testing.T, store *storage.Fake) {
	stageVersion(t, store, []fileSpec{
		{path: "index.html", body: []byte("<h1>secret</h1>"), contentType: "text/html; charset=utf-8"},
	})
}

// --- No / invalid cookie → 302 to /authz (no content) -----------------------

func TestGated_NoCookieRedirectsToAuthz(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, _ := gatedHandler(t, edgetoken.ModePassword, store)

	rec := doRequest(h, http.MethodGet, testHost, "/secret", nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("no cookie should 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("bad Location %q: %v", loc, err)
	}
	if !strings.HasPrefix(loc, "https://app.dropway.dev/authz") {
		t.Errorf("Location = %q, want /authz exchange", loc)
	}
	if u.Query().Get("host") != testHost {
		t.Errorf("authz host = %q, want %q", u.Query().Get("host"), testHost)
	}
	if u.Query().Get("next") != "/secret" {
		t.Errorf("authz next = %q, want /secret", u.Query().Get("next"))
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("no-cookie response must not leak content")
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("redirect Cache-Control = %q, want no-store", got)
	}
}

func TestGated_VerifierNilFailsClosed(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	// nil signer → nil verifier → every gated request must 302 (fail closed).
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModeOrgOnly)}}, store, nil, nil)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "validlooking")
	if rec.Code != http.StatusFound {
		t.Fatalf("nil verifier should fail closed (302), got %d", rec.Code)
	}
}

// --- Valid token → serves with private,no-store -----------------------------

func TestGated_ValidTokenServesPrivate(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, s := gatedHandler(t, edgetoken.ModePassword, store)

	tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, time.Minute)
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token should serve 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "<h1>secret</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
	// Gated content MUST be private/no-store + Vary: Cookie, never public cache.
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store, max-age=0, must-revalidate" {
		t.Errorf("gated Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Cookie" {
		t.Errorf("Vary = %q, want Cookie", got)
	}
	if strings.Contains(rec.Header().Get("Cache-Control"), "public") {
		t.Errorf("gated response must never carry public Cache-Control")
	}
}

func TestGated_ValidTokenAllModes(t *testing.T) {
	for _, mode := range []string{edgetoken.ModePassword, edgetoken.ModeAllowlist, edgetoken.ModeOrgOnly} {
		store := storage.NewFake()
		stageGatedIndex(t, store)
		h, s := gatedHandler(t, mode, store)
		tok := mint(t, s, testHost, testSiteID, mode, time.Minute)
		rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
		if rec.Code != http.StatusOK {
			t.Errorf("mode %s: valid token should serve, got %d", mode, rec.Code)
		}
	}
}

// --- Security rejections ----------------------------------------------------

func TestGated_WrongAudienceDenied(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, s := gatedHandler(t, edgetoken.ModePassword, store)

	// Token minted for otherHost, presented at testHost → aud mismatch → 302.
	tok := mint(t, s, otherHost, testSiteID, edgetoken.ModePassword, time.Minute)
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusFound {
		t.Fatalf("wrong aud should be denied (302), got %d", rec.Code)
	}
}

func TestGated_SiteIDMismatchDenied(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, s := gatedHandler(t, edgetoken.ModePassword, store)

	// Token bound to a sibling site_id → 302.
	tok := mint(t, s, testHost, otherSiteID, edgetoken.ModePassword, time.Minute)
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusFound {
		t.Fatalf("site_id mismatch should be denied (302), got %d", rec.Code)
	}
}

func TestGated_ModeMismatchDenied(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	// Route is org_only; token carries password mode → H1 mode-binding rejects it.
	h, s := gatedHandler(t, edgetoken.ModeOrgOnly, store)

	tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, time.Minute)
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusFound {
		t.Fatalf("mode mismatch should be denied (302), got %d", rec.Code)
	}
}

func TestGated_ExpiredTokenDenied(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, s := gatedHandler(t, edgetoken.ModePassword, store)

	// Mint an already-expired token (negative TTL).
	tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, -time.Minute)
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusFound {
		t.Fatalf("expired token should be denied (302), got %d", rec.Code)
	}
}

func TestGated_AlgNoneForgeryDenied(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, _ := gatedHandler(t, edgetoken.ModePassword, store)

	// A hand-crafted alg=none token: header {"alg":"none"} . payload . (empty sig).
	none := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJpc3MiOiJodHRwczovL2FwaS5zaGlwcGVkLmFwcC9lZGdlIiwiYXVkIjoiYWNtZS5zaGlwcGVkdXNlcmNvbnRlbnQuY29tIn0."
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, none)
	if rec.Code != http.StatusFound {
		t.Fatalf("alg=none forgery should be denied (302), got %d", rec.Code)
	}
}

func TestGated_HS256ForgeryDenied(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, _ := gatedHandler(t, edgetoken.ModePassword, store)

	// A token with alg=HS256 (the public key used as an HMAC secret) must be rejected
	// because the verifier pins alg=EdDSA only. A bogus HS256 signature suffices —
	// the alg gate fires before any signature check could pass.
	hs := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJpc3MiOiJodHRwczovL2FwaS5zaGlwcGVkLmFwcC9lZGdlIn0.bogus_signature"
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, hs)
	if rec.Code != http.StatusFound {
		t.Fatalf("HS256 forgery should be denied (302), got %d", rec.Code)
	}
}

func TestGated_RevokedTokenDenied(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	s := testSigner(t)

	// Mint first so we know the token iat; then set min_iat above it → revoked.
	tok := mint(t, s, testHost, testSiteID, edgetoken.ModeOrgOnly, time.Minute)
	future := time.Now().Add(time.Hour).Unix()
	revoked := fakeRevoked{
		minIATs: map[string]int64{"org:" + testOrgID: future},
		errOn:   map[string]bool{},
	}
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModeOrgOnly)}}, store, s, revoked)

	rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusFound {
		t.Fatalf("revoked (min_iat>iat) token should be denied (302), got %d", rec.Code)
	}
}

func TestGated_RevocationReadErrorFailsClosed(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	s := testSigner(t)
	revoked := fakeRevoked{
		minIATs: map[string]int64{},
		errOn:   map[string]bool{"user:viewer-123": true}, // read error for the user dimension
	}
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModeOrgOnly)}}, store, s, revoked)

	tok := mint(t, s, testHost, testSiteID, edgetoken.ModeOrgOnly, time.Minute)
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusFound {
		t.Fatalf("revocation read error should fail closed (302), got %d", rec.Code)
	}
}

func TestGated_NilRevocationReaderFailsClosed(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	s := testSigner(t)
	// Verifier built with a nil revocation reader → every gated request fails closed.
	h := newHandler(fakeResolver{map[string]serve.Route{testHost: gatedRoute(edgetoken.ModePassword)}}, store, s, nil)

	tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, time.Minute)
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusFound {
		t.Fatalf("nil revocation reader should fail closed (302), got %d", rec.Code)
	}
}

// --- Callback: cookie set + safe redirect -----------------------------------

func TestGated_CallbackSetsCookieAndRedirects(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, s := gatedHandler(t, edgetoken.ModePassword, store)

	tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, time.Minute)
	target := "/__authz/callback?token=" + url.QueryEscape(tok) + "&next=%2Fdashboard"
	rec := doRequest(h, http.MethodGet, testHost, target, nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("callback should 302, got %d", rec.Code)
	}
	setCookie := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-edge=" + tok, "Path=/", "Secure", "HttpOnly", "SameSite=Lax", "Max-Age=900"} {
		if !strings.Contains(setCookie, want) {
			t.Errorf("Set-Cookie missing %q; got %q", want, setCookie)
		}
	}
	if got := rec.Header().Get("Location"); got != "https://"+testHost+"/dashboard" {
		t.Errorf("callback Location = %q, want same-host /dashboard", got)
	}
}

func TestGated_CallbackBadTokenRedirectsToAuthz(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, _ := gatedHandler(t, edgetoken.ModePassword, store)

	target := "/__authz/callback?token=garbage&next=%2Fx"
	rec := doRequest(h, http.MethodGet, testHost, target, nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("bad callback token should 302, got %d", rec.Code)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("bad callback token must NOT set a cookie")
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "https://app.dropway.dev/authz") {
		t.Errorf("bad callback should bounce to /authz, got %q", rec.Header().Get("Location"))
	}
}

func TestGated_CallbackOpenRedirectDefense(t *testing.T) {
	store := storage.NewFake()
	stageGatedIndex(t, store)
	h, s := gatedHandler(t, edgetoken.ModePassword, store)

	tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, time.Minute)
	// A protocol-relative next must be normalized to "/".
	target := "/__authz/callback?token=" + url.QueryEscape(tok) + "&next=" + url.QueryEscape("//evil.com/x")
	rec := doRequest(h, http.MethodGet, testHost, target, nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("callback should 302, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://"+testHost+"/" {
		t.Errorf("open-redirect not defended: Location = %q, want same-host /", got)
	}
}

// --- Gated 404 stays private ------------------------------------------------

func TestGated_NotFoundStaysPrivate(t *testing.T) {
	// Every gated 404 — platform default AND a tenant's custom 404.html — must be
	// private/no-store + Vary:Cookie (asPrivate parity), never publicly cacheable on
	// a protected origin. A shared CDN must never store/replay a gated 404.
	const wantCC = "private, no-store, max-age=0, must-revalidate"

	t.Run("platform 404", func(t *testing.T) {
		store := storage.NewFake()
		stageGatedIndex(t, store) // no 404.html ⇒ platform default 404
		h, s := gatedHandler(t, edgetoken.ModePassword, store)

		tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, time.Minute)
		rec := doRequest(h, http.MethodGet, testHost, "/nope-not-here", nil, tok)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("gated miss should 404, got %d", rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != wantCC {
			t.Errorf("gated platform 404 Cache-Control = %q, want %q", got, wantCC)
		}
		if got := rec.Header().Get("Vary"); got != "Cookie" {
			t.Errorf("gated platform 404 Vary = %q, want Cookie", got)
		}
	})

	t.Run("custom 404.html", func(t *testing.T) {
		store := storage.NewFake()
		stageVersion(t, store, []fileSpec{
			{path: "index.html", body: []byte("<h1>secret</h1>"), contentType: "text/html; charset=utf-8"},
			{path: "404.html", body: []byte("<h1>tenant nope</h1>"), contentType: "text/html; charset=utf-8"},
		})
		h, s := gatedHandler(t, edgetoken.ModePassword, store)

		tok := mint(t, s, testHost, testSiteID, edgetoken.ModePassword, time.Minute)
		rec := doRequest(h, http.MethodGet, testHost, "/missing", nil, tok)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("gated miss should 404, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "tenant nope") {
			t.Errorf("expected custom 404 body, got %q", rec.Body.String())
		}
		// Tenant content on a gated origin ⇒ never publicly cached.
		if got := rec.Header().Get("Cache-Control"); got != wantCC {
			t.Errorf("gated custom 404 Cache-Control = %q, want %q", got, wantCC)
		}
		if got := rec.Header().Get("Vary"); got != "Cookie" {
			t.Errorf("gated custom 404 Vary = %q, want Cookie", got)
		}
	})
}
