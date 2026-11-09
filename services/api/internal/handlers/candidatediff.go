// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/services/api/internal/store"
)

const (
	diffFileCap = 80
	diffTextCap = 2048
	diffReadCap = 64 << 10
)

type candidateDiffFile struct {
	Path            string `json:"path"`
	Change          string `json:"change"`
	Binary          bool   `json:"binary"`
	BeforeSHA256    string `json:"before_sha256,omitempty"`
	AfterSHA256     string `json:"after_sha256,omitempty"`
	BeforeSize      int64  `json:"before_size,omitempty"`
	AfterSize       int64  `json:"after_size,omitempty"`
	Before          string `json:"before,omitempty"`
	After           string `json:"after,omitempty"`
	BeforeTruncated bool   `json:"before_truncated,omitempty"`
	AfterTruncated  bool   `json:"after_truncated,omitempty"`
	Untrusted       bool   `json:"untrusted"`
}

// GetCandidateDiff returns a bounded before/after of the candidate against its
// pinned base. File text is untrusted. A hash mismatch fails the whole request.
func (a *API) GetCandidateDiff(w http.ResponseWriter, r *http.Request) {
	t, ok := a.readRoom(w, r)
	if !ok || !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	cand, base, proposed, err := a.Store.CandidateVersions(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "candidateID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	baseFiles, err := a.verifiedManifest(r.Context(), t.OrgID, cand.SiteID, base.ID, base.ContentHash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	propFiles, err := a.verifiedManifest(r.Context(), t.OrgID, cand.SiteID, proposed.ID, proposed.ContentHash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	files, truncated, omitted, err := a.diffFiles(r.Context(), t.OrgID, baseFiles, propFiles)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"candidate_id":      cand.ID,
		"site_id":           cand.SiteID,
		"base_version_id":   base.ID,
		"base_content_hash": base.ContentHash,
		"content_hash":      proposed.ContentHash,
		"verified":          true,
		"truncated":         truncated,
		"omitted_files":     omitted,
		"files":             files,
		"comment_content":   "untrusted_data",
	})
}

func (a *API) verifiedManifest(ctx context.Context, orgID, siteID, versionID, want string) (map[string]manifest.Target, error) {
	raw, err := a.Objects.GetManifest(ctx, orgID, siteID, versionID)
	if err != nil {
		return nil, fmt.Errorf("%w: manifest unavailable", store.ErrCandidateArtifact)
	}
	var stored manifest.Stored
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("%w: manifest unreadable", store.ErrCandidateArtifact)
	}
	files := make([]manifest.File, 0, len(stored.Files))
	for path, tgt := range stored.Files {
		files = append(files, manifest.File{Path: path, SHA256: tgt.SHA256})
	}
	if manifest.Digest(files) != want {
		return nil, fmt.Errorf("%w: content hash mismatch", store.ErrCandidateArtifact)
	}
	return stored.Files, nil
}

func (a *API) diffFiles(ctx context.Context, orgID string, base, proposed map[string]manifest.Target) ([]candidateDiffFile, bool, int, error) {
	paths := map[string]struct{}{}
	for p := range base {
		paths[p] = struct{}{}
	}
	for p := range proposed {
		paths[p] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	truncated := len(ordered) > diffFileCap
	omitted := 0
	if truncated {
		omitted = len(ordered) - diffFileCap
		ordered = ordered[:diffFileCap]
	}
	out := make([]candidateDiffFile, 0, len(ordered))
	for _, path := range ordered {
		before, hasBefore := base[path]
		after, hasAfter := proposed[path]
		change := "modified"
		switch {
		case hasBefore && !hasAfter:
			change = "removed"
		case !hasBefore && hasAfter:
			change = "added"
		case before.SHA256 == after.SHA256:
			change = "unchanged"
		}
		item := candidateDiffFile{
			Path: path, Change: change, Untrusted: true,
		}
		if hasBefore {
			item.BeforeSHA256 = before.SHA256
			item.BeforeSize = before.Size
		}
		if hasAfter {
			item.AfterSHA256 = after.SHA256
			item.AfterSize = after.Size
		}
		if change != "unchanged" {
			if hasBefore {
				item.Binary = item.Binary || binaryTarget(before)
			}
			if hasAfter {
				item.Binary = item.Binary || binaryTarget(after)
			}
			if !item.Binary {
				if hasBefore {
					text, cut, bin, err := a.readVerifiedText(ctx, orgID, before)
					if err != nil {
						return nil, false, 0, err
					}
					if bin {
						item.Binary = true
					} else {
						item.Before, item.BeforeTruncated = text, cut
					}
				}
				if hasAfter && !item.Binary {
					text, cut, bin, err := a.readVerifiedText(ctx, orgID, after)
					if err != nil {
						return nil, false, 0, err
					}
					if bin {
						item.Binary = true
						item.Before, item.BeforeTruncated = "", false
					} else {
						item.After, item.AfterTruncated = text, cut
					}
				}
			}
		}
		out = append(out, item)
	}
	return out, truncated, omitted, nil
}

func binaryTarget(t manifest.Target) bool {
	if t.Size > diffReadCap {
		return true
	}
	switch {
	case len(t.ContentType) >= 6 && (t.ContentType[:6] == "image/" || t.ContentType[:6] == "audio/" || t.ContentType[:6] == "video/"):
		return true
	case t.ContentType == "application/octet-stream" || t.ContentType == "application/pdf" || t.ContentType == "application/zip":
		return true
	default:
		return false
	}
}

func (a *API) readVerifiedText(ctx context.Context, orgID string, tgt manifest.Target) (string, bool, bool, error) {
	rc, err := a.Objects.GetBlob(ctx, orgID, tgt.SHA256)
	if err != nil {
		return "", false, false, err
	}
	defer rc.Close()
	buf, err := io.ReadAll(io.LimitReader(rc, diffReadCap+1))
	if err != nil {
		return "", false, false, err
	}
	if int64(len(buf)) != tgt.Size {
		return "", false, false, fmt.Errorf("%w: blob length mismatch", store.ErrCandidateArtifact)
	}
	sum := sha256.Sum256(buf)
	if hex.EncodeToString(sum[:]) != tgt.SHA256 {
		return "", false, false, fmt.Errorf("%w: blob hash mismatch", store.ErrCandidateArtifact)
	}
	if bytes.IndexByte(buf, 0) >= 0 || !utf8.Valid(buf) {
		return "", false, true, nil
	}
	cut := false
	if len(buf) > diffTextCap {
		buf = buf[:diffTextCap]
		cut = true
	}
	text := string(buf)
	if cut {
		text += "\n...[truncated]"
	}
	return text, cut, false, nil
}
