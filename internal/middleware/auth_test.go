package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpang/shipped/internal/auth"
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
