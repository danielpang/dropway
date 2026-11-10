// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
)

const maxReviewFileBytes = 2 << 20

// ReadReviewVersionFile returns verified bytes, never browser-executable HTML.
// Only the trusted dashboard renderer reconstructs a scriptless static view.
func (a *API) ReadReviewVersionFile(w http.ResponseWriter, r *http.Request) {
	t, ok := a.readRoom(w, r)
	if !ok || !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	roomID, siteID, versionID := chi.URLParam(r, "roomID"), chi.URLParam(r, "siteID"), chi.URLParam(r, "versionID")
	_, rights, err := a.Rooms.RoomAccess(r.Context(), t, roomID, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !rights.CanRead {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	ver, err := a.Store.GetSiteVersion(r.Context(), t, versionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if ver.SiteID != siteID || ver.OrgID != t.OrgID {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.ContainsAny(p, "\\\x00") {
		httpx.WriteError(w, fmt.Errorf("%w: invalid artifact path", httpx.ErrBadRequest))
		return
	}
	files, err := a.verifiedManifest(r.Context(), t.OrgID, siteID, versionID, ver.ContentHash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	target, ok := files[p]
	if !ok {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	if target.Size < 0 || target.Size > maxReviewFileBytes {
		writeReviewTooLarge(w)
		return
	}
	rc, err := a.Objects.GetBlob(r.Context(), t.OrgID, target.SHA256)
	if err != nil {
		writeStoreError(w, store.ErrCandidateArtifact)
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxReviewFileBytes+1))
	sum := sha256.Sum256(data)
	if err != nil || int64(len(data)) != target.Size || hex.EncodeToString(sum[:]) != target.SHA256 {
		writeStoreError(w, store.ErrCandidateArtifact)
		return
	}
	// Recheck access after reading potentially slow object storage.
	if !a.requireOrgMember(w, r, t) {
		return
	}
	if _, rights, err = a.Rooms.RoomAccess(r.Context(), t, roomID, siteID); err != nil {
		writeStoreError(w, err)
		return
	}
	if !rights.CanRead {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"site_id": siteID, "version_id": versionID, "content_hash": ver.ContentHash, "path": p, "content_base64": base64.StdEncoding.EncodeToString(data), "content_type": http.DetectContentType(data), "sha256": target.SHA256})
}
