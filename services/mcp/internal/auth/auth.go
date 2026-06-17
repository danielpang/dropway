// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package auth is the MCP service's OAuth resource-server gate. It validates the
// bearer access token the MCP client obtained from the Dropway authorization
// server (Better Auth) — reusing the platform's EdDSA/JWKS verifier — and stashes
// the resulting tenant (org + user) on the request context for the tools. A
// missing/invalid token gets a 401 carrying the RFC 9728 resource-metadata pointer
// so the client knows where to start the OAuth flow.
package auth

import (
	"context"
	"net/http"
	"strings"

	coreauth "github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
)

// tokenVerifier is the subset of internal/auth.Verifier the gate needs (so tests
// can inject a fake).
type tokenVerifier interface {
	Verify(ctx context.Context, token string) (*coreauth.Claims, error)
}

type tenantKey struct{}

// WithTenant returns ctx carrying the authenticated tenant.
func WithTenant(ctx context.Context, t store.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey{}, t)
}

// TenantFromContext returns the tenant set by Middleware (tools read this).
func TenantFromContext(ctx context.Context) (store.Tenant, bool) {
	t, ok := ctx.Value(tenantKey{}).(store.Tenant)
	return t, ok
}

// Middleware validates the bearer token and injects the tenant, then calls next.
// resourceMetadataURL is the absolute URL of this server's
// /.well-known/oauth-protected-resource, advertised on a 401 (RFC 9728) so MCP
// clients can discover the authorization server and begin the OAuth flow.
func Middleware(v tokenVerifier, resourceMetadataURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			unauthorized(w, resourceMetadataURL, "missing bearer token")
			return
		}
		claims, err := v.Verify(r.Context(), tok)
		if err != nil {
			unauthorized(w, resourceMetadataURL, "invalid token")
			return
		}
		t := store.Tenant{OrgID: claims.OrgID, UserID: claims.UserID()}
		if t.OrgID == "" {
			unauthorized(w, resourceMetadataURL, "token has no organization")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithTenant(r.Context(), t)))
	})
}

// bearer extracts the token from "Authorization: Bearer <token>" (case-insensitive
// scheme), or "" when absent/malformed.
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func unauthorized(w http.ResponseWriter, resourceMetadataURL, detail string) {
	if resourceMetadataURL != "" {
		w.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+resourceMetadataURL+`"`)
	}
	http.Error(w, "401 Unauthorized: "+detail, http.StatusUnauthorized)
}
