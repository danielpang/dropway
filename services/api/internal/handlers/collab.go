// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Collaboration toggle ("allow non-creators to modify", migration 0014).
// Dropway is collaborative BY DEFAULT: any live org member may modify any
// site/skill/chat log's CONTENT. The creator (or an admin) can flip a
// resource's toggle off to restrict content edits to creator-or-admin — e.g.
// when they don't know the other people in the org. Security-sensitive
// changes (access mode, allowlist, revocation, domains) stay admin-gated and
// deletion stays creator-or-admin regardless of the toggle.

// requireSiteEditor gates a site CONTENT mutation (deploy/publish/previews):
// toggle on or caller is the creator → any live org member; toggle off →
// admin/owner only. Writes the 403 on failure.
func (a *API) requireSiteEditor(w http.ResponseWriter, r *http.Request, t store.Tenant, site store.Site) bool {
	if site.AllowMemberEdits || site.OwnerUserID == t.UserID {
		return a.requireOrgMember(w, r, t)
	}
	return a.requireAdmin(w, r, t)
}

// requireSkillEditor is requireSiteEditor for skill content (uploads,
// metadata, folder memberships).
func (a *API) requireSkillEditor(w http.ResponseWriter, r *http.Request, t store.Tenant, skill store.Skill) bool {
	if skill.AllowMemberEdits || skill.OwnerUserID == t.UserID {
		return a.requireOrgMember(w, r, t)
	}
	return a.requireAdmin(w, r, t)
}

// requireChatLogEditor is requireSiteEditor for chat-log content (appends,
// message curation, site binding, panel flag).
func (a *API) requireChatLogEditor(w http.ResponseWriter, r *http.Request, t store.Tenant, l store.ChatLog) bool {
	if l.AllowMemberEdits || l.CreatedBy == t.UserID {
		return a.requireOrgMember(w, r, t)
	}
	return a.requireAdmin(w, r, t)
}

// collabRequest is the shared PUT body for all three toggles.
type collabRequest struct {
	AllowMemberEdits bool `json:"allow_member_edits"`
}

// SetSiteCollab flips a site's collaboration toggle (creator-or-admin).
// PUT /v1/sites/{id}/collab.
func (a *API) SetSiteCollab(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")
	var req collabRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	site, err := a.Store.GetSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Flipping WHO may edit is itself creator-or-admin (never toggle-gated,
	// or a collaborator could lock the creator out of their own site).
	if !a.requireSiteOwnerOrAdmin(w, r, t, site) {
		return
	}
	site, err = a.Store.SetSiteAllowMemberEdits(r.Context(), t, siteID, req.AllowMemberEdits)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSiteCollab, "site:"+siteID, map[string]any{
		"allow_member_edits": req.AllowMemberEdits,
	})
	orgSlug, err := a.Store.OrgSlug(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	bytes, _ := a.Store.SiteStorageBytes(r.Context(), t, siteID)
	vanityBySite, err := a.Store.VanityHostsForOrg(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a.toSiteResponse(site, orgSlug, bytes, vanityBySite[siteID]))
}

// requireSiteOwnerOrAdmin gates a site meta-mutation to its creator (who must
// still be a live org member) or an org admin/owner.
func (a *API) requireSiteOwnerOrAdmin(w http.ResponseWriter, r *http.Request, t store.Tenant, site store.Site) bool {
	if site.OwnerUserID == t.UserID {
		return a.requireOrgMember(w, r, t)
	}
	return a.requireAdmin(w, r, t)
}

// SetSkillCollab flips a skill's collaboration toggle (creator-or-admin).
// PUT /v1/skills/{id}/collab.
func (a *API) SetSkillCollab(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	skillID := chi.URLParam(r, "id")
	var req collabRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	skill, err := a.Store.GetSkill(r.Context(), t, skillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireSkillOwnerOrAdmin(w, r, t, skill) {
		return
	}
	skill, err = a.Store.SetSkillAllowMemberEdits(r.Context(), t, skillID, req.AllowMemberEdits)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillCollab, "skill:"+skillID, map[string]any{
		"allow_member_edits": req.AllowMemberEdits,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"skill": toSkillResponse(skill)})
}

// SetChatLogCollab flips a chat log's collaboration toggle (creator-or-admin).
// PUT /v1/chats/{id}/collab.
func (a *API) SetChatLogCollab(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	var req collabRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	log, err := a.Store.GetChatLog(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireChatLogOwnerOrAdmin(w, r, t, log) {
		return
	}
	log, err = a.Store.SetChatLogAllowMemberEdits(r.Context(), t, id, req.AllowMemberEdits)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionChatLogCollab, "chatlog:"+id, map[string]any{
		"allow_member_edits": req.AllowMemberEdits,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chat_log": toChatLogResponse(log)})
}
