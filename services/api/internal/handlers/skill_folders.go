// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// skillFolderResponse is the API representation of a skill folder.
type skillFolderResponse struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	ItemCount int64     `json:"item_count"`
	CreatedAt time.Time `json:"created_at"`
}

func toFolderResponse(f store.SkillFolder) skillFolderResponse {
	return skillFolderResponse{ID: f.ID, Slug: f.Slug, Title: f.Title, ItemCount: f.ItemCount, CreatedAt: f.CreatedAt}
}

// ListSkillFolders returns the org's folders (any member).
func (a *API) ListSkillFolders(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	a.ensureSkillsSeeded(r, t)

	folders, err := a.Store.ListSkillFolders(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]skillFolderResponse, len(folders))
	for i, f := range folders {
		out[i] = toFolderResponse(f)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"folders": out})
}

// createSkillFolderRequest is the POST /v1/skill-folders body.
type createSkillFolderRequest struct {
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
}

// CreateSkillFolder creates a folder (admin/owner only).
func (a *API) CreateSkillFolder(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireAdmin(w, r, t) {
		return
	}
	var req createSkillFolderRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.Slug == "" {
		httpx.WriteError(w, fmt.Errorf("%w: slug is required", httpx.ErrBadRequest))
		return
	}
	folder, err := a.Store.CreateSkillFolder(r.Context(), t, req.Slug, req.Title)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillFolderCreate, "skill_folder:"+folder.ID, map[string]any{
		"slug": folder.Slug, "title": folder.Title,
	})
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"folder": toFolderResponse(folder)})
}

// renameSkillFolderRequest is the PATCH /v1/skill-folders/{id} body.
type renameSkillFolderRequest struct {
	Title string `json:"title"`
}

// RenameSkillFolder retitles a folder (admin/owner only; the slug is stable).
func (a *API) RenameSkillFolder(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireAdmin(w, r, t) {
		return
	}
	var req renameSkillFolderRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.Title == "" {
		httpx.WriteError(w, fmt.Errorf("%w: title is required", httpx.ErrBadRequest))
		return
	}
	folder, err := a.Store.RenameSkillFolder(r.Context(), t, chi.URLParam(r, "id"), req.Title)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillFolderRename, "skill_folder:"+folder.ID, map[string]any{
		"slug": folder.Slug, "title": folder.Title,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"folder": toFolderResponse(folder)})
}

// DeleteSkillFolder removes a folder (admin/owner only). Memberships cascade;
// the skills themselves survive.
func (a *API) DeleteSkillFolder(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireAdmin(w, r, t) {
		return
	}
	folderID := chi.URLParam(r, "id")
	if err := a.Store.DeleteSkillFolder(r.Context(), t, folderID); err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillFolderDelete, "skill_folder:"+folderID, nil)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// addSkillFolderItemRequest is the POST /v1/skill-folders/{id}/items body.
type addSkillFolderItemRequest struct {
	SkillID  string `json:"skill_id"`
	IsPreset bool   `json:"is_preset,omitempty"`
}

// AddSkillFolderItem adds a skill to a folder. Admins may add any skill and
// set the preset flag; a skill's owner may add their own skill (never as a
// preset). The free-tier per-folder cap → 402.
func (a *API) AddSkillFolderItem(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	folderID := chi.URLParam(r, "id")

	var req addSkillFolderItemRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.SkillID == "" {
		httpx.WriteError(w, fmt.Errorf("%w: skill_id is required", httpx.ErrBadRequest))
		return
	}
	skill, err := a.Store.GetSkill(r.Context(), t, req.SkillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Owners may curate their own skill into folders but only an admin may mark
	// presets (the org-endorsed starter set).
	if req.IsPreset {
		if !a.requireAdmin(w, r, t) {
			return
		}
	} else if !a.requireSkillOwnerOrAdmin(w, r, t, skill) {
		return
	}

	if err := a.Store.AddSkillToFolder(r.Context(), t, folderID, req.SkillID, req.IsPreset); err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillFolderChange, "skill_folder:"+folderID, map[string]any{
		"skill_id": req.SkillID, "slug": skill.Slug, "added": true, "is_preset": req.IsPreset,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"added": true})
}

