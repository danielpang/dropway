// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/skillseeds"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// skillFolderRefResponse is one folder membership on a skill row.
type skillFolderRefResponse struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	IsPreset bool   `json:"is_preset"`
}

// skillResponse is the API representation of a skill.
type skillResponse struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
	Slug  string `json:"slug"`
	// OwnerID is the uploader; store.SeedOwnerUserID marks a Dropway-seeded
	// preset (render as "Dropway").
	OwnerID string `json:"owner_id"`
	// IsSeeded is true when the skill is a Dropway-provided preset (owner is the
	// seed sentinel). Clients render the owner as "Dropway" from this flag rather
	// than string-matching the sentinel UUID themselves.
	IsSeeded         bool    `json:"is_seeded"`
	Title            string  `json:"title"`
	Description      string  `json:"description"`
	CurrentVersionID *string `json:"current_version_id,omitempty"`
	SizeBytes        int64   `json:"size_bytes"`
	// Version is the current version's monotonic number (0 before the first
	// upload). It bumps on every content change; CLI/MCP compare it against a
	// downloaded skill's recorded version to detect updates.
	Version int32 `json:"version"`
	// FeedVisible shares the skill to the org feed (default true); the owner/admin
	// can make it private to pull it off the feed.
	FeedVisible bool `json:"feed_visible"`
	// AllowMemberEdits is the collaboration toggle (default true): false
	// restricts content edits (uploads/metadata/folders) to creator-or-admin.
	AllowMemberEdits bool                     `json:"allow_member_edits"`
	Folders          []skillFolderRefResponse `json:"folders"`
	CreatedAt        time.Time                `json:"created_at"`
}

func toSkillResponse(s store.Skill) skillResponse {
	folders := make([]skillFolderRefResponse, len(s.Folders))
	for i, f := range s.Folders {
		folders[i] = skillFolderRefResponse{ID: f.FolderID, Slug: f.Slug, Title: f.Title, IsPreset: f.IsPreset}
	}
	return skillResponse{
		ID:               s.ID,
		OrgID:            s.OrgID,
		Slug:             s.Slug,
		OwnerID:          s.OwnerUserID,
		IsSeeded:         s.OwnerUserID == store.SeedOwnerUserID,
		Title:            s.Title,
		Description:      s.Description,
		CurrentVersionID: s.CurrentVersionID,
		SizeBytes:        s.SizeBytes,
		Version:          s.Version,
		FeedVisible:      s.FeedVisible,
		AllowMemberEdits: s.AllowMemberEdits,
		Folders:          folders,
		CreatedAt:        s.CreatedAt,
	}
}

// requireSkillOwnerOrAdmin gates a skill mutation to its owner (who must still
// be a live org member) or an org admin/owner. Writes the 403 on failure.
func (a *API) requireSkillOwnerOrAdmin(w http.ResponseWriter, r *http.Request, t store.Tenant, skill store.Skill) bool {
	if skill.OwnerUserID == t.UserID {
		return a.requireOrgMember(w, r, t)
	}
	return a.requireAdmin(w, r, t)
}

// ensureSkillsSeeded lazily materializes the default folders + preset skills
// for the org on its first skills touch. Best-effort: a failure is logged and
// the request proceeds (skills_seeded stays false, so the next touch retries).
//
// Ordering is GC-safe: it STAGES the rows (folders/skills/versions/memberships,
// no live pointers), writes every version's manifest to object storage, and
// only THEN publishes (flips current_version_id + skills_seeded). A crash at any
// point leaves skills_seeded false so a later touch re-runs — and because a
// skill only becomes current after its manifest is durably written, the GC can
// never see a live preset version whose blobs look unreferenced.
//
// A process-local seededOrgs set short-circuits the per-request DB round-trip
// once this process has observed the org seeded (the flag is monotonic).
func (a *API) ensureSkillsSeeded(r *http.Request, t store.Tenant) {
	if a.Store == nil || a.Objects == nil || len(a.SkillSeeds) == 0 {
		return
	}
	if _, ok := a.seededOrgs.Load(t.OrgID); ok {
		return
	}
	ctx := r.Context()
	done, err := a.Store.SkillsSeeded(ctx, t)
	if err != nil {
		logger(r).Error("skills seed check failed", "org_id", t.OrgID, "err", err)
		return
	}
	if done {
		a.seededOrgs.Store(t.OrgID, struct{}{})
		return
	}

	// Stage seed blobs. Always PutBlob (content is tiny) so a retry refreshes
	// each blob's last-modified time, keeping the GC age guard on our side while
	// the version is staged-but-not-yet-current.
	seeds := make([]store.SkillSeed, 0, len(a.SkillSeeds))
	for _, seed := range a.SkillSeeds {
		blobs := make([]store.BlobSize, 0, len(seed.Files))
		for _, f := range seed.Files {
			if err := a.Objects.PutBlob(ctx, t.OrgID, f.SHA256, bytes.NewReader(f.Content), f.Size, f.ContentType); err != nil {
				logger(r).Error("skills seed blob stage failed", "org_id", t.OrgID, "sha", f.SHA256, "err", err)
				return
			}
			blobs = append(blobs, store.BlobSize{SHA: f.SHA256, Size: f.Size})
		}
		seeds = append(seeds, store.SkillSeed{
			Slug:        seed.Slug,
			Title:       seed.Title,
			Description: seed.Description,
			FolderSlug:  seed.FolderSlug,
			ContentHash: seed.Digest,
			SizeBytes:   seed.TotalSize,
			Blobs:       blobs,
		})
	}

	created, staged, err := a.Store.SeedOrgSkillsStage(ctx, t, seeds)
	if err != nil {
		logger(r).Error("skills seed stage failed", "org_id", t.OrgID, "err", err)
		return
	}
	if !staged {
		a.seededOrgs.Store(t.OrgID, struct{}{})
		return
	}

	// Write every staged version's manifest BEFORE publishing. If any write
	// fails we bail without publishing, so the next touch re-stages and retries
	// rather than leaving a live version with no manifest.
	bySlug := make(map[string]skillseeds.Seed, len(a.SkillSeeds))
	for _, seed := range a.SkillSeeds {
		bySlug[seed.Slug] = seed
	}
	for _, c := range created {
		seed, ok := bySlug[c.Slug]
		if !ok {
			continue
		}
		files := make(map[string]manifestTarget, len(seed.Files))
		for _, f := range seed.Files {
			files[f.Path] = manifestTarget{SHA256: f.SHA256, ContentType: f.ContentType, Size: f.Size}
		}
		body, err := json.Marshal(storedManifest{SchemaVersion: manifest.SchemaVersion, Files: files})
		if err != nil {
			logger(r).Error("skills seed manifest marshal failed", "slug", c.Slug, "err", err)
			return
		}
		if err := a.Objects.PutSkillManifest(ctx, t.OrgID, c.SkillID, c.VersionID, body); err != nil {
			logger(r).Error("skills seed manifest write failed", "slug", c.Slug, "err", err)
			return
		}
	}

	if err := a.Store.SeedOrgSkillsPublish(ctx, t, created); err != nil {
		logger(r).Error("skills seed publish failed", "org_id", t.OrgID, "err", err)
		return
	}
	a.seededOrgs.Store(t.OrgID, struct{}{})
	logger(r).Info("seeded default skills", "org_id", t.OrgID, "skills", len(created))
}

