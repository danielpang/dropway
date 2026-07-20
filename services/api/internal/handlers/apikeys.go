// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

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

// touchInterval is how often a key's last_used_at is refreshed. It matches the
// SQL-side throttle in TouchAPIKeyLastUsed; the in-process cache below front-runs
// that throttle so the common case does NO database work at all.
const touchInterval = 5 * time.Minute

// keyAuthenticator resolves a presented API-key secret to synthesized claims,
// applying the fail-closed liveness policy and rate limits. It implements
// middleware.KeyAuthenticator.
type keyAuthenticator struct {
	store         APIKeyStore
	keyLimiter    *rateLimiter // per-key-id budget (the primary control)
	ipLimiter     *rateLimiter // per-client-IP budget, consulted BEFORE any DB work
	allowFallback bool         // mirror AllowJWTRoleFallback for the missing-member-table case
	lastTouch     sync.Map     // keyID -> time.Time; gates the last_used_at write
}

// NewKeyAuthenticator builds the boundary authenticator. Either limiter may be nil
// (that control disabled). allowFallback governs the self-host-pre-Better-Auth case
// (identity.member absent): true → resolve as a member-role principal so keys still
// work; false (default, strict) → deny, since creator membership can't be confirmed.
func NewKeyAuthenticator(s APIKeyStore, keyLimiter, ipLimiter *rateLimiter, allowFallback bool) *keyAuthenticator {
	return &keyAuthenticator{store: s, keyLimiter: keyLimiter, ipLimiter: ipLimiter, allowFallback: allowFallback}
}

// AuthenticateAPIKey resolves the secret and returns the synthesized principal, or
// an error the middleware maps to a uniform 401 (any liveness failure) or a 429
// (*middleware.RateLimitedError). The steps mirror the JWT path's fail-closed
// posture: nothing is trusted that can't be re-confirmed live.
//
// Throttling is two-tiered so neither failed nor over-budget auth can hammer the
// database: a generous per-CLIENT-IP bucket is consulted FIRST, before any lookup,
// which bounds a spray of bad/random secrets (the resolve query never runs once the
// IP is over budget) the way the password exchange throttles by IP before bcrypt;
// then a tighter per-KEY bucket is consulted right after resolution, before the
// identity.member re-check, so a flood on one valid key can't drive that second
// query either. Legitimate CI behind a shared egress IP is why the IP bucket is
// generous relative to the per-key one (see WireAPIKeyAuth defaults).
func (k *keyAuthenticator) AuthenticateAPIKey(ctx context.Context, secret, clientIP string) (*middleware.KeyPrincipal, error) {
	if k.store == nil {
		return nil, errors.New("apikey: no store configured")
	}

	// IP pre-throttle: bounds unauthenticated resolve load before the first query.
	if k.ipLimiter != nil && clientIP != "" {
		if ok, retryAfter := k.ipLimiter.allow("apikey-ip:" + clientIP); !ok {
			return nil, &middleware.RateLimitedError{RetryAfter: retryAfter}
		}
	}

	if _, err := apikey.Parse(secret); err != nil {
		return nil, err // malformed → generic 401
	}
	princ, err := k.store.ResolveAPIKey(ctx, apikey.Hash(secret))
	if err != nil {
		return nil, err // unknown hash (ErrNotFound) or DB error → generic 401
	}

	// Fail-closed liveness — all in-memory on the already-fetched row (no DB).
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

	// Per-key rate limit BEFORE the identity.member re-check, so an over-budget valid
	// key is denied without driving the second (membership) query.
	if k.keyLimiter != nil {
		if ok, retryAfter := k.keyLimiter.allow("apikey:" + princ.ID); !ok {
			return nil, &middleware.RateLimitedError{RetryAfter: retryAfter}
		}
	}

	// The key acts AS its creator; a creator who left the org kills the key. The
	// live role is the creator's real role (carried for attribution) — the
	// member-level ceiling is enforced downstream at the admin gate, not here.
	role, err := k.liveRole(ctx, princ.OrgID, princ.CreatedBy)
	if err != nil {
		return nil, err
	}

	// Best-effort last-used stamp, off the hot path and gated by the in-process
	// interval cache so it opens a transaction at most once per touchInterval per key
	// (not once per request). A failure here must never fail the request.
	k.maybeTouch(princ)

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

// maybeTouch stamps last_used_at without blocking the request, but only when this
// process hasn't already stamped the key within touchInterval — so the common case
// (a busy key) does NO database work, instead of opening a full transaction per
// request for a field that only needs to move every few minutes. The SQL throttle
// in TouchAPIKeyLastUsed remains the cross-process backstop. A benign race (two
// requests both seeing a stale entry) costs at most one extra no-op transaction.
func (k *keyAuthenticator) maybeTouch(p store.APIKeyPrincipal) {
	now := time.Now()
	if v, ok := k.lastTouch.Load(p.ID); ok {
		if last, ok := v.(time.Time); ok && now.Sub(last) < touchInterval {
			return
		}
	}
	k.lastTouch.Store(p.ID, now)
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
	// Reject control characters (newlines, escapes, etc.): the name is echoed into
	// the audit trail and the dashboard key list, so a control char would enable
	// log-line / terminal spoofing.
	if hasControlChar(name) {
		httpx.WriteError(w, fmt.Errorf("%w: name must not contain control characters", httpx.ErrBadRequest))
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

// hasControlChar reports whether s contains any Unicode control character (C0/C1
// range, including newlines and terminal escapes). Used to keep free-text that
// flows into the audit trail / dashboard free of log- and terminal-spoofing input.
func hasControlChar(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
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
