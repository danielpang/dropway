// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// ---------------------------------------------------------------------------
// POST /v1/members/invites   (admin/owner only)
// ---------------------------------------------------------------------------

type recordInviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// RecordMemberInvite records an audit entry for an org invitation the dashboard
// just created via Better Auth (which owns the invitation row + delivery email).
// The Go API is the audit system of record, so the dashboard calls this AFTER a
// successful invite to capture who invited whom, with what role — scoped by RLS to
// the inviter's active org. ADMIN/OWNER only (re-checked live), matching the gate
// Better Auth enforces on inviting.
//
// Best-effort from the caller's side: the invitation already exists and is
// authoritative, so a failure here only loses the trail row, never the invite.
func (a *API) RecordMemberInvite(w http.ResponseWriter, r *http.Request) {
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

	var req recordInviteRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, badRequest(err.Error()))
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" || !strings.Contains(email, "@") || len(email) > 320 {
		httpx.WriteError(w, badRequest("a valid email is required"))
		return
	}
	role := normalizeInviteRole(req.Role)

	a.recordAudit(r, t, audit.ActionMemberInvite, "invite:"+email, map[string]any{
		"email": email,
		"role":  role,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

// ---------------------------------------------------------------------------
// POST /v1/members/mfa-reset   (admin/owner only)
// ---------------------------------------------------------------------------

type recordMfaResetRequest struct {
	UserID string `json:"user_id"`
}

// RecordMfaReset records an audit entry for an owner/admin clearing a member's
// two-factor enrollment (the lockout recovery path). The reset itself is
// performed by the dashboard against the Better Auth identity schema (which the
// Go API never writes); this captures WHO reset WHOM in the org's audit trail.
// ADMIN/OWNER only (re-checked live), matching the gate the dashboard action
// enforces on the reset. Best-effort from the caller's side, like the invite
// trail: a failure here only loses the trail row, never the reset.
func (a *API) RecordMfaReset(w http.ResponseWriter, r *http.Request) {
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

	var req recordMfaResetRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, badRequest(err.Error()))
		return
	}
	userID := strings.TrimSpace(req.UserID)
	if userID == "" || len(userID) > 64 {
		httpx.WriteError(w, badRequest("a target user_id is required"))
		return
	}

	a.recordAudit(r, t, audit.ActionMfaReset, "member:"+userID, map[string]any{
		"user_id": userID,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

// ---------------------------------------------------------------------------
// POST /v1/members/joined   (any member — records their OWN join)
// ---------------------------------------------------------------------------

// RecordMemberJoin records an audit entry for the caller accepting an invitation
// and joining their (now-active) org. Better Auth owns the membership row; the
// dashboard calls this after the join (once the joined org is the active org, so
// the RLS tenant + JWT org_id both point at it) to capture the new member.
//
// NOT admin-gated: the new member is a plain member recording their OWN join. The
// target is taken from the verified caller id (claims), never the request body, so
// a caller can only ever record themselves. We confirm live membership (MemberRole)
// so a non-member can't write a spurious join row for an org; on a self-host without
// the Better Auth schema we record without a role rather than fail (the join is
// already authoritative).
func (a *API) RecordMemberJoin(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if t.UserID == "" {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}

	role, err := a.Store.MemberRole(r.Context(), t.OrgID, t.UserID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNoMembership):
			// Not a member of the active org → nothing legitimate to record.
			httpx.WriteError(w, badRequest("caller is not a member of the active org"))
			return
		case errors.Is(err, store.ErrAuthSchemaUnavailable):
			// Self-host pre-Better-Auth: record without a role rather than fail.
			role = ""
		default:
			writeStoreError(w, err)
			return
		}
	}

	meta := map[string]any{}
	if role != "" {
		meta["role"] = role
	}
	a.recordAudit(r, t, audit.ActionMemberJoin, "member:"+t.UserID, meta)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

// normalizeInviteRole clamps a client-supplied invite role to the known set,
// defaulting an empty/unknown value to "member" (the same roles the invite form
// offers). It is metadata only — the authoritative role lives on the Better Auth
// member row — so an odd value is coerced, not rejected.
func normalizeInviteRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case store.RoleOwner:
		return store.RoleOwner
	case store.RoleAdmin:
		return store.RoleAdmin
	default:
		return store.RoleMember
	}
}
