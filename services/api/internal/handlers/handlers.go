// Package handlers implements the Go API's HTTP endpoints (the system of record,
// docs/ARCHITECTURE.md §3). Phase 1 ships the core publish/serve loop, DB-backed:
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
	"time"

	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/internal/logx"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// presignTTL bounds how long a direct-to-store upload URL is valid.
const presignTTL = 15 * time.Minute

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

	// Revoker writes the hard-revocation denylist (revoked:user/site/org) the
	// serving Worker + /authz read. Optional: when nil, hard revocation degrades to
	// the short edge-token TTL only (the routes are still rewritten). In production
	// it is the same Cloudflare KV writer as Projection (ARCHITECTURE.md §6/§10).
	Revoker EdgeRevoker

	// AllowJWTRoleFallback gates the requireAdmin fallback to the verified JWT role
	// claim when the Better Auth auth.member table is unavailable. Default false
	// (strict): admin-gated actions are DENIED when membership can't be confirmed
	// live. A self-host pre-Better-Auth can opt in (ALLOW_JWT_ROLE_FALLBACK=true).
	// See config.Config.AllowJWTRoleFallback / ARCHITECTURE.md §10 [LOW].
	AllowJWTRoleFallback bool
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
// token authenticates as (verification proof #3 in §13).
type meResponse struct {
	UserID string `json:"user_id"`
	OrgID  string `json:"org_id"`
	Role   string `json:"role"`
}

// Me returns the caller's verified identity.
func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, meResponse{
		UserID: claims.UserID(),
		OrgID:  claims.OrgID,
		Role:   claims.Role,
	})
}

// tenant derives the RLS tenant from verified claims. Callers that reach here are
// behind the Auth middleware, so claims are present; the bool guards the rare
// "mounted bare" path.
func tenant(ctx context.Context) (store.Tenant, bool) {
	c, ok := middleware.ClaimsFromContext(ctx)
	if !ok {
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
// access-policy / org-policy / role mutations (ARCHITECTURE.md §5.4/§10 [HIGH]).
// It writes a 403 and returns false on a non-admin, an empty membership, or any
// re-check error.
//
// If the Better Auth auth.member table is unavailable (a self-host that hasn't run
// Better Auth yet), the behavior is STRICT BY DEFAULT (ARCHITECTURE.md §10 [LOW]):
// admin-gated actions are DENIED rather than trusting the unverified JWT role
// claim. A self-host pre-Better-Auth can opt back into the claim fallback by setting
// AllowJWTRoleFallback (ALLOW_JWT_ROLE_FALLBACK=true), which logs the degradation.
//
// On success it returns true; callers proceed with the privileged action.
func (a *API) requireAdmin(w http.ResponseWriter, r *http.Request, t store.Tenant) bool {
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

// wrapUnauthorized yields an error httpx maps to 401, used for the defensive
// "claims somehow absent" branch.
func wrapUnauthorized() error { return errUnauthorized{} }

type errUnauthorized struct{}

func (errUnauthorized) Error() string { return "unauthorized" }

// Is bridges to httpx.ErrUnauthorized for the status mapping.
func (errUnauthorized) Is(target error) bool { return target == httpx.ErrUnauthorized }

// logger is a small helper to fetch the request-scoped (request_id-tagged) logger.
func logger(r *http.Request) *slog.Logger { return logx.FromContext(r.Context()) }
