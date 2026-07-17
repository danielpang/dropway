// Package middleware holds the Go API's HTTP middleware and the RLS tenant-context
// helper. The Auth middleware is the front door of the authz boundary:
// it verifies the Bearer EdDSA JWT via internal/auth
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

// ReauthRequiredCode is the machine-readable error code returned when a token
// verifies cryptographically but carries no usable tenant (empty sub/org_id —
// e.g. a session minted before the user's organization existed). Distinct from
// plain "unauthorized" so the dashboard/CLI can drive a re-authentication
// instead of treating it as a generic failure.
const ReauthRequiredCode = "reauth_required"

// Auth returns middleware that requires a valid Bearer EdDSA JWT. On success it
// injects the verified *auth.Claims into the request context; on any failure it
// renders a 401 and does NOT call the next handler.
//
// Tenant enforcement lives HERE, once: a verified token whose sub or org_id is
// empty is rejected at the boundary with the typed 401 body (reauth_required)
// rather than surfacing later as a store-level failure. This is the single
// authoritative check; the store's ErrMissingTenant remains only as
// defense-in-depth behind it. (History: empty-org tokens reached the store and
// produced opaque 500s — "claims missing user_id/org_id for RLS context" — that
// were patched in three separate places; enforcing at the boundary removes the
// class.)
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
			if claims.UserID() == "" || claims.OrgID == "" {
				// The token verified but carries no tenant. Two distinct causes with
				// different fixes, so the message names both rather than only "sign in
				// again": a user who never created an org (onboarding) is NOT helped by
				// re-authenticating — it re-mints the same org-less token. The dashboard
				// disambiguates via listOrganizations; the CLI shows this text.
				httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorBody{
					Error:   ReauthRequiredCode,
					Message: "session has no active organization; create one or sign in again",
				})
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
