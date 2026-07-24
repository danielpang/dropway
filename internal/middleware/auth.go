// Package middleware holds the Go API's HTTP middleware and the RLS tenant-context
// helper. The Auth middleware is the front door of the authz boundary:
// it verifies the Bearer EdDSA JWT via internal/auth
// and stashes the verified *auth.Claims in the request context. The rlstx helper
// (rlstx.go) opens a transaction and sets the per-request tenant GUCs that the
// Postgres RLS policies read.
package middleware

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/danielpang/dropway/internal/apikey"
	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/logx"
)

// claimsKey is the unexported context key under which verified claims are
// stored. Unexported so only this package can write it — callers read via
// ClaimsFromContext.
type claimsKey struct{}

// apiKeyIDKey is the unexported context key under which the authenticating API
// key's id is stored, when (and only when) the request authenticated via an API
// key rather than a user session. Its presence is the "keyed auth" marker the
// handlers use to enforce the member-level role ceiling and to stamp
// audit_log.actor_token.
type apiKeyIDKey struct{}

// Verifier is the slice of *auth.Verifier the middleware depends on. Defining it
// as an interface keeps the middleware unit-testable with a fake verifier (no
// live JWKS endpoint needed).
type Verifier interface {
	Verify(ctx context.Context, token string) (*auth.Claims, error)
}

// KeyPrincipal is the resolved identity for an API-key-authenticated request: the
// SYNTHESIZED claims (sub = the key's created_by, org_id, and the creator's LIVE
// role) plus the key id for audit/rate-limit/ceiling. Everything downstream
// consumes *auth.Claims, so a keyed request flows through the exact same tenant
// context, quota, and handler paths as a session request.
type KeyPrincipal struct {
	Claims *auth.Claims
	KeyID  string
}

// KeyAuthenticator authenticates a presented API-key secret at the boundary: it
// resolves the key, applies the fail-closed liveness policy (revoked / expired /
// suspended org / kill switch / creator no longer a member), synthesizes claims,
// and rate-limits per key. It is implemented in the handlers layer (which owns the
// Store + limiter); the middleware depends only on this interface so it stays
// unit-testable and takes no store dependency.
//
// It returns *RateLimitedError when the request is over a rate budget (→ 429), or
// a generic error for every authentication failure (→ a uniform 401, so the
// boundary is not an existence/liveness oracle). clientIP is the resolved client
// address (for the per-IP pre-throttle); it may be empty when unknown.
type KeyAuthenticator interface {
	AuthenticateAPIKey(ctx context.Context, secret, clientIP string) (*KeyPrincipal, error)
}

// RateLimitedError signals a keyed request exceeded its per-key rate budget. It
// carries the wait until the next token so the middleware can render Retry-After.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string { return "api key rate limited" }

// Auth returns middleware that requires a valid Bearer EdDSA JWT, with no API-key
// path. It is the back-compatible constructor used by tests and by surfaces that
// never accept keys (keyed requests there fall through to JWT verification and get
// a generic 401). Equivalent to AuthWithKeys(v, nil).
func Auth(v Verifier) func(http.Handler) http.Handler {
	return AuthWithKeys(v, nil)
}

// AuthWithKeys returns middleware that requires either a valid Bearer EdDSA JWT or
// a valid Bearer API key. A token carrying the API-key prefix is routed to the key
// authenticator (when configured); everything else is verified as a JWT. On success
// it injects the verified/synthesized *auth.Claims into the request context (and,
// for a keyed request, the key id marker); on any failure it renders 401/429 and
// does NOT call the next handler.
//
// The public serve path carries no credential and must never be wrapped by this —
// only the control-plane (api.dropway.dev) routes are.
func AuthWithKeys(v Verifier, keys KeyAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				httpx.WriteError(w, wrapUnauthorized("missing or malformed Authorization header"))
				return
			}

			// Route by token shape: a dw_live_ prefix is an API key, everything else
			// is a Better Auth JWT. The prefix check is syntactic only — authentication
			// is the resolver's job.
			if keys != nil && apikey.HasPrefix(token) {
				princ, err := keys.AuthenticateAPIKey(r.Context(), token, clientIP(r))
				if err != nil {
					var rle *RateLimitedError
					if errors.As(err, &rle) {
						httpx.WriteRateLimited(w, rle.RetryAfter, "rate_limited", "too many requests, please slow down")
						return
					}
					// Uniform 401 for unknown / revoked / expired / disabled-org /
					// creator-departed — no oracle distinguishing them. The real
					// reason is logged server-side only.
					logx.FromContext(r.Context()).Warn("auth: api key rejected",
						"err", err.Error(), "path", r.URL.Path)
					httpx.WriteError(w, wrapUnauthorized("invalid token"))
					return
				}
				ctx := context.WithValue(r.Context(), claimsKey{}, princ.Claims)
				ctx = context.WithValue(ctx, apiKeyIDKey{}, princ.KeyID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			claims, err := v.Verify(r.Context(), token)
			if err != nil {
				// Don't echo the verifier error verbatim (it can hint at why a
				// forged token failed); use a generic unauthorized message. Log WHY
				// server-side (with the token's unverified aud/iss) so a rejected
				// token — e.g. an MCP-forwarded write whose audience the API doesn't
				// accept — is diagnosable instead of a silent 401.
				aud, iss := auth.UnverifiedAudIss(token)
				logx.FromContext(r.Context()).Warn("auth: token verification failed",
					"err", err.Error(), "token_aud", aud, "token_iss", iss, "path", r.URL.Path)
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

// APIKeyIDFromContext returns the authenticating API key's id when the request
// authenticated via an API key (and reports ok=false for a user-session request).
// Its presence is the keyed-auth marker: handlers use it to enforce the
// member-level role ceiling and to stamp audit_log.actor_token.
func APIKeyIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(apiKeyIDKey{}).(string)
	return id, ok && id != ""
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

// clientIP returns the best-effort client address for the per-IP key throttle. chi's
// RealIP middleware has already rewritten RemoteAddr from a trusted forwarded
// header, so RemoteAddr is the resolved value; we keep just the host. The value is
// used ONLY as a rate-limit key (never for authz), so a spoofed header only
// re-buckets the attacker onto themselves.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
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
