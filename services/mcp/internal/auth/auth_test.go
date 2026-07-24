// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/analytics"
	coreauth "github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
)

type fakeVerifier struct {
	claims *coreauth.Claims
	err    error
}

func (f fakeVerifier) Verify(_ context.Context, _ string) (*coreauth.Claims, error) {
	return f.claims, f.err
}

const resourceMeta = "https://mcp.dropway.dev/.well-known/oauth-protected-resource"

// nextRecorder records whether the wrapped handler ran + the tenant/token it saw.
func nextRecorder(seen *store_Tenant) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := TenantFromContext(r.Context()); ok {
			seen.set, seen.OrgID, seen.UserID = true, t.OrgID, t.UserID
		}
		if tok, ok := TokenFromContext(r.Context()); ok {
			seen.token = tok
		}
		w.WriteHeader(http.StatusOK)
	})
}

type store_Tenant struct {
	set           bool
	OrgID, UserID string
	token         string
}

func do(h http.Handler, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "https://mcp.dropway.dev/mcp", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMiddleware_MissingTokenIs401WithResourceMetadata(t *testing.T) {
	var seen store_Tenant
	h := Middleware(fakeVerifier{claims: &coreauth.Claims{OrgID: "org-1"}}, resourceMeta, nextRecorder(&seen))

	rec := do(h, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token = %d, want 401", rec.Code)
	}
	if wa := rec.Header().Get("WWW-Authenticate"); !strings.Contains(wa, resourceMeta) {
		t.Errorf("401 must advertise resource metadata for OAuth discovery; got %q", wa)
	}
	if seen.set {
		t.Error("next handler must not run on a 401")
	}
}

func TestMiddleware_MalformedHeaderIs401(t *testing.T) {
	h := Middleware(fakeVerifier{claims: &coreauth.Claims{OrgID: "org-1"}}, resourceMeta, nextRecorder(&store_Tenant{}))
	if rec := do(h, "Token abc"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-Bearer scheme = %d, want 401", rec.Code)
	}
}

func TestMiddleware_InvalidTokenIs401(t *testing.T) {
	h := Middleware(fakeVerifier{err: errors.New("bad signature")}, resourceMeta, nextRecorder(&store_Tenant{}))
	if rec := do(h, "Bearer xxx"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token = %d, want 401", rec.Code)
	}
}

func TestMiddleware_TokenWithoutOrgIs401(t *testing.T) {
	h := Middleware(fakeVerifier{claims: &coreauth.Claims{OrgID: ""}}, resourceMeta, nextRecorder(&store_Tenant{}))
	if rec := do(h, "Bearer xxx"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token without org = %d, want 401", rec.Code)
	}
}

func TestMiddleware_ValidTokenInjectsTenant(t *testing.T) {
	var seen store_Tenant
	h := Middleware(fakeVerifier{claims: &coreauth.Claims{OrgID: "org-7"}}, resourceMeta, nextRecorder(&seen))

	rec := do(h, "bearer the-token") // lowercase scheme must also work
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token = %d, want 200", rec.Code)
	}
	if !seen.set || seen.OrgID != "org-7" {
		t.Errorf("tenant should be injected with OrgID=org-7; got %+v", seen)
	}
	// The raw token must be stashed for the write tools to forward to the Go API.
	if seen.token != "the-token" {
		t.Errorf("token should be in context for forwarding; got %q", seen.token)
	}
}

// fakeEmitter records captured analytics events.
type fakeEmitter struct{ events []analytics.Event }

func (f *fakeEmitter) Capture(_ context.Context, ev analytics.Event) { f.events = append(f.events, ev) }

// A rejected token is captured to analytics as auth_rejected (surface mcp); the
// credential-less 401 challenge that starts every OAuth connect is NOT captured.
func TestMiddlewareObserved_CapturesRejections(t *testing.T) {
	em := &fakeEmitter{}
	var seen store_Tenant
	h := MiddlewareObserved(fakeVerifier{err: errors.New("token is expired")}, resourceMeta, em, nextRecorder(&seen))

	// Credential-less challenge → no event.
	if rec := do(h, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("challenge status = %d, want 401", rec.Code)
	}
	if len(em.events) != 0 {
		t.Fatalf("captured %d events for the OAuth challenge, want 0", len(em.events))
	}

	// A presented-but-invalid token → one auth_rejected event.
	if rec := do(h, "Bearer bad.token.sig"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("rejection status = %d, want 401", rec.Code)
	}
	if len(em.events) != 1 {
		t.Fatalf("captured %d events, want 1", len(em.events))
	}
	ev := em.events[0]
	if ev.Event != "auth_rejected" || ev.DistinctID != "system" {
		t.Errorf("event = %q distinct_id = %q", ev.Event, ev.DistinctID)
	}
	if ev.Properties["surface"] != "mcp" || ev.Properties["kind"] != "jwt" || ev.Properties["reason"] != "token is expired" {
		t.Errorf("properties = %+v", ev.Properties)
	}
}

