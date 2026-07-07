// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/skillspec"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// skillFolderDownloadMaxBytes bounds one bulk folder-download response. Skills
// beyond the budget come back as truncated entries the client fetches
// individually (each skill alone is ≤ skillspec.MaxTotalBytes).
const skillFolderDownloadMaxBytes = 50 << 20 // 50 MiB

// ---------------------------------------------------------------------------
// POST /v1/skills/{id}/uploads/prepare
// ---------------------------------------------------------------------------

// PrepareSkillUpload validates the manifest against the skill rules (root
// SKILL.md, size/count caps, clean paths) BEFORE any bytes move, then returns
// presigned PUT URLs for the blobs the org doesn't already have — the same
// wire contract as deployment prepare.
func (a *API) PrepareSkillUpload(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	skillID := chi.URLParam(r, "id")

	// Confused-deputy guard + role gate: only the owner (a live member) or an
	// admin may push content into a skill.
	skill, err := a.Store.GetSkill(r.Context(), t, skillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireSkillOwnerOrAdmin(w, r, t, skill) {
		return
	}

	var req prepareRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if err := validateSkillManifest(req.Manifest); err != nil {
		httpx.WriteError(w, err)
		return
	}

	resp, err := a.prepareMissingBlobs(r, t.OrgID, req.Manifest)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	logger(r).Info("skill upload prepared",
		"skill_id", skillID, "org_id", t.OrgID,
		"files", len(req.Manifest), "missing", len(resp.Missing))
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// validateSkillManifest maps the wire manifest onto the shared skillspec rules,
// returning a 400-mapped error on violation.
func validateSkillManifest(files []ManifestFile) error {
	infos := make([]skillspec.FileInfo, len(files))
	for i, f := range files {
		infos[i] = skillspec.FileInfo{Path: f.Path, Size: f.Size}
	}
	if err := skillspec.Validate(infos); err != nil {
		if errors.Is(err, skillspec.ErrMissingSkillMD) {
			return fmt.Errorf("%w: a skill needs a SKILL.md at its root", httpx.ErrBadRequest)
		}
		return fmt.Errorf("%w: %s", httpx.ErrBadRequest, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// POST /v1/skills/{id}/uploads  (finalize — latest-only, so finalize IS publish)
// ---------------------------------------------------------------------------

type skillFinalizeResponse struct {
	VersionID string `json:"version_id"`
	VersionNo int32  `json:"version_no"`
	// Warnings are non-fatal advisories (e.g. unparseable SKILL.md frontmatter).
	Warnings []string `json:"warnings,omitempty"`
}

// FinalizeSkillUpload server-verifies every blob (present + bytes hash == key),
// re-asserts the skill rules against verified sizes, parses SKILL.md
// frontmatter for title/description fallbacks, inserts the immutable version,
// flips the live pointer (same tx), and writes the immutable manifest object.
func (a *API) FinalizeSkillUpload(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
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

	var req finalizeRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if len(req.Manifest) == 0 || req.Digest == "" {
		httpx.WriteError(w, fmt.Errorf("%w: manifest and digest are required", httpx.ErrBadRequest))
		return
	}
	if !validSHA256(req.Digest) {
		httpx.WriteError(w, fmt.Errorf("%w: bad digest", httpx.ErrBadRequest))
		return
	}
	if err := validateSkillManifest(req.Manifest); err != nil {
		httpx.WriteError(w, err)
		return
	}

	// Server-verify the manifest (shared with deploys): recomputed digest + per-blob
	// re-hash with server-observed sizes. The client digest/sizes are never trusted.
	vm, err := a.verifyManifest(r, t.OrgID, req.Manifest, req.Digest)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	files, totalSize := vm.Files, vm.TotalSize

	// Re-assert the skill size/count caps against SERVER-OBSERVED per-file sizes
	// (the prepare-time check trusted the client's claims).
	verified := make([]skillspec.FileInfo, 0, len(files))
	for p, tgt := range files {
		verified = append(verified, skillspec.FileInfo{Path: p, Size: tgt.Size})
	}
	if err := skillspec.Validate(verified); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}

	// Parse SKILL.md frontmatter (bounded read) for title/description fallbacks.
	var warnings []string
	fm := skillspec.Frontmatter{}
	if tgt, ok := files[skillspec.SkillMD]; ok {
		if body, err := a.readBlobBounded(r, t.OrgID, tgt.SHA256, skillspec.MaxFrontmatterBytes); err == nil {
			fm = skillspec.ParseFrontmatter(body)
		}
		if fm.Name == "" && fm.Description == "" {
			warnings = append(warnings, "SKILL.md has no parseable frontmatter (name/description); the skill keeps its current metadata")
		}
	}

	ver, err := a.Store.CreateSkillVersion(r.Context(), t, store.CreateSkillVersionParams{
		SkillID:     skillID,
		ContentHash: vm.Digest,
		SizeBytes:   totalSize,
		Status:      "ready",
		Blobs:       vm.Blobs,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Fill empty metadata from frontmatter (explicit user-set values win).
	title, desc := skill.Title, skill.Description
	if title == "" && fm.Name != "" {
		title = fm.Name
	}
	if desc == "" && fm.Description != "" {
		desc = fm.Description
	}
	if title != skill.Title || desc != skill.Description {
		if _, err := a.Store.SetSkillMeta(r.Context(), t, skillID, title, desc); err != nil {
			logger(r).Warn("skill frontmatter metadata update failed", "skill_id", skillID, "err", err)
		}
	}

	body, err := json.Marshal(storedManifest{SchemaVersion: manifest.SchemaVersion, Files: files})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := a.Objects.PutSkillManifest(r.Context(), t.OrgID, skillID, ver.ID, body); err != nil {
		httpx.WriteError(w, err)
		return
	}

	// Publish (flip the live pointer) ONLY after the manifest is durably written,
	// so the GC never sees a current version whose blobs look unreferenced. This
	// also makes a re-upload of GC'd identical content republish it.
	if err := a.Store.PublishSkillVersion(r.Context(), t, skillID, ver.ID); err != nil {
		writeStoreError(w, err)
		return
	}

	logger(r).Info("skill upload finalized",
		"skill_id", skillID, "version_id", ver.ID, "version_no", ver.VersionNo,
		"org_id", t.OrgID, "size_bytes", totalSize)
	a.recordAudit(r, t, audit.ActionSkillUpload, "skill:"+skillID, map[string]any{
		"version_id": ver.ID,
		"version_no": ver.VersionNo,
		"size_bytes": totalSize,
	})
	httpx.WriteJSON(w, http.StatusCreated, skillFinalizeResponse{
		VersionID: ver.ID,
		VersionNo: ver.VersionNo,
		Warnings:  warnings,
	})
}

// readBlobBounded streams at most limit bytes of a blob (the frontmatter read).
func (a *API) readBlobBounded(r *http.Request, orgID, sha string, limit int64) ([]byte, error) {
	rc, err := a.Objects.GetBlob(r.Context(), orgID, sha)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	var total int64
	for total < limit {
		n, err := rc.Read(tmp)
		if n > 0 {
			take := int64(n)
			if total+take > limit {
				take = limit - total
			}
			buf = append(buf, tmp[:take]...)
			total += take
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}

// ---------------------------------------------------------------------------
// GET /v1/skills/{id}/files + /download
// ---------------------------------------------------------------------------

// skillFileMeta is one manifest entry (the /files listing).
type skillFileMeta struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
	SHA256      string `json:"sha256"`
}

// skillFilePayload is one downloaded file: utf8 text inline or base64 bytes.
type skillFilePayload struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"` // "utf8" | "base64"
}

// skillDownloadPayload is one whole skill's files (per-skill and bulk shapes).
type skillDownloadPayload struct {
	Slug  string             `json:"slug"`
	Files []skillFilePayload `json:"files,omitempty"`
	// Truncated marks a skill omitted from a BULK download because the response
	// budget ran out — fetch it via GET /v1/skills/{id}/download.
	Truncated bool   `json:"truncated,omitempty"`
	SkillID   string `json:"skill_id"`
	// Version is the downloaded content's version number, so a client can record
	// it and later detect when the org's copy has moved ahead (an update).
	Version int32 `json:"version"`
}

// ListSkillFiles returns the current version's manifest entries.
func (a *API) ListSkillFiles(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	skill, err := a.Store.GetSkill(r.Context(), t, chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	files, err := a.skillManifestFiles(r, t, skill)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out := make([]skillFileMeta, 0, len(files))
	for p, tgt := range files {
		out = append(out, skillFileMeta{Path: p, Size: tgt.Size, ContentType: tgt.ContentType, SHA256: tgt.SHA256})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"files": out})
}

// DownloadSkill returns the current version's files inline (utf8 or base64) —
// the shape agents/CLI write straight into .claude/skills/<slug>/.
func (a *API) DownloadSkill(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	skill, err := a.Store.GetSkill(r.Context(), t, chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	payload, _, err := a.downloadSkillPayload(r, t, skill)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionSkillDownload, "skill:"+skill.ID, map[string]any{"slug": skill.Slug})
	httpx.WriteJSON(w, http.StatusOK, payload)
}

// skillManifestFiles loads the skill's CURRENT manifest (400 when the skill
// has no finalized upload yet).
func (a *API) skillManifestFiles(r *http.Request, t store.Tenant, skill store.Skill) (map[string]manifestTarget, error) {
	if skill.CurrentVersionID == nil {
		return nil, fmt.Errorf("%w: skill %q has no uploaded content yet", httpx.ErrBadRequest, skill.Slug)
	}
	body, err := a.Objects.GetSkillManifest(r.Context(), t.OrgID, skill.ID, *skill.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	var m storedManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return m.Files, nil
}

// downloadSkillPayload builds one skill's inline file payload, reporting the
// bytes it contributed (the bulk budget input).
func (a *API) downloadSkillPayload(r *http.Request, t store.Tenant, skill store.Skill) (skillDownloadPayload, int64, error) {
	files, err := a.skillManifestFiles(r, t, skill)
	if err != nil {
		return skillDownloadPayload{}, 0, err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	payload := skillDownloadPayload{Slug: skill.Slug, SkillID: skill.ID, Version: skill.Version}
	var total int64
	for _, p := range paths {
		tgt := files[p]
		body, err := a.readBlobBounded(r, t.OrgID, tgt.SHA256, skillspec.MaxTotalBytes)
		if err != nil {
			return skillDownloadPayload{}, 0, err
		}
		content, encoding := encodeFileContent(body)
		payload.Files = append(payload.Files, skillFilePayload{Path: p, Content: content, Encoding: encoding})
		total += int64(len(body))
	}
	return payload, total, nil
}

// encodeFileContent returns utf8 text as-is and anything else base64-encoded
// (the same text/binary split the MCP download tools use).
func encodeFileContent(b []byte) (content, encoding string) {
	if utf8.Valid(b) && !containsNUL(b) {
		return string(b), "utf8"
	}
	return base64.StdEncoding.EncodeToString(b), "base64"
}

func containsNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}