// createSkillRequest is the POST /v1/skills body. folders are folder IDs the
// skill should join immediately (optional).
type createSkillRequest struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title,omitempty"`
	Folders []string `json:"folders,omitempty"`
}

// CreateSkill registers a skill (metadata only — content arrives via the
// uploads prepare/finalize flow). Duplicate slug → 400; free-tier folder cap →
// 402.
func (a *API) CreateSkill(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	a.ensureSkillsSeeded(r, t)

	var req createSkillRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.Slug == "" {
		httpx.WriteError(w, fmt.Errorf("%w: slug is required", httpx.ErrBadRequest))
		return
	}
	if !store.ValidSlug(req.Slug) {
		httpx.WriteError(w, fmt.Errorf("%w: slug must be 1-63 chars, lowercase letters/digits/hyphens, no leading/trailing or doubled hyphens", httpx.ErrBadRequest))
		return
	}

	skill, err := a.Store.CreateSkill(r.Context(), t, req.Slug, req.Title, req.Folders)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("skill created", "skill_id", skill.ID, "slug", skill.Slug, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSkillCreate, "skill:"+skill.ID, map[string]any{
		"slug":    skill.Slug,
		"folders": folderSlugs(skill),
	})
	if a.Analytics != nil {
		a.Analytics.Capture(r.Context(), analytics.Event{
			DistinctID: t.UserID,
			Event:      "skill_created",
			Properties: map[string]any{"org_id": t.OrgID, "skill_id": skill.ID, "slug": skill.Slug},
			Groups:     map[string]string{"organization": t.OrgID},
		})
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"skill": toSkillResponse(skill)})
}

// ListSkills lists/searches the org's skills: ?q= text filter, ?folder= a
// folder slug, ?presets=true only preset-flagged members.
func (a *API) ListSkills(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	a.ensureSkillsSeeded(r, t)

	qp := r.URL.Query()
	skills, err := a.Store.ListSkills(r.Context(), t, qp.Get("q"), qp.Get("folder"), qp.Get("presets") == "true")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]skillResponse, len(skills))
	for i, s := range skills {
		out[i] = toSkillResponse(s)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// GetSkill returns one skill by id (404 if absent or another tenant's).
func (a *API) GetSkill(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	skill, err := a.Store.GetSkill(r.Context(), t, chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"skill": toSkillResponse(skill)})
}

// DeleteSkill removes a skill (owner or org admin).
func (a *API) DeleteSkill(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	skillID := chi.URLParam(r, "id")
	skill, err := a.Store.GetSkill(r.Context(), t, skillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireSkillOwnerOrAdmin(w, r, t, skill) {
		return
	}
	if err := a.Store.DeleteSkill(r.Context(), t, skillID); err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("skill deleted", "skill_id", skillID, "slug", skill.Slug, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSkillDelete, "skill:"+skillID, map[string]any{"slug": skill.Slug})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// setSkillFoldersRequest is the PUT /v1/skills/{id}/folders body: the complete
// replacement set of folder IDs.
type setSkillFoldersRequest struct {
	Folders []string `json:"folders"`
}

// SetSkillFolders replaces a skill's folder memberships (owner or admin).
// Preset flags on kept folders survive; the free-tier per-folder cap applies
// to newly-gained memberships (402).
func (a *API) SetSkillFolders(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	skillID := chi.URLParam(r, "id")
	skill, err := a.Store.GetSkill(r.Context(), t, skillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireSkillEditor(w, r, t, skill) {
		return
	}

	var req setSkillFoldersRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	updated, err := a.Store.SetSkillFolders(r.Context(), t, skillID, req.Folders)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillFolderChange, "skill:"+skillID, map[string]any{
		"slug":    updated.Slug,
		"folders": folderSlugs(updated),
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"skill": toSkillResponse(updated)})
}

func folderSlugs(s store.Skill) []string {
	out := make([]string, len(s.Folders))
	for i, f := range s.Folders {
		out[i] = f.Slug
	}
	return out
}
