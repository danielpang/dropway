// Package middleware holds the Go API's HTTP middleware and the RLS tenant-context
// helper. The Auth middleware is the front door of the authz boundary
// (docs/ARCHITECTURE.md §3): it verifies the Bearer EdDSA JWT via internal/auth
// and stashes the verified *auth.Claims in the request context. The rlstx helper
// (rlstx.go) opens a transaction and sets the per-request tenant GUCs that the
// Postgres RLS policies read.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/httpx"
)

// claimsKey is the unexported context key under which verified claims are
// stored. Unexported so only this package can write it — callers read via
// ClaimsFromContext.
type claimsKey struct{}

// Verifier is the slice of *auth.Verifier the middleware depends on. Defining it
// as an interface keeps the middleware unit-testable with a fake verifier (no
// live JWKS endpoint needed).
type Verifier interface {
	Verify(ctx context.Context, token string) (*auth.Claims, error)
}

// Auth returns middleware that requires a valid Bearer EdDSA JWT. On success it
// injects the verified *auth.Claims into the request context; on any failure it
// renders a 401 and does NOT call the next handler.
//
// The public serve path carries no JWT and must never be wrapped by this — only
// the control-plane (api.dropway.dev) routes are.
func Auth(v Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				httpx.WriteError(w, wrapUnauthorized("missing or malformed Authorization header"))
				return
			}
			claims, err := v.Verify(r.Context(), token)
			if err != nil {
				// Don't echo the verifier error verbatim (it can hint at why a
				// forged token failed); use a generic unauthorized message.
				httpx.WriteError(w, wrapUnauthorized("invalid token"))
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext returns the verified claims placed by Auth, if present.
// Handlers behind Auth can rely on ok==true.
func ClaimsFromContext(ctx context.Context) (*auth.Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(*auth.Claims)
	return c, ok
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
// The scheme match is case-insensitive per RFC 7235.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// wrapUnauthorized produces an error that httpx.WriteError maps to 401.
func wrapUnauthorized(msg string) error {
	return &unauthorizedError{msg: msg}
}

type unauthorizedError struct{ msg string }

func (e *unauthorizedError) Error() string { return e.msg }

// Is lets errors.Is(e, httpx.ErrUnauthorized) succeed so WriteError maps it to
// 401 without us importing httpx's sentinel into the struct.
func (e *unauthorizedError) Is(target error) bool { return target == httpx.ErrUnauthorized }
