package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/logx"
)

// fakeVerifier lets us drive Auth without a live JWKS endpoint.
type fakeVerifier struct {
	wantToken string
	claims    *auth.Claims
	err       error
	called    bool
}

func (f *fakeVerifier) Verify(_ context.Context, token string) (*auth.Claims, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	if token != f.wantToken {
		return nil, errors.New("unexpected token")
	}
	return f.claims, nil
}

func TestAuth_ValidToken_InjectsClaims(t *testing.T) {
	claims := &auth.Claims{OrgID: "org_1", Role: "owner"}
	claims.Subject = "user_1"
	fv := &fakeVerifier{wantToken: "good.jwt.token", claims: claims}

	var seen *auth.Claims
	var ok bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, ok = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer good.jwt.token")
	rr := httptest.NewRecorder()

	Auth(fv)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !ok || seen == nil {
		t.Fatal("claims not present in handler context")
	}
	if seen.UserID() != "user_1" || seen.OrgID != "org_1" || seen.Role != "owner" {
		t.Errorf("claims = %+v", seen)
	}
}

func TestAuth_CaseInsensitiveScheme(t *testing.T) {
	claims := &auth.Claims{OrgID: "o"}
	claims.Subject = "u"
	fv := &fakeVerifier{wantToken: "t", claims: claims}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "bearer t") // lowercase scheme
	rr := httptest.NewRecorder()

	Auth(fv)(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for case-insensitive scheme", rr.Code)
	}
}

func TestAuth_Rejections(t *testing.T) {
	cases := []struct {
		name        string
		authHeader  string // "" means do not set
		verifierErr error
		wantCalled  bool
	}{
		{"no header", "", nil, false},
		{"wrong scheme", "Basic abc", nil, false},
		{"empty bearer", "Bearer ", nil, false},
		{"verify fails", "Bearer bad", errors.New("invalid signature"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fv := &fakeVerifier{wantToken: "never", err: tc.verifierErr}
			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()
			Auth(fv)(next).ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rr.Code)
			}
			if nextCalled {
				t.Error("next handler should not run on auth failure")
			}
			if fv.called != tc.wantCalled {
				t.Errorf("verifier called = %v, want %v", fv.called, tc.wantCalled)
			}
		})
	}
}

func TestClaimsFromContext_Absent(t *testing.T) {
	if _, ok := ClaimsFromContext(context.Background()); ok {
		t.Error("expected ok=false on a bare context")
	}
}

// fakeKeyAuth drives AuthWithKeys' API-key branch.
type fakeKeyAuth struct {
	princ *KeyPrincipal
	err   error
}

func (f *fakeKeyAuth) AuthenticateAPIKey(_ context.Context, _, _ string) (*KeyPrincipal, error) {
	return f.princ, f.err
}

func TestAuthWithKeys_APIKey_InjectsClaimsAndMarker(t *testing.T) {
	claims := &auth.Claims{OrgID: "org_1", Role: "member"}
	claims.Subject = "creator_1"
	ka := &fakeKeyAuth{princ: &KeyPrincipal{Claims: claims, KeyID: "key_9"}}
	// The verifier must NOT be consulted for a dw_live_ token.
	fv := &fakeVerifier{wantToken: "never"}

	var seenClaims *auth.Claims
	var seenKey string
	var keyed bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenClaims, _ = ClaimsFromContext(r.Context())
		seenKey, keyed = APIKeyIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	req.Header.Set("Authorization", "Bearer dw_live_abc123")
	rr := httptest.NewRecorder()
	AuthWithKeys(fv, ka)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if fv.called {
		t.Error("JWT verifier must not be consulted for an API-key token")
	}
	if seenClaims == nil || seenClaims.UserID() != "creator_1" || seenClaims.OrgID != "org_1" {
		t.Errorf("synthesized claims not injected: %+v", seenClaims)
	}
	if !keyed || seenKey != "key_9" {
		t.Errorf("keyed marker = (%q, %v), want (key_9, true)", seenKey, keyed)
	}
}

func TestAuthWithKeys_APIKey_RateLimited429(t *testing.T) {
	ka := &fakeKeyAuth{err: &RateLimitedError{RetryAfter: 2 * 1e9}} // 2s
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	req.Header.Set("Authorization", "Bearer dw_live_abc123")
	rr := httptest.NewRecorder()
	AuthWithKeys(&fakeVerifier{}, ka)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header on 429")
	}
}

func TestAuthWithKeys_APIKey_AuthFailure401(t *testing.T) {
	ka := &fakeKeyAuth{err: errors.New("revoked")}
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { nextCalled = true })

	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	req.Header.Set("Authorization", "Bearer dw_live_abc123")
	rr := httptest.NewRecorder()
	AuthWithKeys(&fakeVerifier{}, ka)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if nextCalled {
		t.Error("next handler must not run on key auth failure")
	}
}

func TestAuthWithKeys_NoAuthenticator_KeyFallsToJWT(t *testing.T) {
	// With no key authenticator, a dw_live_ token is treated as a JWT and fails
	// verification → 401 (surfaces that keys aren't accepted on this surface).
	fv := &fakeVerifier{wantToken: "never", err: errors.New("bad jwt")}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	req.Header.Set("Authorization", "Bearer dw_live_abc123")
	rr := httptest.NewRecorder()
	AuthWithKeys(fv, nil)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if !fv.called {
		t.Error("with no key authenticator, the JWT verifier should be consulted")
	}
}

// A rejected JWT logs WHY server-side (with the token's unverified aud/iss) while
// the client still gets the generic 401 — the diagnosability contract for e.g. an
// MCP-forwarded write whose audience the API doesn't accept.
func TestAuth_VerifyFailure_LogsReasonWithAudIss(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	fv := &fakeVerifier{wantToken: "never", err: errors.New("token has invalid audience")}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"aud":"https://mcp.dropway.test/mcp/","iss":"https://app.dropway.test"}`))
	req := httptest.NewRequest(http.MethodPost, "/v1/sites", nil)
	req = req.WithContext(logx.WithLogger(req.Context(), log))
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJFZERTQSJ9."+payload+".sig")
	rr := httptest.NewRecorder()

	Auth(fv)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	out := buf.String()
	for _, want := range []string{"token verification failed", "token has invalid audience", "mcp.dropway.test/mcp/", "app.dropway.test"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q; got: %s", want, out)
		}
	}
	// The response body must stay generic — no verifier detail leaks to the client.
	if strings.Contains(rr.Body.String(), "audience") {
		t.Errorf("response leaked verifier detail: %s", rr.Body.String())
	}
}

// A rejected API key logs the real reason server-side; the client keeps the
// uniform 401 (no revoked/unknown/expired oracle).
func TestAuthWithKeys_KeyFailure_LogsReason(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	ka := &fakeKeyAuth{err: errors.New("key revoked")}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	req = req.WithContext(logx.WithLogger(req.Context(), log))
	req.Header.Set("Authorization", "Bearer dw_live_abc123")
	rr := httptest.NewRecorder()
	AuthWithKeys(&fakeVerifier{}, ka)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(buf.String(), "api key rejected") || !strings.Contains(buf.String(), "key revoked") {
		t.Errorf("log output missing key-rejection reason; got: %s", buf.String())
	}
	if strings.Contains(rr.Body.String(), "revoked") {
		t.Errorf("response leaked key-auth detail: %s", rr.Body.String())
	}
}
