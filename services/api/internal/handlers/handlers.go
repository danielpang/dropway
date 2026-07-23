// Package handlers implements the Go API's HTTP endpoints (the system of record).
// Phase 1 ships the core publish/serve loop, DB-backed:
// create/list/get sites, the deployments prepare→finalize→publish flow, and the
// identity echo + health check. Every authenticated handler runs under the RLS
// tenant context (via the store's tx-per-call SET LOCAL) and quota is checked at
// the cost-creating action (the open-core seam).
package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/logx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/skillseeds"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// presignTTL bounds how long a direct-to-store upload URL is valid.
const presignTTL = 15 * time.Minute

// defaultPreviewTTL is how long a version-preview host lives when PREVIEW_TTL_HOURS
// is unset: 7 days, renewable (re-creating an expired preview is the same call).
const defaultPreviewTTL = 7 * 24 * time.Hour

// previewTTL returns the configured preview lifetime, defaulting to 7 days.
func (a *API) previewTTL() time.Duration {
	if a.PreviewTTL > 0 {
		return a.PreviewTTL
	}
	return defaultPreviewTTL
}

// aiSpendPeriodStart returns the start of the AI spend window (cap + usage
// display), using the injected resolver when present, else the calendar month.
func (a *API) aiSpendPeriodStart(ctx context.Context, t store.Tenant) time.Time {
	if a.AISpendPeriodStart != nil {
		return a.AISpendPeriodStart(ctx, t)
	}
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// API holds the handler dependencies wired in main.go. Quota is the open-core
// seam (Unlimited in OSS, the cloud hard-cap provider under -tags cloud). Store,
// Objects, and Projection are the Phase-1 publish/serve loop; they may be nil in
// a DB-less deployment, in which case the DB-backed routes return 503.
//
// Phase 2 adds EdgeSigner (mints the host-scoped edge token + serves the edge
// JWKS) and Domains (the Cloudflare-for-SaaS custom-hostname provider). Both are
// optional: routes that need them return 503 when unset.
type API struct {
	Quota      quota.Provider
	Store      SiteStore
	Objects    ObjectStore
	Projection ProjectionWriter
	EdgeSigner EdgeSigner
	Domains    DomainProvider

	// Analytics is the optional product-analytics emitter (internal/analytics; a
	// PostHog client when POSTHOG_KEY is set, else nil/Noop). Handlers use it for
	// best-effort product events (e.g. site_created). nil → emissions are skipped.
	// The shared client's lifecycle is owned by main(); handlers only enqueue.
	Analytics analytics.Emitter

	// Revoker writes the hard-revocation denylist (revoked:user/site/org) the
	// serving Worker + /authz read. Optional: when nil, hard revocation degrades to
	// the short edge-token TTL only (the routes are still rewritten). In production
	// it is the same Cloudflare KV writer as Projection.
	Revoker EdgeRevoker

	// RevocationReader reads the same denylist so the /authz mint path refuses to
	// re-mint for a viewer whose JWT predates a revocation (H2). Optional: nil → the
	// mint-time denylist check is skipped (edge TTL + live re-checks remain). In
	// production it is the same Cloudflare KV reader as Projection/Revoker.
	RevocationReader EdgeRevocationReader

	// AllowJWTRoleFallback gates the requireAdmin fallback to the verified JWT role
	// claim when the Better Auth identity.member table is unavailable. Default false
	// (strict): admin-gated actions are DENIED when membership can't be confirmed
	// live. A self-host pre-Better-Auth can opt in (ALLOW_JWT_ROLE_FALLBACK=true).
	// See config.Config.AllowJWTRoleFallback ([LOW]).
	AllowJWTRoleFallback bool

	// ContentScheme / ContentPort shape the DISPLAY URLs the API returns for a site
	// (live_url / preview_url): scheme://host[:port]. They affect ONLY the rendered
	// URL — host_routes.host (and route:<host>) stay the bare host. Wired from
	// config.Config; ContentScheme defaults to "https" when empty (via ContentURL).
	ContentScheme string
	ContentPort   string

	// CustomDomainsEnabled reports whether the custom-domain feature is actually
	// backed by a real provider (Cloudflare for SaaS is configured). False in
	// self-host/dev, where the provider is the in-memory fake that can never reach
	// "verified". Surfaced on /v1/me so the dashboard hides the Domains UI when the
	// feature can't actually work. Wired from config in main.go.
	CustomDomainsEnabled bool

	// PasswordRateLimiter throttles the unauthenticated POST /v1/authz/password
	// exchange, keyed by (client IP + target host), as a first-layer brute-force /
	// denial-of-wallet control (M3). Each attempt otherwise burns a cost-12 bcrypt.
	// Optional: nil disables throttling (the bare unit-test constructor leaves it
	// unset); main.go wires a limiter for the real server.
	PasswordRateLimiter *rateLimiter

	// Keys is the data layer for org-scoped API keys (create/list/revoke +
	// resolve/touch). Separate from Store so the key feature composes without
	// widening the large SiteStore interface (and its test fakes). nil → the
	// /v1/api-keys management routes return 503 and no key auth is possible.
	Keys APIKeyStore

	// KeyAuth authenticates a presented API key at the boundary (resolve +
	// fail-closed liveness + per-key rate limit → synthesized claims). Wired from
	// Keys + a rate limiter in main.go and handed to middleware.AuthWithKeys. nil →
	// only JWT auth is accepted (keys 401).
	KeyAuth middleware.KeyAuthenticator

	// SkillSeeds are the embedded default preset skills materialized lazily per
	// org on the first skills touch (internal/skillseeds.Load, wired in main.go).
	// Empty → orgs start with no folders/presets (they can still create both).
	SkillSeeds []skillseeds.Seed

	// PreviewTTL is how long a version-preview host lives from creation/renewal
	// (PREVIEW_TTL_HOURS; default 7 days). The zero value falls back to
	// defaultPreviewTTL so a bare unit-test API still gets a sane deadline.
	PreviewTTL time.Duration

	// AI builder wiring. All optional: when AI or AIModels is nil the AI routes
	// return 503 (self-host without an OPENROUTER_API_KEY, or a DB-less API).
	AI       AITurnRunner   // runs one builder turn (streamed); *ai.Runner
	AIModels AIModelCatalog // OpenRouter model catalog for the picker
	AIGate   AIGate         // plan/card gate (cloud); nil → allow all
	// AIDefaultModel is the model used when a session omits one.
	AIDefaultModel string
	// AIMaxConcurrent bounds active AI sessions per site (0 → default 2).
	AIMaxConcurrent int
	// AISpendPeriodStart returns the start of the window the AI spend cap + the
	// "usage this month" display are computed over. Nil → the calendar month; the
	// cloud build injects the Stripe billing-period resolver so the number the
	// user sees matches what the cap enforces (the SAME resolver the AI runner's
	// PeriodStart uses).
	AISpendPeriodStart func(ctx context.Context, t store.Tenant) time.Time

	// Org memory wiring (docs/org-memory-scope.md). Both optional: when either
	// is nil the /v1/ai/memories + /v1/orgs/memory routes return 503 (self-host
	// without an EMBEDDINGS_API_KEY, or a DB-less API). Memory is the data
	// surface (*store.Store; separate from SiteStore like Keys); MemoryEmbedder
	// is the embeddings client; MemoryMaxPerOrg caps rows per org (0 →
	// unlimited).
	Memory          MemoryStore
	MemoryEmbedder  MemoryEmbedder
	MemoryMaxPerOrg int
	// MemoryExtract runs async extraction over shared chat logs (*ai.Runner).
	// nil → chat shares/appends don't feed memory.
	MemoryExtract MemoryExtractor
	// MemoryIndex chunks + embeds published site/skill content for retrieval
	// (*ai.Runner). nil → publishes aren't indexed.
	MemoryIndex MemoryIndexer

	// seededOrgs is a process-local set of org ids already observed as
	// skills-seeded, so ensureSkillsSeeded can skip its per-request DB round-trip
	// on the hot path (skills_seeded is monotonic, so a cached "seeded" never goes
	// stale; the DB advisory lock remains the source of truth for correctness).
	seededOrgs sync.Map
}

// ContentURL renders a content host as a client-facing display URL using the
// configured scheme + optional port: scheme://host[:port]. The bare host is what
// the serving server resolves by (Host header), so the scheme/port live only in
// the displayed URL. An empty ContentScheme defaults to "https" so a zero-value
// API (the bare unit-test constructor) still emits a well-formed https URL.
func (a *API) ContentURL(host string) string {
	scheme := a.ContentScheme
	if scheme == "" {
		scheme = "https"
	}
	url := scheme + "://" + host
	if a.ContentPort != "" {
		url += ":" + a.ContentPort
	}
	return url
}

// New constructs an API with only the quota seam (back-compat for the unit tests
// that don't need the DB). A nil provider defaults to Unlimited so a misconfigured
// wiring fails open to OSS behavior rather than panicking.
func New(q quota.Provider) *API {
	if q == nil {
		q = quota.Unlimited{}
	}
	return &API{Quota: q}
}

// NewFull constructs an API with the full Phase-1 dependency set.
func NewFull(q quota.Provider, s SiteStore, obj ObjectStore, proj ProjectionWriter) *API {
	a := New(q)
	a.Store = s
	a.Objects = obj
	a.Projection = proj
	return a
}

// Healthz is the unauthenticated liveness probe. It never touches the DB so it
// stays green during a database blip (readiness is a separate concern).
func (a *API) Healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// meResponse echoes the verified claims so the dashboard/CLI can confirm who the
// token authenticates as (verification proof #3).
type meResponse struct {
	UserID string `json:"user_id"`
	OrgID  string `json:"org_id"`
	Role   string `json:"role"`
	// CustomDomainsEnabled tells the dashboard whether to show the custom-domain UI
	// (true only when a real Cloudflare-for-SaaS provider is configured).
	CustomDomainsEnabled bool `json:"custom_domains_enabled"`
}

// Me returns the caller's verified identity.
func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, meResponse{
		UserID:               claims.UserID(),
		OrgID:                claims.OrgID,
		Role:                 claims.Role,
		CustomDomainsEnabled: a.CustomDomainsEnabled,
	})
}