// A verified token with no org_id is captured too (it is a rejected credential).
func TestMiddlewareObserved_NoOrgCaptured(t *testing.T) {
	em := &fakeEmitter{}
	var seen store_Tenant
	h := MiddlewareObserved(fakeVerifier{claims: &coreauth.Claims{}}, resourceMeta, em, nextRecorder(&seen))

	if rec := do(h, "Bearer some.token.sig"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(em.events) != 1 || em.events[0].Properties["reason"] != "token has no organization" {
		t.Errorf("events = %+v", em.events)
	}
}

// fakeKeyValidator drives the Gate's API-key branch.
type fakeKeyValidator struct {
	tenant store.Tenant
	err    error
	calls  int
}

func (f *fakeKeyValidator) ValidateAPIKey(_ context.Context, _ string) (store.Tenant, error) {
	f.calls++
	return f.tenant, f.err
}

const testKey = "dw_live_0000000000000000000000000000000000000000000" // 43 base62 chars

// A dw_live_ bearer routes to the key validator; on success the tenant AND the
// raw key (forwarded to the API by write tools) land in the context, and the
// JWT verifier is never consulted.
func TestGate_APIKey_Valid(t *testing.T) {
	kv := &fakeKeyValidator{tenant: store.Tenant{OrgID: "org-9", UserID: "user-9"}}
	var seen store_Tenant
	h := Gate(Config{
		Verifier:            fakeVerifier{err: errors.New("must not be called")},
		ResourceMetadataURL: resourceMeta,
		Keys:                kv,
	}, nextRecorder(&seen))

	rec := do(h, "Bearer "+testKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !seen.set || seen.OrgID != "org-9" || seen.UserID != "user-9" {
		t.Errorf("tenant = %+v", seen)
	}
	if seen.token != testKey {
		t.Errorf("context token = %q, want the raw key (forwarded on writes)", seen.token)
	}
	if kv.calls != 1 {
		t.Errorf("validator calls = %d, want 1", kv.calls)
	}
}

// A rejected key → uniform 401 + one auth_rejected event with step=api_key_auth.
func TestGate_APIKey_Rejected(t *testing.T) {
	em := &fakeEmitter{}
	kv := &fakeKeyValidator{err: errors.New("api 401: unauthorized")}
	var seen store_Tenant
	h := Gate(Config{
		Verifier:            fakeVerifier{},
		ResourceMetadataURL: resourceMeta,
		Emitter:             em,
		Keys:                kv,
	}, nextRecorder(&seen))

	rec := do(h, "Bearer "+testKey)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if seen.set {
		t.Error("next handler must not see a tenant on key rejection")
	}
	if len(em.events) != 1 {
		t.Fatalf("captured %d events, want 1", len(em.events))
	}
	p := em.events[0].Properties
	if p["surface"] != "mcp" || p["kind"] != "api_key" || p["step"] != "api_key_auth" {
		t.Errorf("properties = %+v", p)
	}
}

// A key presented to a server with no validator (no API_URL) → 401 with a clear
// detail, captured, and the JWT verifier is not consulted.
func TestGate_APIKey_NotConfigured(t *testing.T) {
	em := &fakeEmitter{}
	var seen store_Tenant
	h := Gate(Config{
		Verifier:            fakeVerifier{err: errors.New("must not be called")},
		ResourceMetadataURL: resourceMeta,
		Emitter:             em,
	}, nextRecorder(&seen))

	rec := do(h, "Bearer "+testKey)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "api keys are not accepted") {
		t.Errorf("body = %q, want a clear not-accepted detail", rec.Body.String())
	}
	if len(em.events) != 1 || em.events[0].Properties["step"] != "api_key_auth" {
		t.Errorf("events = %+v", em.events)
	}
}

// fakeMeClient drives the cached validator.
type fakeMeClient struct {
	userID, orgID string
	err           error
	calls         int
}

func (f *fakeMeClient) Me(_ context.Context, _ string) (string, string, error) {
	f.calls++
	return f.userID, f.orgID, f.err
}

// The validator resolves via /v1/me, caches successes (one upstream call for a
// burst), never caches failures, and rejects malformed keys without an API call.
func TestAPIKeyValidator(t *testing.T) {
	mc := &fakeMeClient{userID: "u1", orgID: "o1"}
	v := NewAPIKeyValidator(mc, time.Minute)

	for i := 0; i < 3; i++ {
		tenant, err := v.ValidateAPIKey(context.Background(), testKey)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if tenant.OrgID != "o1" || tenant.UserID != "u1" {
			t.Fatalf("tenant = %+v", tenant)
		}
	}
	if mc.calls != 1 {
		t.Errorf("upstream calls = %d, want 1 (cached)", mc.calls)
	}

	// Malformed key: rejected locally, no upstream call.
	before := mc.calls
	if _, err := v.ValidateAPIKey(context.Background(), "dw_live_short"); err == nil {
		t.Error("malformed key must be rejected")
	}
	if mc.calls != before {
		t.Error("malformed key must not reach the API")
	}

	// Failures are not cached: each attempt re-asks the API.
	mcBad := &fakeMeClient{err: errors.New("api 401: unauthorized")}
	vBad := NewAPIKeyValidator(mcBad, time.Minute)
	for i := 0; i < 2; i++ {
		if _, err := vBad.ValidateAPIKey(context.Background(), testKey); err == nil {
			t.Fatal("expected rejection")
		}
	}
	if mcBad.calls != 2 {
		t.Errorf("failed validations should not be cached; upstream calls = %d, want 2", mcBad.calls)
	}
}

// The gate's invalid-token 401 body carries the recovery guidance (including the
// family-revocation explanation) so the MCP client's LLM can instruct the user.
func TestGate_InvalidToken_BodyExplainsRecovery(t *testing.T) {
	var seen store_Tenant
	h := Middleware(fakeVerifier{err: errors.New("token is expired")}, resourceMeta, nextRecorder(&seen))
	rec := do(h, "Bearer some.jwt.token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	for _, want := range []string{"family revocation", "re-authorize", "refresh"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("401 body missing %q; got: %s", want, rec.Body.String())
		}
	}
}
