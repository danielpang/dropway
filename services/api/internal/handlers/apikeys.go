// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"github.com/danielpang/dropway/internal/apikey"
	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// APIKeyStore is the slice of the data layer the API-key feature needs. It is kept
// separate from the large SiteStore interface so the feature composes without
// widening SiteStore (and every test fake of it). *store.Store satisfies both.
type APIKeyStore interface {
	// ResolveAPIKey looks a key up by sha256 hash at the auth boundary (bypasses RLS
	// via the definer function; no tenant context yet).
	ResolveAPIKey(ctx context.Context, keyHash string) (store.APIKeyPrincipal, error)
	// TouchAPIKeyLastUsed best-effort stamps last_used_at (throttled in SQL).
	TouchAPIKeyLastUsed(ctx context.Context, orgID, userID, keyID string) error
	// MemberRole is the LIVE role re-check for creator-membership fail-closed + the
	// synthesized claims' role.
	MemberRole(ctx context.Context, orgID, userID string) (string, error)
	CreateAPIKey(ctx context.Context, t store.Tenant, p store.CreateAPIKeyParams) (store.APIKey, error)
	ListAPIKeys(ctx context.Context, t store.Tenant) ([]store.APIKey, error)
	RevokeAPIKey(ctx context.Context, t store.Tenant, id string, ctxProv audit.Context) (store.APIKey, error)
}

// ---------------------------------------------------------------------------
// Key authenticator (the middleware.KeyAuthenticator implementation)
// ---------------------------------------------------------------------------

// keyAuthenticator resolves a presented API-key secret to synthesized claims,
// applying the fail-closed liveness policy and a per-key rate limit. It implements
// middleware.KeyAuthenticator.
type keyAuthenticator struct {
	store         APIKeyStore
	limiter       *rateLimiter
	allowFallback bool // mirror AllowJWTRoleFallback for the missing-member-table case
}

// NewKeyAuthenticator builds the boundary authenticator. limiter may be nil (no
// per-key rate limiting). allowFallback governs the self-host-pre-Better-Auth case
// (identity.member absent): true → resolve as a member-role principal so keys still
// work; false (default, strict) → deny, since creator membership can't be confirmed.
func NewKeyAuthenticator(s APIKeyStore, limiter *rateLimiter, allowFallback bool) *keyAuthenticator {
	return &keyAuthenticator{store: s, limiter: limiter, allowFallback: allowFallback}
}

// AuthenticateAPIKey resolves the secret and returns the synthesized principal, or
// an error the middleware maps to a uniform 401 (any liveness failure) or a 429
// (*middleware.RateLimitedError). The steps mirror the JWT path's fail-closed
// posture: nothing is trusted that can't be re-confirmed live.
func (k *keyAuthenticator) AuthenticateAPIKey(ctx context.Context, secret string) (*middleware.KeyPrincipal, error) {
	if k.store == nil {
		return nil, errors.New("apikey: no store configured")
	}
	if _, err := apikey.Parse(secret); err != nil {
		return nil, err // malformed → generic 401
	}
	princ, err := k.store.ResolveAPIKey(ctx, apikey.Hash(secret))
	if err != nil {
		return nil, err // unknown hash (ErrNotFound) or DB error → generic 401
	}

	// Fail-closed liveness, in the same spirit as the JWT path's live re-checks.
	if princ.RevokedAt != nil {
		return nil, errors.New("apikey: revoked")
	}
	if princ.ExpiresAt != nil && !princ.ExpiresAt.After(time.Now()) {
		return nil, errors.New("apikey: expired")
	}
	if princ.OrgStatus != "active" {
		return nil, errors.New("apikey: org not active")
	}
	if !princ.APIKeysEnabled {
		return nil, errors.New("apikey: org kill switch on")
	}

	// The key acts AS its creator; a creator who left the org kills the key. The
	// live role is the creator's real role (carried for attribution) — the
	// member-level ceiling is enforced downstream at the admin gate, not here.
	role, err := k.liveRole(ctx, princ.OrgID, princ.CreatedBy)
	if err != nil {
		return nil, err
	}

	// Per-key rate limit AFTER resolution (need the key id). The lookup is a single
	// indexed SELECT, so resolving-then-limiting is cheap; presigned blob PUTs never
	// hit the API, so bulk uploads stay fast.
	if k.limiter != nil {
		if ok, retryAfter := k.limiter.allow("apikey:" + princ.ID); !ok {
			return nil, &middleware.RateLimitedError{RetryAfter: retryAfter}
		}
	}

	// Best-effort last-used stamp, off the hot path (throttled in SQL). A failure
	// here must never fail the request.
	k.touchAsync(princ)

	claims := &auth.Claims{OrgID: princ.OrgID, Role: role}
	claims.Subject = princ.CreatedBy
	claims.IssuedAt = jwt.NewNumericDate(time.Now())
	return &middleware.KeyPrincipal{Claims: claims, KeyID: princ.ID}, nil
}

