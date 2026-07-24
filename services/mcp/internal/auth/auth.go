// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package auth is the MCP service's auth gate. It accepts EITHER a Better Auth
// OAuth access token (validated against the platform's EdDSA/JWKS verifier — the
// claude.ai connector path) OR a Dropway org API key (dw_live_…, validated
// through the Go API so the key policy has exactly one implementation — the
// header-auth path for Claude Code CLI / Cursor / CI). Either way the resolved
// tenant (org + user) is stashed on the request context for the tools, along
// with the raw credential so write tools can forward it to the Go API (which
// accepts both credential kinds and re-authorizes every write itself). A
// missing/invalid credential gets a 401 carrying the RFC 9728 resource-metadata
// pointer so OAuth clients know where to start the flow.
package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/internal/apikey"
	coreauth "github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
)

// tokenVerifier is the subset of internal/auth.Verifier the gate needs (so tests
// can inject a fake).
type tokenVerifier interface {
	Verify(ctx context.Context, token string) (*coreauth.Claims, error)
}

// KeyValidator authenticates a dw_live_ API-key secret to a tenant. Implemented
// by NewAPIKeyValidator (which asks the Go API); nil in the gate config means
// keys are not accepted on this deployment (no API_URL → read-only server).
type KeyValidator interface {
	ValidateAPIKey(ctx context.Context, secret string) (store.Tenant, error)
}

type tenantKey struct{}
type tokenKey struct{}

// WithTenant returns ctx carrying the authenticated tenant.
func WithTenant(ctx context.Context, t store.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey{}, t)
}

// TenantFromContext returns the tenant set by the gate (tools read this).
func TenantFromContext(ctx context.Context) (store.Tenant, bool) {
	t, ok := ctx.Value(tenantKey{}).(store.Tenant)
	return t, ok
}

// WithToken returns ctx carrying the raw authenticated credential (OAuth token or
// API key), so write tools can forward it to the Go API — which accepts both and
// re-runs its own authn/authz per request — to perform mutations under the
// caller's identity.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}

// TokenFromContext returns the raw bearer credential set by the gate.
func TokenFromContext(ctx context.Context) (string, bool) {
	tok, ok := ctx.Value(tokenKey{}).(string)
	return tok, ok
}

// Config carries the gate's dependencies. Verifier and ResourceMetadataURL are
// required; Emitter (analytics) and Keys (API-key support) are optional.
type Config struct {
	Verifier            tokenVerifier
	ResourceMetadataURL string
	Emitter             analytics.Emitter
	Keys                KeyValidator
}

// The gate's 401 bodies are written for the READER of the error — the MCP
// client's LLM and its user — not just for logs: they say what was rejected and
// how to recover. detailInvalidToken covers the refresh-token "family
// revocation" case explicitly: replaying an already-rotated refresh token
// revokes ALL of a connection's tokens as a security measure, after which every
// access token is rejected here until the user re-authorizes — the client
// cannot fix that with a silent refresh.
const (
	detailInvalidToken = "invalid token. The access token was rejected (expired, revoked, " +
		"or minted for the wrong audience). Your MCP client should refresh it automatically; " +
		"if this error persists, the connection's refresh token has likely been revoked — " +
		"for example after a refresh-token reuse triggered a security revocation of ALL of " +
		"this connection's tokens (family revocation) — and the user must re-authorize the " +
		"Dropway connection (in Claude: Settings → Connectors → Dropway → reconnect; in other " +
		"clients, re-run the OAuth connect flow)."

	detailNoOrg = "token has no organization. The user signed in but has no active " +
		"organization, so no tenant can be resolved. Ask the user to open the Dropway " +
		"dashboard, create or select an organization, then reconnect this MCP connection."

	detailKeyRejected = "invalid token. The API key was rejected — it may be revoked, " +
		"expired, disabled by the org's API-keys kill switch, or unknown. Ask a Dropway " +
		"dashboard admin to check Settings → API keys and mint a new key if needed."

	detailKeysNotAccepted = "api keys are not accepted by this server (it runs read-only, " +
		"with no API_URL configured). Connect with OAuth instead, or ask the operator to " +
		"set API_URL on the MCP service to enable API-key auth."
)

