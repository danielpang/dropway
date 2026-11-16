// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
)

type previewFile struct {
	Path   string `json:"path"`
	Data   string `json:"data"`
	SHA256 string `json:"sha256"`
}

// CandidatePreview returns verified, private artifact bytes for the dashboard's
// scriptless renderer. It never serves uploaded HTML as an executable response.
func (a *API) CandidatePreview(w http.ResponseWriter, r *http.Request) {
	t, ok := a.readRoom(w, r)
	if !ok || !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	roomID, id := chi.URLParam(r, "roomID"), chi.URLParam(r, "candidateID")
	cand, base, proposed, err := a.Store.CandidateVersions(r.Context(), t, roomID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if a.Rooms == nil {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	_, rights, err := a.Rooms.RoomAccess(r.Context(), t, roomID, cand.SiteID)
	if err != nil || !rights.CanRead {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	before, err := a.previewFiles(r.Context(), t.OrgID, cand.SiteID, base)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	after, err := a.previewFiles(r.Context(), t.OrgID, cand.SiteID, proposed)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Recheck membership and the candidate binding after object storage reads.
	if !a.requireOrgMember(w, r, t) {
		return
	}
	current, _, _, err := a.Store.CandidateVersions(r.Context(), t, roomID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if current.BaseContentHash != cand.BaseContentHash || current.ContentHash != cand.ContentHash {
		writeStoreError(w, store.ErrCandidateArtifact)
		return
	}
	_, rights, err = a.Rooms.RoomAccess(r.Context(), t, roomID, cand.SiteID)
	if err != nil || !rights.CanRead {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"candidate_id": cand.ID, "base_content_hash": base.ContentHash, "content_hash": proposed.ContentHash, "before": before, "after": after})
}

func (a *API) previewFiles(ctx context.Context, org, site string, version store.SiteVersion) ([]previewFile, error) {
	files, err := a.verifiedManifest(ctx, org, site, version.ID, version.ContentHash)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	var total int64
	for name, target := range files {
		switch strings.ToLower(path.Ext(name)) {
		case ".html", ".htm", ".css", ".png", ".jpg", ".jpeg", ".webp", ".woff", ".woff2":
			if target.Size < 0 || target.Size > 2<<20 {
				return nil, store.ErrCandidateArtifact
			}
			total += target.Size
			paths = append(paths, name)
		}
	}
	if total > 10<<20 || len(paths) > 500 {
		return nil, store.ErrCandidateArtifact
	}
	sort.Strings(paths)
	out := make([]previewFile, 0, len(paths))
	for _, name := range paths {
		target := files[name]
		reader, err := a.Objects.GetBlob(ctx, org, target.SHA256)
		if err != nil {
			return nil, store.ErrCandidateArtifact
		}
		data, err := io.ReadAll(io.LimitReader(reader, target.Size+1))
		reader.Close()
		sum := sha256.Sum256(data)
		if err != nil || int64(len(data)) != target.Size || hex.EncodeToString(sum[:]) != target.SHA256 {
			return nil, store.ErrCandidateArtifact
		}
		out = append(out, previewFile{Path: name, Data: base64.StdEncoding.EncodeToString(data), SHA256: target.SHA256})
	}
	return out, nil
}
