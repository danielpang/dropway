// Package storage is the object-store seam for content-addressed blobs and
// immutable deploy manifests (docs/ARCHITECTURE.md §3 object layout, §7.1).
//
// Object layout (content-addressed, per-org, dedup scoped to the org so a global
// existence check can't become a cross-tenant content-confirmation oracle — §10):
//
//	blobs/<org_id>/<sha256>                                  -- one file's bytes
//	manifests/<org_id>/<site_id>/<version_id>.json           -- the deploy manifest
//
// The interface is deliberately small: presign a direct-to-store PUT for each
// missing blob (the CLI/browser uploads bytes itself), HEAD to learn which blobs
// already exist (dedup), GetObject to server-verify the stored bytes hash == the
// key before a version is marked ready, and PutManifest to write the immutable
// per-deploy manifest. The S3/R2 implementation lives in s3.go; an in-memory
// Fake (memory.go) backs unit tests without a live store.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrNotFound is returned by Head/GetObject when the object is absent.
var ErrNotFound = errors.New("storage: object not found")

// Store is the object-store surface the deploy/serve loop depends on.
type Store interface {
	// PresignPut returns a URL the client can PUT the blob's bytes to directly.
	// The key is derived server-side from orgID + the server-validated sha256 —
	// never a client-supplied path (§10). The URL expires after ttl.
	PresignPut(ctx context.Context, orgID, sha256 string, ttl time.Duration) (string, error)

	// HeadBlob reports whether the blob already exists for this org (drives
	// only-changed-blob upload). It returns the stored size on hit.
	HeadBlob(ctx context.Context, orgID, sha256 string) (exists bool, size int64, err error)

	// GetBlob streams a blob's bytes back. Callers must Close the reader. Used to
	// server-verify the stored bytes hash == the key before marking a version
	// ready. Returns ErrNotFound if absent.
	GetBlob(ctx context.Context, orgID, sha256 string) (io.ReadCloser, error)

	// PutBlob writes a blob directly (used by tests and any server-side ingest
	// path; the primary upload path is the client PUT to a PresignPut URL).
	PutBlob(ctx context.Context, orgID, sha256 string, r io.Reader, size int64, contentType string) error

	// PutManifest writes the immutable per-deploy manifest JSON.
	PutManifest(ctx context.Context, orgID, siteID, versionID string, manifest []byte) error

	// GetManifest reads a deploy manifest back (e.g. for the serving rebuild / a
	// content-type lookup). Returns ErrNotFound if absent.
	GetManifest(ctx context.Context, orgID, siteID, versionID string) ([]byte, error)
}

// BlobKey returns the canonical R2/S3 key for a blob. Exported so callers
// (and tests) build keys consistently; the key is content-addressed and
// per-org so dedup never leaks across tenants (§10).
func BlobKey(orgID, sha256 string) string {
	return fmt.Sprintf("blobs/%s/%s", orgID, sha256)
}

// ManifestKey returns the canonical key for a deploy manifest.
func ManifestKey(orgID, siteID, versionID string) string {
	return fmt.Sprintf("manifests/%s/%s/%s.json", orgID, siteID, versionID)
}
