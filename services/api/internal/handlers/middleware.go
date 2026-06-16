// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// EnsureOrgProvisioned is middleware (mounted AFTER Auth) that idempotently
// creates the app-side org rows (org_meta + org_usage) for the verified tenant
// before the request reaches a handler. A solo user / first request thus always
// has the anchor row its business data attaches to (ARCHITECTURE.md §5: app data
// attaches to the org via org_meta keyed by the Better Auth organization.id).
//
// It is a no-op when the rows already exist. With no Store configured it passes
// through (the DB-backed handlers themselves return 503).
func (a *API) EnsureOrgProvisioned(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Store == nil {
			next.ServeHTTP(w, r)
			return
		}
		claims, ok := middleware.ClaimsFromContext(r.Context())
		if !ok || claims.OrgID == "" {
			// No verified tenant → nothing to provision; let the handler's own
			// auth/tenant check produce the right 401.
			next.ServeHTTP(w, r)
			return
		}
		t := store.Tenant{OrgID: claims.OrgID, UserID: claims.UserID()}
		if err := a.Store.EnsureOrgProvisioned(r.Context(), t); err != nil {
			logger(r).Error("ensure-org-provisioned failed", "org_id", t.OrgID, "err", err)
			httpx.WriteError(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
