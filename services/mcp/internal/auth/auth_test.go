// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreauth "github.com/danielpang/dropway/internal/auth"
)

type fakeVerifier struct {
	claims *coreauth.Claims
	err    error
}

func (f fakeVerifier) Verify(_ context.Context, _ string) (*coreauth.Claims, error) {
	return f.claims, f.err
}

const resourceMeta = "https://mcp.dropway.dev/.well-known/oauth-protected-resource"

// nextRecorder records whether the wrapped handler ran + the tenant it saw.
func nextRecorder(seen *store_Tenant) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := TenantFromContext(r.Context()); ok {
			seen.set, seen.OrgID, seen.UserID = true, t.OrgID, t.UserID
		}
		w.WriteHeader(http.StatusOK)
	})
}

type store_Tenant struct {
	set            bool
	OrgID, UserID  string
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
}