// tenant derives the RLS tenant from verified claims. Callers that reach here are
// behind the Auth middleware, so claims are present; the bool guards the rare
// "mounted bare" path. A verified JWT can still lack a tenant (a session minted
// before the user's organization existed carries no org claim), and an empty
// org/user cannot be scoped for RLS — the store would fail closed with
// middleware.ErrMissingTenant as an opaque 500. Report !ok instead so callers
// render the 401 that tells the client to re-authenticate.
func tenant(ctx context.Context) (store.Tenant, bool) {
	c, ok := middleware.ClaimsFromContext(ctx)
	if !ok || c.OrgID == "" || c.UserID() == "" {
		return store.Tenant{}, false
	}
	return store.Tenant{OrgID: c.OrgID, UserID: c.UserID()}, true
}

// requireStore returns the store or writes a 503 and reports false. Used by the
// DB-backed routes so a DB-less deployment degrades cleanly.
func (a *API) requireStore(w http.ResponseWriter) bool {
	if a.Store == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "database not configured"})
		return false
	}
	return true
}

// requireSigner returns the edge signer or writes a 503 (the /authz mint/password
// exchange needs it).
func (a *API) requireSigner(w http.ResponseWriter) bool {
	if a.EdgeSigner == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "edge signer not configured"})
		return false
	}
	return true
}

