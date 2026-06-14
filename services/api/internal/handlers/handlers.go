// Package handlers implements the Go API's HTTP endpoints (the system of record,
// docs/ARCHITECTURE.md §3). Phase 1 ships the health check, an identity echo,
// and a stub site-create that exercises the quota seam end to end.
package handlers

import (
	"net/http"
	"time"

	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/internal/quota"
)

// API holds the handler dependencies wired in main.go. The quota.Provider is the
// open-core seam: OSS gets quota.Unlimited{}, the cloud build injects the real
// hard-cap provider.
type API struct {
	Quota quota.Provider
}

// New constructs an API with its dependencies. A nil provider defaults to
// Unlimited so a misconfigured wiring fails open to the OSS behavior rather than
// panicking.
func New(q quota.Provider) *API {
	if q == nil {
		q = quota.Unlimited{}
	}
	return &API{Quota: q}
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

// Me returns the caller's verified identity. Auth middleware guarantees claims
// are present; the defensive check keeps this safe if it's ever mounted bare.
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

// siteResponse is the Phase-1 stub site. The real create path writes an immutable
// site row under RLS (§5) and returns the live URL; here we just prove the quota
// gate + 201 envelope.
type siteResponse struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	OwnerID   string    `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateSite reserves quota for one more site for the caller and returns a stub
// site. On a quota cap it surfaces quota.ExceededError, which httpx renders as
// 402 with the upgrade payload (§9).
func (a *API) CreateSite(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}

	// Synchronous hard-cap check at the cost-creating action (§9). OSS:
	// Unlimited → always nil. Cloud: real per-user site cap → may 402.
	if err := a.Quota.CheckAndReserve(
		r.Context(), claims.OrgID, claims.UserID(), quota.ResourceSitePerUser,
	); err != nil {
		httpx.WriteError(w, err)
		return
	}

	// Phase-1 stub: a real implementation inserts under SET LOCAL tenant context
	// and returns the resolved live URL.
	httpx.WriteJSON(w, http.StatusCreated, siteResponse{
		ID:        "site_stub",
		OrgID:     claims.OrgID,
		OwnerID:   claims.UserID(),
		CreatedAt: time.Now().UTC(),
	})
}

// wrapUnauthorized yields an error httpx maps to 401, used for the defensive
// "claims somehow absent" branch.
func wrapUnauthorized() error { return errUnauthorized{} }

type errUnauthorized struct{}

func (errUnauthorized) Error() string { return "unauthorized" }

// Is bridges to httpx.ErrUnauthorized for the status mapping.
func (errUnauthorized) Is(target error) bool { return target == httpx.ErrUnauthorized }
