// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
)

// ---------------------------------------------------------------------------
// POST   /v1/sites/{id}/versions/{versionID}/preview   (create / renew / extend)
// DELETE /v1/sites/{id}/versions/{versionID}/preview
// ---------------------------------------------------------------------------

type previewResponse struct {
	PreviewURL string `json:"preview_url"`
	// ExpiresAt is the RFC3339 deadline the edge enforces (410 past it). Calling
	// the endpoint again re-creates an expired preview or extends a live one.
	ExpiresAt string `json:"expires_at"`
	VersionID string `json:"version_id"`
}

// CreatePreview registers (or renews) the time-limited preview host for one
// site version and projects it to the edge. Re-creating an expired or deleted
// preview is the same call: the draft's blobs + manifest are retained by the
// draft-aware GC, so this is one row + one KV write.
func (a *API) CreatePreview(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireProjection(w) {
		return
	}
	siteID := chi.URLParam(r, "id")
	versionID := chi.URLParam(r, "versionID")

	prev, err := a.Store.CreatePreviewRoute(r.Context(), t, siteID, versionID, a.previewTTL())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := a.Projection.PutRoute(r.Context(), prev.Host, prev.Route); err != nil {
		// The DB row is authoritative; surface the failure so the caller retries
		// (an unprojected preview URL would be dead at the edge).
		logger(r).Error("preview projection write failed",
			"host", prev.Host, "site_id", siteID, "version_id", versionID, "err", err)
		httpx.WriteError(w, err)
		return
	}

	logger(r).Info("preview created",
		"site_id", siteID, "version_id", versionID, "host", prev.Host,
		"expires_at", prev.ExpiresAt, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionPreviewCreate, "site:"+siteID, map[string]any{
		"version_id": versionID,
		"host":       prev.Host,
		"expires_at": prev.ExpiresAt.UTC().Format(time.RFC3339),
	})
	httpx.WriteJSON(w, http.StatusOK, previewResponse{
		PreviewURL: a.ContentURL(prev.Host),
		ExpiresAt:  prev.ExpiresAt.UTC().Format(time.RFC3339),
		VersionID:  versionID,
	})
}

// DeletePreview removes a version's preview host(s) immediately (row + KV key).
func (a *API) DeletePreview(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireProjection(w) {
		return
	}
	siteID := chi.URLParam(r, "id")
	versionID := chi.URLParam(r, "versionID")

	hosts, err := a.Store.DeletePreviewRoutes(r.Context(), t, siteID, versionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for _, host := range hosts {
		if err := a.Projection.DeleteRoute(r.Context(), host); err != nil {
			// Best-effort: the row is gone (rebuild won't resurrect it) and the edge
			// 410s at the deadline anyway; the ops sweep cleans stragglers.
			logger(r).Error("preview route delete failed",
				"host", host, "site_id", siteID, "version_id", versionID, "err", err)
		}
	}

	logger(r).Info("preview deleted",
		"site_id", siteID, "version_id", versionID, "hosts", len(hosts), "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionPreviewDelete, "site:"+siteID, map[string]any{
		"version_id": versionID,
	})
	w.WriteHeader(http.StatusNoContent)
}
