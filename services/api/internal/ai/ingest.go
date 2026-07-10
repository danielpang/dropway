// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"

	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// maxIngestBytes caps a single AI draft's total uncompressed size (a runaway
// build can't fill the org's storage). 100 MiB is well above any static site.
const maxIngestBytes = 100 << 20

// storedManifest mirrors the deploy handler's on-disk manifest shape so the
// serving worker resolves AI drafts identically to normal deploys.
type storedManifest struct {
	SchemaVersion int                       `json:"schema_version"`
	Files         map[string]manifestTarget `json:"files"`
}

type manifestTarget struct {
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
}

// ingestTar reads a tar of the sandbox's working tree, uploads each file as a
// content-addressed blob (server computes the hashes — never the sandbox), then
// creates an immutable AI draft version with the manifest written to storage.
// It returns the created version.
//
// This is the server-authoritative counterpart to the deploy prepare/finalize
// flow: because the API already holds the bytes (it exported them from the
// sandbox), it uploads directly via storage.PutBlob and skips the presign round
// trip, but the hashing + dedup-aware metering are identical (store.CreateSiteVersion
// runs the same accountStorage path).
func ingestTar(ctx context.Context, objects storage.Store, st *store.Store, t store.Tenant, siteID string, r io.Reader) (store.SiteVersion, error) {
	files := map[string]manifestTarget{}
	blobs := []store.BlobSize{}
	seen := map[string]struct{}{}
	manifestFiles := []manifest.File{}
	var total int64

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return store.SiteVersion{}, fmt.Errorf("ai ingest: read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		rel := normalizePath(hdr.Name)
		if rel == "" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxIngestBytes+1))
		if err != nil {
			return store.SiteVersion{}, fmt.Errorf("ai ingest: read %q: %w", rel, err)
		}
		total += int64(len(data))
		if total > maxIngestBytes {
			return store.SiteVersion{}, fmt.Errorf("ai ingest: draft exceeds %d bytes", maxIngestBytes)
		}
		sum := sha256.Sum256(data)
		sha := hex.EncodeToString(sum[:])

		if _, dup := seen[sha]; !dup {
			seen[sha] = struct{}{}
			if err := objects.PutBlob(ctx, t.OrgID, sha, bytes.NewReader(data), int64(len(data)), contentType(rel)); err != nil {
				return store.SiteVersion{}, fmt.Errorf("ai ingest: put blob %s: %w", sha, err)
			}
			blobs = append(blobs, store.BlobSize{SHA: sha, Size: int64(len(data))})
		}
		files[rel] = manifestTarget{SHA256: sha, ContentType: contentType(rel), Size: int64(len(data))}
		manifestFiles = append(manifestFiles, manifest.File{Path: rel, SHA256: sha})
	}
	if len(files) == 0 {
		return store.SiteVersion{}, fmt.Errorf("ai ingest: no files produced")
	}

	digest := manifest.Digest(manifestFiles)
	ver, err := st.CreateSiteVersion(ctx, t, store.CreateSiteVersionParams{
		SiteID:      siteID,
		ContentHash: digest,
		SizeBytes:   total,
		Status:      "ready",
		CreatedVia:  "ai",
		Blobs:       blobs,
	})
	if err != nil {
		return store.SiteVersion{}, err
	}

	body, err := json.Marshal(storedManifest{SchemaVersion: manifest.SchemaVersion, Files: files})
	if err != nil {
		return store.SiteVersion{}, err
	}
	if err := objects.PutManifest(ctx, t.OrgID, siteID, ver.ID, body); err != nil {
		return store.SiteVersion{}, fmt.Errorf("ai ingest: put manifest: %w", err)
	}
	return ver, nil
}

// normalizePath cleans a tar entry name into a forward-slash site-relative path,
// dropping any leading "./" or "/" and rejecting traversal.
func normalizePath(name string) string {
	p := path.Clean("/" + strings.ReplaceAll(name, "\\", "/"))
	return strings.TrimPrefix(p, "/")
}

// contentType guesses a file's content type from its extension, defaulting to
// application/octet-stream. Serving re-derives this too; storing it makes the
// manifest self-describing.
func contentType(rel string) string {
	if ct := mime.TypeByExtension(path.Ext(rel)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
