// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// siteContentMaxBytes bounds a single site read_file response and the total of a
// download_site response. It matches the MCP download tools' historical inline
// cap (10 MiB), so routing those reads through the API keeps the same ceiling.
const siteContentMaxBytes = 10 << 20 // 10 MiB

// siteFileMeta is one manifest entry in the GET /sites/{id}/files listing.
type siteFileMeta struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
	SHA256      string `json:"sha256"`
}

// siteFilePayload is one file's bytes: utf8 text inline or base64 (the same
// text/binary split the skill download endpoints use). Size is the RAW byte
// count (before any base64 expansion), so a client can report true file sizes.
type siteFilePayload struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding"` // "utf8" | "base64"
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// siteDownloadResponse is a whole site's current-version files.
type siteDownloadResponse struct {
	Slug   string            `json:"slug"`
	SiteID string            `json:"site_id"`
	Files  []siteFilePayload `json:"files"`
	// Truncated marks that the response budget ran out before every file was
	// inlined — fetch the rest via GET /v1/sites/{id}/files/content?path=...
	Truncated bool `json:"truncated,omitempty"`
}

// ListSiteFiles returns the current published version's manifest entries.
// GET /v1/sites/{id}/files — any org member (reads are RLS-scoped).
func (a *API) ListSiteFiles(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	site, err := a.Store.GetSite(r.Context(), t, chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	files, err := a.siteManifestFiles(r, t, site)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out := make([]siteFileMeta, 0, len(files))
	for p, tgt := range files {
		out = append(out, siteFileMeta{Path: p, Size: tgt.Size, ContentType: tgt.ContentType, SHA256: tgt.SHA256})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"files": out})
}

// ReadSiteFile returns one file's contents from the current published version.
// GET /v1/sites/{id}/files/content?path=<path> — a path absent from the manifest
// is a 404 (the MCP maps it back to its "not found" tool result).
func (a *API) ReadSiteFile(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		httpx.WriteError(w, fmt.Errorf("%w: query parameter 'path' is required", httpx.ErrBadRequest))
		return
	}
	site, err := a.Store.GetSite(r.Context(), t, chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	files, err := a.siteManifestFiles(r, t, site)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	tgt, ok := files[path]
	if !ok {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	// Refuse an oversized file rather than silently truncating it: readBlobBounded
	// caps the read at siteContentMaxBytes, so serving it would return the first
	// 10 MiB as if complete (with Size reported as 10 MiB). The manifest carries
	// the true size, so reject before reading.
	if tgt.Size > siteContentMaxBytes {
		httpx.WriteError(w, fmt.Errorf("%w: file %q is %d bytes, over the %d-byte read limit", httpx.ErrBadRequest, path, tgt.Size, int64(siteContentMaxBytes)))
		return
	}
	body, err := a.readBlobBounded(r, t.OrgID, tgt.SHA256, siteContentMaxBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	content, encoding := encodeFileContent(body)
	httpx.WriteJSON(w, http.StatusOK, siteFilePayload{
		Path: path, Content: content, Encoding: encoding, ContentType: tgt.ContentType, Size: int64(len(body)),
	})
}

// DownloadSite returns every file of the current published version inline, up to
// siteContentMaxBytes total (Truncated past that). GET /v1/sites/{id}/download.
func (a *API) DownloadSite(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	site, err := a.Store.GetSite(r.Context(), t, chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	files, err := a.siteManifestFiles(r, t, site)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	resp := siteDownloadResponse{Slug: site.Slug, SiteID: site.ID, Files: []siteFilePayload{}}
	var total int64
	for _, p := range paths {
		tgt := files[p]
		// Budget on the manifest's true size BEFORE reading: readBlobBounded caps
		// each read at siteContentMaxBytes, so budgeting on the returned bytes would
		// let an over-cap file through truncated-but-marked-complete. Omit any file
		// that won't fit whole (Truncated), never serve a partial one.
		if total+tgt.Size > siteContentMaxBytes {
			resp.Truncated = true
			break
		}
		body, err := a.readBlobBounded(r, t.OrgID, tgt.SHA256, siteContentMaxBytes)
		if err != nil {
			httpx.WriteError(w, err)
			return
		}
		size := int64(len(body))
		total += size
		content, encoding := encodeFileContent(body)
		resp.Files = append(resp.Files, siteFilePayload{
			Path: p, Content: content, Encoding: encoding, ContentType: tgt.ContentType, Size: size,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// siteManifestFiles loads the site's CURRENT published manifest (400 when the
// site has no published version yet — the caller usually short-circuits that
// case before ever calling the API).
func (a *API) siteManifestFiles(r *http.Request, t store.Tenant, site store.Site) (map[string]manifestTarget, error) {
	if site.CurrentVersionID == nil {
		return nil, fmt.Errorf("%w: site %q has no published version yet", httpx.ErrBadRequest, site.Slug)
	}
	body, err := a.Objects.GetManifest(r.Context(), t.OrgID, site.ID, *site.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	var m storedManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return m.Files, nil
}