// Middleware is Gate without analytics or API-key support (rejections are still
// logged). Kept as the back-compatible constructor for tests.
func Middleware(v tokenVerifier, resourceMetadataURL string, next http.Handler) http.Handler {
	return Gate(Config{Verifier: v, ResourceMetadataURL: resourceMetadataURL}, next)
}

// MiddlewareObserved is Gate without API-key support. Kept as the
// back-compatible constructor for tests.
func MiddlewareObserved(v tokenVerifier, resourceMetadataURL string, emitter analytics.Emitter, next http.Handler) http.Handler {
	return Gate(Config{Verifier: v, ResourceMetadataURL: resourceMetadataURL, Emitter: emitter}, next)
}

// Gate authenticates the request and injects the tenant + raw credential, then
// calls next. A dw_live_-prefixed bearer routes to the key validator; everything
// else is verified as an OAuth JWT. cfg.ResourceMetadataURL is the absolute URL
// of this server's /.well-known/oauth-protected-resource, advertised on a 401
// (RFC 9728) so MCP clients can discover the authorization server and begin the
// OAuth flow. Every rejection of a PRESENTED credential is captured to analytics
// as an `auth_rejected` event with a `step` pinpointing the failing stage
// (emitter nil → logs only); the credential-less 401 challenge that starts every
// OAuth connect is logged but NOT captured (it isn't an error).
func Gate(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			// Normal first step of the flow: an unauthenticated client gets a 401 +
			// RFC 9728 pointer and then starts OAuth. Logged so the connection attempt
			// is visible in the trace (not an error).
			slog.Info("mcp auth: unauthenticated request → 401 challenge",
				"method", r.Method, "path", r.URL.Path)
			unauthorized(w, cfg.ResourceMetadataURL, "missing bearer token")
			return
		}

		// API-key path: routed by the syntactic dw_live_ prefix, authenticated by
		// the validator (which delegates to the Go API's fail-closed key policy).
		if apikey.HasPrefix(tok) {
			if cfg.Keys == nil {
				slog.Warn("mcp auth: api key presented but key auth is not configured (no API_URL)",
					"path", r.URL.Path)
				analytics.CaptureAuthRejected(r.Context(), cfg.Emitter, analytics.AuthRejection{
					Surface: "mcp", Kind: "api_key", Step: "api_key_auth",
					Reason: "api keys not accepted (server has no API_URL)",
					Method: r.Method, Path: r.URL.Path,
				})
				unauthorized(w, cfg.ResourceMetadataURL, detailKeysNotAccepted)
				return
			}
			t, err := cfg.Keys.ValidateAPIKey(r.Context(), tok)
			if err != nil {
				// Uniform client-facing 401 (no oracle); real reason server-side only.
				slog.Warn("mcp auth: api key rejected", "err", err.Error(), "path", r.URL.Path)
				analytics.CaptureAuthRejected(r.Context(), cfg.Emitter, analytics.AuthRejection{
					Surface: "mcp", Kind: "api_key", Step: "api_key_auth",
					Reason: err.Error(), Method: r.Method, Path: r.URL.Path,
				})
				unauthorized(w, cfg.ResourceMetadataURL, detailKeyRejected)
				return
			}
			slog.Info("mcp auth: api key verified",
				"user_id", t.UserID, "org_id", t.OrgID, "path", r.URL.Path)
			ctx := WithToken(WithTenant(r.Context(), t), tok)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		claims, err := cfg.Verifier.Verify(r.Context(), tok)
		if err != nil {
			// Log WHY (and the token's unverified aud/iss) so an audience/issuer
			// mismatch is diagnosable — the gate is otherwise a silent 401.
			aud, iss := coreauth.UnverifiedAudIss(tok)
			slog.Warn("mcp auth: token verification failed",
				"err", err.Error(), "token_aud", aud, "token_iss", iss, "path", r.URL.Path)
			analytics.CaptureAuthRejected(r.Context(), cfg.Emitter, analytics.AuthRejection{
				Surface: "mcp", Kind: "jwt", Step: "jwt_verify",
				Reason: err.Error(), TokenAud: aud, TokenIss: iss,
				Method: r.Method, Path: r.URL.Path,
			})
			unauthorized(w, cfg.ResourceMetadataURL, detailInvalidToken)
			return
		}
		t := store.Tenant{OrgID: claims.OrgID, UserID: claims.UserID()}
		if t.OrgID == "" {
			slog.Warn("mcp auth: verified token has no org_id",
				"sub", claims.UserID(), "path", r.URL.Path)
			analytics.CaptureAuthRejected(r.Context(), cfg.Emitter, analytics.AuthRejection{
				Surface: "mcp", Kind: "jwt", Step: "org_claim",
				Reason: "token has no organization", Method: r.Method, Path: r.URL.Path,
			})
			unauthorized(w, cfg.ResourceMetadataURL, detailNoOrg)
			return
		}
		slog.Info("mcp auth: token verified",
			"user_id", t.UserID, "org_id", t.OrgID, "path", r.URL.Path)
		ctx := WithToken(WithTenant(r.Context(), t), tok)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// --- API-key validation via the Go API ---------------------------------------

// MeClient is the slice of apiclient.Client the key validator needs (so tests
// can inject a fake).
type MeClient interface {
	Me(ctx context.Context, token string) (userID, orgID string, err error)
}

// apiKeyValidator authenticates a key by calling the Go API's GET /v1/me with the
// key as the bearer. The API's auth boundary runs its full fail-closed key policy
// (liveness, org kill switch, creator-membership re-check, rate limits) — the MCP
// server deliberately does NOT reimplement any of it, so the policy cannot drift
// between services. Successes are cached briefly (by key hash, never the secret)
// so an agent's burst of tool calls costs one API round-trip, not one per call;
// the TTL bounds how long a just-revoked key keeps working here (writes are safe
// regardless — the API re-authenticates the forwarded key on every write).
type apiKeyValidator struct {
	api MeClient
	ttl time.Duration

	mu    sync.Mutex
	cache map[string]cachedKey // apikey.Hash(secret) → tenant
}

type cachedKey struct {
	tenant  store.Tenant
	expires time.Time
}

// DefaultKeyCacheTTL bounds the revocation lag for cached key validations. Reads
// through the MCP can outlive a revocation by at most this long; writes cannot
// (the API re-checks the key itself on every forwarded write).
const DefaultKeyCacheTTL = 60 * time.Second

// NewAPIKeyValidator builds the validator over the Go API client. ttl <= 0 uses
// DefaultKeyCacheTTL.
func NewAPIKeyValidator(api MeClient, ttl time.Duration) KeyValidator {
	if ttl <= 0 {
		ttl = DefaultKeyCacheTTL
	}
	return &apiKeyValidator{api: api, ttl: ttl, cache: map[string]cachedKey{}}
}

func (v *apiKeyValidator) ValidateAPIKey(ctx context.Context, secret string) (store.Tenant, error) {
	if _, err := apikey.Parse(secret); err != nil {
		return store.Tenant{}, err // malformed → generic 401, no API call
	}
	hash := apikey.Hash(secret)

	v.mu.Lock()
	if c, ok := v.cache[hash]; ok && time.Now().Before(c.expires) {
		v.mu.Unlock()
		return c.tenant, nil
	}
	v.mu.Unlock()

	userID, orgID, err := v.api.Me(ctx, secret)
	if err != nil {
		return store.Tenant{}, err
	}
	if orgID == "" || userID == "" {
		return store.Tenant{}, errAPIKeyNoTenant
	}
	t := store.Tenant{OrgID: orgID, UserID: userID}

	v.mu.Lock()
	v.cache[hash] = cachedKey{tenant: t, expires: time.Now().Add(v.ttl)}
	// Opportunistic sweep so the map can't grow unboundedly under a spray of
	// distinct valid keys (invalid keys are never cached).
	if len(v.cache) > 1024 {
		now := time.Now()
		for k, c := range v.cache {
			if now.After(c.expires) {
				delete(v.cache, k)
			}
		}
	}
	v.mu.Unlock()
	return t, nil
}

type apiKeyNoTenantError struct{}

func (apiKeyNoTenantError) Error() string { return "mcp/auth: api key resolved to no tenant" }

var errAPIKeyNoTenant = apiKeyNoTenantError{}

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
