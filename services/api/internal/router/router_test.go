// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpang/shipped/internal/auth"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/services/api/internal/handlers"
)

// fakeVerifier is a middleware.Verifier that returns a canned result so the router
// tests can drive both the authed (verified) and rejected (error) branches without
// a live JWKS endpoint.
type fakeVerifier struct {
	claims *auth.Claims
	err    error
}

func (f fakeVerifier) Verify(context.Context, string) (*auth.Claims, error) {
	return f.claims, f.err
}

func verifierOK() fakeVerifier {
	c := &auth.Claims{OrgID: "org_1", Role: "owner"}
	c.Subject = "user_1"
	return fakeVerifier{claims: c}
}

// newRouter builds the router under test. The API has no Store, so the DB-backed
// routes degrade to 503 (their own guard) — which is exactly what we want for a
// routing test: a 503 (or 200 on the JWT-free public routes) proves the route is
// REGISTERED and reached the handler, vs a 404 (route missing) or 401 (auth gate).
func newRouter(v fakeVerifier) http.Handler {
	api := handlers.New(quota.Unlimited{}) // no store/objects/projection
	return New(v, api, nil)
}

func req(t *testing.T, h http.Handler, method, path string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if authed {
		r.Header.Set("Authorization", "Bearer x")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

// ---------------------------------------------------------------------------
// Public (JWT-free) routes: healthz, edge JWKS, and the password exchange must be
// reachable WITHOUT an Authorization header.
// ---------------------------------------------------------------------------

func TestRouter_Healthz_PublicNoAuth(t *testing.T) {
	h := newRouter(verifierOK())
	rr := req(t, h, http.MethodGet, "/healthz", false)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200 (public, no auth)", rr.Code)
	}
}

func TestRouter_EdgeJWKS_PublicButNoSigner_503(t *testing.T) {
	h := newRouter(verifierOK())
	// No EdgeSigner wired → the handler returns 503. The point is it's REACHABLE
	// without auth (not 401/404), proving the public registration.
	rr := req(t, h, http.MethodGet, "/.well-known/edge-jwks", false)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /.well-known/edge-jwks = %d, want 503 (public, no signer)", rr.Code)
	}
}

func TestRouter_AuthzPassword_JWTFree(t *testing.T) {
	// The password exchange must NOT require a Better Auth token (an un-signed-in
	// viewer submits it). With no store it 503s — but crucially it is NOT 401, which
	// would mean the Auth gate wrongly wrapped it.
	h := newRouter(verifierOK())
	rr := req(t, h, http.MethodPost, "/v1/authz/password", false)
	if rr.Code == http.StatusUnauthorized {
		t.Fatal("POST /v1/authz/password must be JWT-free (got 401 — wrongly behind Auth)")
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /v1/authz/password = %d, want 503 (no store), never 401/404", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// The authz boundary: every /v1 route except the JWT-free password exchange
// requires a verified token.
// ---------------------------------------------------------------------------

func TestRouter_V1_RequiresAuth_401WithoutToken(t *testing.T) {
	h := newRouter(verifierOK())
	authedRoutes := []struct{ method, path string }{
		{http.MethodGet, "/v1/me"},
		{http.MethodGet, "/v1/members"},
		{http.MethodGet, "/v1/audit"},
		{http.MethodPost, "/v1/sites"},
		{http.MethodGet, "/v1/sites"},
		{http.MethodPost, "/v1/authz/mint"},
		{http.MethodPut, "/v1/orgs/allow-external-sharing"},
	}
	for _, rt := range authedRoutes {
		rr := req(t, h, rt.method, rt.path, false) // no Authorization header
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token = %d, want 401 (behind the authz boundary)", rt.method, rt.path, rr.Code)
		}
	}
}

func TestRouter_V1_RejectsInvalidToken_401(t *testing.T) {
	// The verifier rejects the token → the Auth middleware returns 401 and never
	// reaches the handler (so it's a 401, not a 503-from-no-store).
	h := newRouter(fakeVerifier{err: errVerify})
	rr := req(t, h, http.MethodGet, "/v1/me", true)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/me with a rejected token = %d, want 401", rr.Code)
	}
}

// errVerify is a stand-in verifier error (e.g. a forged/expired token).
var errVerify = verifyErr("token rejected")

type verifyErr string

func (e verifyErr) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// /v1/me is the one authed route that needs no store: a verified token → 200 with
// the echoed claims, proving the Auth→handler chain wires through end to end.
// ---------------------------------------------------------------------------

func TestRouter_Me_AuthedNoStoreNeeded_200(t *testing.T) {
	h := newRouter(verifierOK())
	rr := req(t, h, http.MethodGet, "/v1/me", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/me with a valid token = %d, want 200", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Registered DB-backed routes are REACHED (503 from the no-store guard), proving
// they're mounted — vs an unregistered path which 404s.
// ---------------------------------------------------------------------------

func TestRouter_RegisteredRoutesReachHandler_503NotStore(t *testing.T) {
	h := newRouter(verifierOK())
	// These are real routes; with a valid token but no store they hit the handler's
	// requireStore guard → 503 (registered + reached), never 404.
	dbRoutes := []struct{ method, path string }{
		{http.MethodGet, "/v1/sites"},
		{http.MethodPost, "/v1/sites"},
		{http.MethodGet, "/v1/sites/site_1"},
		{http.MethodPost, "/v1/sites/site_1/deployments/prepare"},
		{http.MethodPost, "/v1/sites/site_1/publish"},
		{http.MethodGet, "/v1/sites/site_1/allowlist"},
		{http.MethodGet, "/v1/sites/site_1/domains"},
		{http.MethodGet, "/v1/domains/dom_1/status"},
		{http.MethodGet, "/v1/members"},
	}
	for _, rt := range dbRoutes {
		rr := req(t, h, rt.method, rt.path, true)
		if rr.Code == http.StatusNotFound {
			t.Errorf("%s %s = 404, want the route REGISTERED (503 from no-store guard)", rt.method, rt.path)
		}
		if rr.Code != http.StatusServiceUnavailable {
			t.Logf("%s %s = %d (not 503; acceptable as long as not 404)", rt.method, rt.path, rr.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// 404 (unregistered path) and 405 (wrong method on a registered path).
// ---------------------------------------------------------------------------

func TestRouter_UnknownPath_404(t *testing.T) {
	h := newRouter(verifierOK())
	rr := req(t, h, http.MethodGet, "/v1/does-not-exist", true)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET /v1/does-not-exist = %d, want 404", rr.Code)
	}
}

func TestRouter_WrongMethod_405(t *testing.T) {
	h := newRouter(verifierOK())
	// /healthz is registered for GET only; a POST should be 405 Method Not Allowed
	// (chi's default), proving the method is part of the registration.
	rr := req(t, h, http.MethodPost, "/healthz", false)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz = %d, want 405 (GET-only route)", rr.Code)
	}
}

func TestRouter_NilLoggerDefaults(t *testing.T) {
	// New must tolerate a nil base logger (falls back to slog.Default) — assert it
	// doesn't panic and still serves.
	api := handlers.New(quota.Unlimited{})
	h := New(verifierOK(), api, nil)
	rr := req(t, h, http.MethodGet, "/healthz", false)
	if rr.Code != http.StatusOK {
		t.Fatalf("router with nil logger: GET /healthz = %d, want 200", rr.Code)
	}
}