// liveRole returns the creator's current role, failing closed when membership can't
// be confirmed. A departed creator (ErrNoMembership) always denies; a missing
// member table denies unless allowFallback (then member-role, matching the
// AllowJWTRoleFallback posture used elsewhere).
func (k *keyAuthenticator) liveRole(ctx context.Context, orgID, userID string) (string, error) {
	role, err := k.store.MemberRole(ctx, orgID, userID)
	if err == nil {
		return role, nil
	}
	if errors.Is(err, store.ErrAuthSchemaUnavailable) && k.allowFallback {
		return store.RoleMember, nil
	}
	// ErrNoMembership (creator left), ErrAuthSchemaUnavailable without fallback, or
	// any other error → deny.
	return "", errors.New("apikey: creator membership could not be confirmed")
}

// touchAsync stamps last_used_at without blocking the request. It uses a detached
// context with a short deadline so a slow/failed write can't wedge anything.
func (k *keyAuthenticator) touchAsync(p store.APIKeyPrincipal) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = k.store.TouchAPIKeyLastUsed(ctx, p.OrgID, p.CreatedBy, p.ID)
	}()
}

// ---------------------------------------------------------------------------
// Management endpoints — session-JWT-only, admin/owner (re-checked live).
// A key can never manage keys: requireAdmin refuses keyed callers (the ceiling).
// ---------------------------------------------------------------------------

type apiKeyResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	CreatedBy  string     `json:"created_by"`
	Scopes     []string   `json:"scopes,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// createAPIKeyResponse is the ONLY response that ever carries the full secret
// (`key`). Every later read returns metadata + prefix only.
type createAPIKeyResponse struct {
	apiKeyResponse
	Key string `json:"key"`
}

func apiKeyView(k store.APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID: k.ID, Name: k.Name, KeyPrefix: k.KeyPrefix, CreatedBy: k.CreatedBy,
		Scopes: k.Scopes, LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt,
		CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt,
	}
}

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

// CreateAPIKey mints an org-scoped API key (POST /v1/api-keys). Admin/owner only
// (re-checked live; keyed callers are refused by the ceiling). The full secret is
// generated server-side, returned ONCE in this response, and never persisted in
// plaintext — only its sha256 hash + display prefix are stored.
func (a *API) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireKeysStore(w) || !a.requireAdmin(w, r, t) {
		return
	}

	var req createAPIKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpx.WriteError(w, fmt.Errorf("%w: name is required", httpx.ErrBadRequest))
		return
	}
	if len(name) > 100 {
		httpx.WriteError(w, fmt.Errorf("%w: name is too long (max 100 chars)", httpx.ErrBadRequest))
		return
	}

	secret, err := apikey.Generate()
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	key, err := a.Keys.CreateAPIKey(r.Context(), t, store.CreateAPIKeyParams{
		Name:      name,
		KeyHash:   apikey.Hash(secret),
		KeyPrefix: apikey.DisplayPrefix(secret),
		Ctx:       auditCtx(r),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("api key created", "org_id", t.OrgID, "key_id", key.ID, "created_by", t.UserID)
	httpx.WriteJSON(w, http.StatusCreated, createAPIKeyResponse{
		apiKeyResponse: apiKeyView(key),
		Key:            secret,
	})
}

// ListAPIKeys returns the active org's keys, newest first (GET /v1/api-keys).
// Admin/owner only. Metadata + prefix only — never the secret.
func (a *API) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireKeysStore(w) || !a.requireAdmin(w, r, t) {
		return
	}
	keys, err := a.Keys.ListAPIKeys(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]apiKeyResponse, len(keys))
	for i, k := range keys {
		out[i] = apiKeyView(k)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// RevokeAPIKey revokes a key by id (DELETE /v1/api-keys/{id}). Admin/owner only;
// idempotent; the very next request with that key 401s. 404 for an unknown id.
func (a *API) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireKeysStore(w) || !a.requireAdmin(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httpx.WriteError(w, fmt.Errorf("%w: key id is required", httpx.ErrBadRequest))
		return
	}
	key, err := a.Keys.RevokeAPIKey(r.Context(), t, id, auditCtx(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("api key revoked", "org_id", t.OrgID, "key_id", key.ID, "revoked_by", t.UserID)
	httpx.WriteJSON(w, http.StatusOK, apiKeyView(key))
}

// requireKeysStore returns the key store or writes a 503 (DB-less deployment).
func (a *API) requireKeysStore(w http.ResponseWriter) bool {
	if a.Keys == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "api keys not configured"})
		return false
	}
	return true
}
