// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/edgerevoke"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// ---------------------------------------------------------------------------
// POST /v1/members/{userId}/revoke   (admin/owner only)
// ---------------------------------------------------------------------------

// RevokeMember hard-revokes a user's edge tokens by writing revoked:user:<userId>
// with min_iat = now (revocation deny-list). Use on member
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
	// Confirm the target is a member of the active org before revoking (don't let an
	// admin of org A write a denylist key for a user belonging to org B).
	if !a.requireTargetOrgMember(w, r, t, targetUserID) {
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
// "sign-out-everywhere" affordance (revocation deny-list).
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
	if kind == edgerevoke.KindUser {
		// Confirm the target is a member of the active org before revoking (don't let
		// an admin of org A write a denylist key for a user belonging to org B).
		if !a.requireTargetOrgMember(w, r, t, id) {
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

// requireTargetOrgMember confirms targetUserID is a CURRENT member of the caller's
// active org (t.OrgID) per the live identity.member table, the same lookup that gates
// admin-only actions (MemberRole). A user-targeted revoke writes revoked:user:<id>,
// which invalidates that user's edge-token sessions; without this scope check an admin
// of org A could write a denylist key for a user belonging to org B (a cross-tenant
// DoS of share-link sessions). On a non-member target we return 404 (the user is not
// visible to this org) and write nothing.
//
// If the Better Auth identity.member table is unavailable, the behavior is STRICT BY
// DEFAULT (the same posture as requireAdmin): the revoke is DENIED rather than allowing
// an unscoped cross-org write. A self-host pre-Better-Auth can opt back in via
// AllowJWTRoleFallback, which logs the degradation.
//
// On success it returns true; callers proceed with the denylist write.
func (a *API) requireTargetOrgMember(w http.ResponseWriter, r *http.Request, t store.Tenant, targetUserID string) bool {
	_, err := a.Store.MemberRole(r.Context(), t.OrgID, targetUserID)
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrNoMembership) {
		// Not a member of the caller's org: refuse and write nothing. 404 (not 403) so
		// we don't confirm whether the id exists in some other org.
		httpx.WriteError(w, fmt.Errorf("%w: user is not a member of this org", httpx.ErrNotFound))
		return false
	}
	if errors.Is(err, store.ErrAuthSchemaUnavailable) {
		if a.AllowJWTRoleFallback {
			// Opt-in fallback: Better Auth not migrated here, so membership cannot be
			// confirmed. The caller is already a verified admin of this org; allow the
			// revoke, logging the degradation.
			logger(r).Warn("member table unavailable; allowing user-targeted revoke without membership check (fallback enabled)",
				"org_id", t.OrgID, "user_id", targetUserID)
			return true
		}
		// Strict default: can't confirm membership live, so refuse rather than write an
		// unscoped cross-org denylist key.
		logger(r).Warn("member table unavailable and JWT role fallback disabled; denying user-targeted revoke",
			"org_id", t.OrgID, "user_id", targetUserID)
		httpx.WriteError(w, fmt.Errorf("%w: user is not a member of this org (membership could not be verified)", httpx.ErrNotFound))
		return false
	}
	writeStoreError(w, err)
	return false
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