// requireDomains returns the custom-domain provider or writes a 503.
func (a *API) requireDomains(w http.ResponseWriter) bool {
	if a.Domains == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "custom domains not configured"})
		return false
	}
	return true
}

// requireAdmin re-checks that the caller holds owner/admin in the active org by
// reading the LIVE member table (not just the JWT role claim) — the gate for
// access-policy / org-policy / role mutations ([HIGH]).
// It writes a 403 and returns false on a non-admin, an empty membership, or any
// re-check error.
//
// If the Better Auth identity.member table is unavailable (a self-host that hasn't run
// Better Auth yet), the behavior is STRICT BY DEFAULT ([LOW]):
// admin-gated actions are DENIED rather than trusting the unverified JWT role
// claim. A self-host pre-Better-Auth can opt back into the claim fallback by setting
// AllowJWTRoleFallback (ALLOW_JWT_ROLE_FALLBACK=true), which logs the degradation.
//
// On success it returns true; callers proceed with the privileged action.
func (a *API) requireAdmin(w http.ResponseWriter, r *http.Request, t store.Tenant) bool {
	// Role ceiling: an API-key-authenticated request is capped at member-level
	// permissions, regardless of the key creator's real role. Every admin-gated
	// action (org policy, member admin, key management) refuses keyed callers so a
	// leaked CI key can never escalate to org takeover — admin actions stay
	// interactive-login only.
	if _, keyed := middleware.APIKeyIDFromContext(r.Context()); keyed {
		httpx.WriteError(w, fmt.Errorf("%w: this action requires an interactive login; API keys are limited to member-level actions", httpx.ErrForbidden))
		return false
	}
	role, err := a.Store.MemberRole(r.Context(), t.OrgID, t.UserID)
	if err != nil {
		if errors.Is(err, store.ErrAuthSchemaUnavailable) {
			if !a.AllowJWTRoleFallback {
				// Strict default: can't confirm membership live → deny (don't trust
				// the JWT role claim). Self-host pre-Better-Auth opts in via config.
				logger(r).Warn("member table unavailable and JWT role fallback disabled; denying admin action",
					"org_id", t.OrgID, "user_id", t.UserID)
				httpx.WriteError(w, fmt.Errorf("%w: admin/owner role required (membership could not be verified)", httpx.ErrForbidden))
				return false
			}
			// Opt-in fallback: Better Auth not migrated here → trust the verified JWT
			// claim so the gate still functions, logging the degradation.
			claims, ok := middleware.ClaimsFromContext(r.Context())
			if ok && store.IsAdminRole(claims.Role) {
				logger(r).Warn("member table unavailable; authorizing admin from JWT claim (fallback enabled)",
					"org_id", t.OrgID, "user_id", t.UserID, "role", claims.Role)
				return true
			}
			httpx.WriteError(w, fmt.Errorf("%w: admin/owner role required", httpx.ErrForbidden))
			return false
		}
		if errors.Is(err, store.ErrNoMembership) {
			httpx.WriteError(w, fmt.Errorf("%w: not a member of this org", httpx.ErrForbidden))
			return false
		}
		writeStoreError(w, err)
		return false
	}
	if !store.IsAdminRole(role) {
		httpx.WriteError(w, fmt.Errorf("%w: admin/owner role required (you are %q)", httpx.ErrForbidden, role))
		return false
	}
	return true
}