// RemoveSkillFolderItem removes a skill from a folder (admin or skill owner).
func (a *API) RemoveSkillFolderItem(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	folderID := chi.URLParam(r, "id")
	skillID := chi.URLParam(r, "skillID")

	skill, err := a.Store.GetSkill(r.Context(), t, skillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireSkillOwnerOrAdmin(w, r, t, skill) {
		return
	}
	if err := a.Store.RemoveSkillFromFolder(r.Context(), t, folderID, skillID); err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillFolderChange, "skill_folder:"+folderID, map[string]any{
		"skill_id": skillID, "slug": skill.Slug, "added": false,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// setSkillFolderItemPresetRequest is the PATCH /{id}/items/{skillID} body.
type setSkillFolderItemPresetRequest struct {
	IsPreset bool `json:"is_preset"`
}

// SetSkillFolderItemPreset flips a membership's preset flag (admin/owner only).
func (a *API) SetSkillFolderItemPreset(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireAdmin(w, r, t) {
		return
	}
	folderID := chi.URLParam(r, "id")
	skillID := chi.URLParam(r, "skillID")

	var req setSkillFolderItemPresetRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if err := a.Store.SetSkillFolderItemPreset(r.Context(), t, folderID, skillID, req.IsPreset); err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillFolderPresetChange, "skill_folder:"+folderID, map[string]any{
		"skill_id": skillID, "is_preset": req.IsPreset,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"is_preset": req.IsPreset})
}

// skillFolderDownloadResponse is the bulk folder download: every finalized
// skill in the folder, inline, under a total response budget. Skills past the
// budget are returned as truncated stubs the client fetches individually.
type skillFolderDownloadResponse struct {
	Folder   skillFolderResponse    `json:"folder"`
	Skills   []skillDownloadPayload `json:"skills"`
	Warnings []string               `json:"warnings,omitempty"`
}

// DownloadSkillFolder bulk-downloads every skill in a folder (any member) —
// the "install the whole preset" affordance.
func (a *API) DownloadSkillFolder(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	folderID := chi.URLParam(r, "id")

	folder, err := a.Store.GetSkillFolder(r.Context(), t, folderID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	skills, err := a.Store.ListFolderSkills(r.Context(), t, folderID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	resp := skillFolderDownloadResponse{Folder: toFolderResponse(folder), Skills: make([]skillDownloadPayload, 0, len(skills))}
	var budget int64 = skillFolderDownloadMaxBytes
	for _, skill := range skills {
		if budget <= 0 {
			resp.Skills = append(resp.Skills, skillDownloadPayload{Slug: skill.Slug, SkillID: skill.ID, Truncated: true})
			continue
		}
		payload, n, err := a.downloadSkillPayload(r, t, skill)
		if err != nil {
			// A single broken skill (e.g. missing manifest object) must not sink the
			// whole folder download; mark it truncated and continue.
			logger(r).Warn("folder download: skill skipped", "skill_id", skill.ID, "err", err)
			resp.Skills = append(resp.Skills, skillDownloadPayload{Slug: skill.Slug, SkillID: skill.ID, Truncated: true})
			continue
		}
		budget -= n
		resp.Skills = append(resp.Skills, payload)
	}
	for _, p := range resp.Skills {
		if p.Truncated {
			resp.Warnings = append(resp.Warnings,
				"some skills were not inlined (size budget or a read error); fetch them via GET /v1/skills/{id}/download")
			break
		}
	}

	a.recordAudit(r, t, audit.ActionSkillDownload, "skill_folder:"+folderID, map[string]any{
		"folder_slug": folder.Slug, "skills": len(resp.Skills),
	})
	httpx.WriteJSON(w, http.StatusOK, resp)
}
