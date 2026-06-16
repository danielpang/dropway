// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/edgerevoke"
	"github.com/danielpang/dropway/internal/httpx"
)

// ---------------------------------------------------------------------------
// POST /v1/members/{userId}/revoke   (admin/owner only)
// ---------------------------------------------------------------------------

// RevokeMember hard-revokes a user's edge tokens by writing revoked:user:<userId>
// with min_iat = now (ARCHITECTURE.md §6/§10 revocation deny-list). Use on member
// removal / ban: every edge token the user holds, issued before now, is rejected by
// the Worker + /authz immediately (not just at the short TTL). ADMIN/OWNER only.
//
// This is the API-surfaced revoke for member removal: Better Auth owns the actual
// `member` row delete, but the Go API owns the edge denylist, so the dashboard
// calls this alongside removing the member to make revocation immediate.
func (a *API) RevokeMember(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	if !a.requireRevoker(w) {
		return
	}
	targetUserID := chi.URLParam(r, "userId")
	if !looksLikeID(targetUserID) {
		httpx.WriteError(w, badRequest("a valid userId is required"))
		return
	}

	minIAT := time.Now().Unix()
	if err := a.Revoker.Revoke(r.Context(), edgerevoke.KindUser, targetUserID, minIAT); err != nil {
		logger(r).Error("revoke member denylist write failed", "user_id", targetUserID, "org_id", t.OrgID, "err", err)
		httpx.WriteError(w, err)
		return
	}

	logger(r).Info("member edge tokens revoked", "user_id", targetUserID, "org_id", t.OrgID, "min_iat", minIAT)
	a.recordAudit(r, t, audit.ActionMemberRevoke, "member:"+targetUserID, map[string]any{
		"min_iat": minIAT,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"revoked": "user:" + targetUserID,
		"min_iat": minIAT,
	})
}

// ---------------------------------------------------------------------------
// POST /v1/sites/{id}/revoke-access   (admin/owner only)
// ---------------------------------------------------------------------------

// RevokeSiteAccess hard-revokes all edge tokens for a site by writing
// revoked:site:<id> with min_iat = now — a generic admin "kill the share now"
// affordance independent of an access-mode change (e.g. an accidental share, an
// incident). ADMIN/OWNER only. The site must belong to the active org (confused-
// deputy guard via GetSite under RLS).
func (a *API) RevokeSiteAccess(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	if !a.requireRevoker(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	// Confirm the site belongs to the active org before revoking (don't let an admin
	// of org A write a denylist key for org B's site id).
	if _, err := a.Store.GetSite(r.Context(), t, siteID); err != nil {
		writeStoreError(w, err)
		return
	}

	minIAT := time.Now().Unix()
	if err := a.Revoker.Revoke(r.Context(), edgerevoke.KindSite, siteID, minIAT); err != nil {
		logger(r).Error("revoke site-access denylist write failed", "site_id", siteID, "org_id", t.OrgID, "err", err)
		httpx.WriteError(w, err)
		return
	}

	logger(r).Info("site edge tokens revoked", "site_id", siteID, "org_id", t.OrgID, "min_iat", minIAT)
	a.recordAudit(r, t, audit.ActionSiteRevokeAccess, "site:"+siteID, map[string]any{
		"min_iat": minIAT,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"revoked": "site:" + siteID,
		"min_iat": minIAT,
	})
}

// ---------------------------------------------------------------------------
// POST /v1/orgs/revoke-access   {kind: "user"|"site"|"org", id}   (admin/owner only)
// ---------------------------------------------------------------------------

type revokeAccessRequest struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// RevokeAccess is the GENERIC hard-revoke entry point the dashboard calls: it bumps
// the denylist min_iat for one subject (kind=user|site|org), the unified
// "sign-out-everywhere" affordance (ARCHITECTURE.md §6/§10 revocation deny-list).
// ADMIN/OWNER only. For kind=site it re-checks the site belongs to the active org;
// kind=user/org write the subject denylist directly (the org id is the caller's
// own active org for kind=org — an admin can only org-kill THEIR org).
//
// It complements the RESTful POST /v1/members/{userId}/revoke and
// POST /v1/sites/{id}/revoke-access (which dispatch the same denylist writes).
func (a *API) RevokeAccess(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	if !a.requireRevoker(w) {
		return
	}

	var req revokeAccessRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, badRequest(err.Error()))
		return
	}

	var (
		kind      edgerevoke.Kind
		id        = req.ID
		auditAct  audit.Action
		auditTgt  string
		mustOwnID bool // re-check the id belongs to the active org (kind=site)
	)
	switch req.Kind {
	case string(edgerevoke.KindUser):
		kind, auditAct, auditTgt = edgerevoke.KindUser, audit.ActionMemberRevoke, "member:"+id
	case string(edgerevoke.KindSite):
		kind, auditAct, auditTgt, mustOwnID = edgerevoke.KindSite, audit.ActionSiteRevokeAccess, "site:"+id, true
	case string(edgerevoke.KindOrg):
		// An admin may only org-kill THEIR OWN org; ignore any client id and use the
		// active org (no cross-org kill switch).
		kind, id = edgerevoke.KindOrg, t.OrgID
		auditAct, auditTgt = audit.ActionSiteRevokeAccess, "org:"+t.OrgID
	default:
		httpx.WriteError(w, badRequest("kind must be one of user, site, org"))
		return
	}
	if !looksLikeID(id) {
		httpx.WriteError(w, badRequest("a valid id is required"))
		return
	}
	if mustOwnID {
		if _, err := a.Store.GetSite(r.Context(), t, id); err != nil {
			writeStoreError(w, err)
			return
		}
	}

	minIAT := time.Now().Unix()
	if err := a.Revoker.Revoke(r.Context(), kind, id, minIAT); err != nil {
		logger(r).Error("revoke-access denylist write failed", "kind", req.Kind, "id", id, "org_id", t.OrgID, "err", err)
		httpx.WriteError(w, err)
		return
	}
	logger(r).Info("access revoked", "kind", string(kind), "id", id, "org_id", t.OrgID, "min_iat", minIAT)
	a.recordAudit(r, t, auditAct, auditTgt, map[string]any{"kind": string(kind), "min_iat": minIAT})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":    string(kind),
		"id":      id,
		"min_iat": minIAT,
	})
}

// requireRevoker returns the edge revoker or writes a 503. Hard revocation needs the
// KV denylist writer; without it (a DB-less/dev deployment) we can't make revocation
// immediate, so we surface that rather than silently no-op a security action.
func (a *API) requireRevoker(w http.ResponseWriter) bool {
	if a.Revoker == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "edge revocation not configured"})
		return false
	}
	return true
}

// looksLikeID is a minimal sanity check for a path id (non-empty, bounded, no
// slashes/spaces). The denylist key is opaque, so we only guard against obviously
// malformed input; the value need not be a member that exists (revoking a non-member
// is a harmless no-op at the edge).
func looksLikeID(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	for _, c := range s {
		if c == '/' || c == ' ' || c == '\t' || c == '\n' {
			return false
		}
	}
	return true
}

// badRequest wraps a message as an httpx 400.
func badRequest(msg string) error {
	return fmt.Errorf("%w: %s", httpx.ErrBadRequest, msg)
}