// requireOrgMember confirms the caller is still a LIVE member of the active org
// (any role) per the identity.member table. It is the lightweight sibling of
// requireAdmin for actions allowed to a site's OWNER: owner identity alone is not
// proof of current membership, so an owner removed from the org must not keep
// managing their old site on a still-valid JWT. Same fail-closed posture as
// requireAdmin: a missing member table denies by default unless
// AllowJWTRoleFallback is set. Writes the 403 on failure.
func (a *API) requireOrgMember(w http.ResponseWriter, r *http.Request, t store.Tenant) bool {
	_, err := a.Store.MemberRole(r.Context(), t.OrgID, t.UserID)
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrAuthSchemaUnavailable) {
		if a.AllowJWTRoleFallback {
			logger(r).Warn("member table unavailable; allowing owner action from verified JWT (fallback enabled)",
				"org_id", t.OrgID, "user_id", t.UserID)
			return true
		}
		logger(r).Warn("member table unavailable and JWT role fallback disabled; denying owner action",
			"org_id", t.OrgID, "user_id", t.UserID)
		httpx.WriteError(w, fmt.Errorf("%w: org membership could not be verified", httpx.ErrForbidden))
		return false
	}
	if errors.Is(err, store.ErrNoMembership) {
		httpx.WriteError(w, fmt.Errorf("%w: not a member of this org", httpx.ErrForbidden))
		return false
	}
	writeStoreError(w, err)
	return false
}

// wrapUnauthorized yields an error httpx maps to 401, used for the defensive
// "claims somehow absent" branch.
func wrapUnauthorized() error { return errUnauthorized{} }

type errUnauthorized struct{}

func (errUnauthorized) Error() string { return "unauthorized" }

// Is bridges to httpx.ErrUnauthorized for the status mapping.
func (errUnauthorized) Is(target error) bool { return target == httpx.ErrUnauthorized }

// logger is a small helper to fetch the request-scoped (request_id-tagged) logger.
func logger(r *http.Request) *slog.Logger { return logx.FromContext(r.Context()) }
