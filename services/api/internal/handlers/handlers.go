// Package handlers implements the Go API's HTTP endpoints (the system of record,
// docs/ARCHITECTURE.md §3). Phase 1 ships the core publish/serve loop, DB-backed:
// create/list/get sites, the deployments prepare→finalize→publish flow, and the
// identity echo + health check. Every authenticated handler runs under the RLS
// tenant context (via the store's tx-per-call SET LOCAL) and quota is checked at
// the cost-creating action (the open-core seam).
package handlers

import (
	"context"
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
type API struct {
	Quota      quota.Provider
	Store      SiteStore
	Objects    ObjectStore
	Projection ProjectionWriter
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

// wrapUnauthorized yields an error httpx maps to 401, used for the defensive
// "claims somehow absent" branch.
func wrapUnauthorized() error { return errUnauthorized{} }

type errUnauthorized struct{}

func (errUnauthorized) Error() string { return "unauthorized" }

// Is bridges to httpx.ErrUnauthorized for the status mapping.
func (errUnauthorized) Is(target error) bool { return target == httpx.ErrUnauthorized }

// logger is a small helper to fetch the request-scoped (request_id-tagged) logger.
func logger(r *http.Request) *slog.Logger { return logx.FromContext(r.Context()) }
