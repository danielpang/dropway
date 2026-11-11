// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"net/http"
	"strings"

	"github.com/danielpang/dropway/internal/httpx"
)

// CollaborationRollout guards only the collaboration surface. Existing site
// publish/rollback authorization and organization consent remain independent.
func (a *API) CollaborationRollout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimSuffix(r.URL.Path, "/")
		// Settings PATCH authorizes its decoded value in the handler so admins
		// can withdraw organization consent even when rollout is unavailable.
		gated := (p == "/v1/orgs/project-rooms" && r.Method != http.MethodPatch) || p == "/v1/project-rooms" || strings.HasPrefix(p, "/v1/project-rooms/") || p == "/v1/provider-credentials" || strings.HasPrefix(p, "/v1/provider-credentials/") || (strings.HasPrefix(p, "/v1/sites/") && strings.HasSuffix(p, "/comment-pages"))
		if gated {
			t, ok := tenant(r.Context())
			if !ok || a.CollaborationAllowed == nil || !a.CollaborationAllowed(r.Context(), t.UserID, t.OrgID) {
				httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorBody{Error: "collaboration_disabled", Message: "project collaboration is not enabled"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) GetCollaborationCapabilities(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireOrgMember(w, r, t) {
		return
	}
	enabled := a.CollaborationAllowed != nil && a.CollaborationAllowed(r.Context(), t.UserID, t.OrgID)
	configured := a.Hosted != nil && a.Hosted.Configured && a.Hosted.Vault != nil && a.Hosted.Store != nil
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"enabled": enabled, "execution_configured": configured})
}
