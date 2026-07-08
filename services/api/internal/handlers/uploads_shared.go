// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// The prepare/finalize upload contract is shared verbatim by site deploys and
// skill uploads: discover missing blobs + presign them, then server-verify every
// blob (present, stored bytes hash == key, server-observed size) and recompute
// the whole-upload digest. These helpers are the single home for those
// never-trust-the-client invariants so the two upload paths cannot drift.

// prepareMissingBlobs returns the manifest's blobs the org doesn't already have,
// each with a presigned PUT URL (dedup is per-org via the blob key prefix). The
// returned error is already httpx-wrapped for a bad sha256 (400); storage errors
// pass through as opaque 500s.
func (a *API) prepareMissingBlobs(r *http.Request, orgID string, mf []ManifestFile) (prepareResponse, error) {
	missing := make([]string, 0)
	uploads := make(map[string]string)
	seen := make(map[string]struct{})
	for _, f := range mf {
		if !validSHA256(f.SHA256) {
			return prepareResponse{}, fmt.Errorf("%w: bad sha256 %q", httpx.ErrBadRequest, f.SHA256)
		}
		if _, dup := seen[f.SHA256]; dup {
			continue // same content behind multiple paths: upload once
		}
		seen[f.SHA256] = struct{}{}

		exists, _, err := a.Objects.HeadBlob(r.Context(), orgID, f.SHA256)
		if err != nil {
			return prepareResponse{}, err
		}
		if exists {
			continue
		}
		url, err := a.Objects.PresignPut(r.Context(), orgID, f.SHA256, presignTTL)
		if err != nil {
			return prepareResponse{}, err
		}
		missing = append(missing, f.SHA256)
		uploads[f.SHA256] = url
	}
	return prepareResponse{Missing: missing, Uploads: uploads}, nil
}

// verifiedManifest is the server-derived, trusted view of a finalize request: the
// per-path targets (with server-observed sizes), the distinct blobs (+ sizes) for
// the storage meter, the total size, and the recomputed whole-upload digest.
type verifiedManifest struct {
	Files     map[string]manifestTarget
	Blobs     []store.BlobSize
	TotalSize int64
	Digest    string
}

// verifyManifest recomputes the whole-upload digest server-side (rejecting a
// mismatch), then verifies each unique blob is present and its stored bytes hash
// == its key, recording the SERVER-OBSERVED size and rejecting a client-claimed
// size that disagrees. The client digest and client sizes are never trusted as
// identifiers. Errors are httpx-wrapped (400) where the client is at fault.
func (a *API) verifyManifest(r *http.Request, orgID string, mf []ManifestFile, clientDigest string) (verifiedManifest, error) {
	manifestFiles := make([]manifest.File, len(mf))
	for i, f := range mf {
		manifestFiles[i] = manifest.File{Path: f.Path, SHA256: f.SHA256}
	}
	serverDigest := manifest.Digest(manifestFiles)
	if serverDigest != clientDigest {
		return verifiedManifest{}, fmt.Errorf("%w: digest mismatch: client sent %s, server computed %s",
			httpx.ErrBadRequest, clientDigest, serverDigest)
	}

	sizeBySHA := make(map[string]int64, len(mf))
	files := make(map[string]manifestTarget, len(mf))
	for _, f := range mf {
		if !validSHA256(f.SHA256) {
			return verifiedManifest{}, fmt.Errorf("%w: bad sha256 %q", httpx.ErrBadRequest, f.SHA256)
		}
		observed, ok := sizeBySHA[f.SHA256]
		if !ok {
			n, err := a.verifyBlob(r, orgID, f.SHA256)
			if err != nil {
				return verifiedManifest{}, err
			}
			observed = n
			sizeBySHA[f.SHA256] = n
		}
		if f.Size != observed {
			return verifiedManifest{}, fmt.Errorf("%w: file %q claims size %d but stored blob %s is %d bytes",
				httpx.ErrBadRequest, f.Path, f.Size, f.SHA256, observed)
		}
		files[f.Path] = manifestTarget{SHA256: f.SHA256, ContentType: f.ContentType, Size: observed}
	}

	var totalSize int64
	blobs := make([]store.BlobSize, 0, len(sizeBySHA))
	for sha, n := range sizeBySHA {
		totalSize += n
		blobs = append(blobs, store.BlobSize{SHA: sha, Size: n})
	}
	return verifiedManifest{Files: files, Blobs: blobs, TotalSize: totalSize, Digest: serverDigest}, nil
}
