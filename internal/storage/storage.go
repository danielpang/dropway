// Package storage is the object-store seam for content-addressed blobs and
// immutable deploy manifests.
//
// Object layout (content-addressed, per-org, dedup scoped to the org so a global
// existence check can't become a cross-tenant content-confirmation oracle):
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

// BlobInfo is one stored blob's content-addressed sha256 plus its store-side
// last-modified time. The R2 version GC needs LastModified to apply an AGE GUARD:
// a blob is uploaded via a presigned PUT BEFORE its version row is finalized, so a
// GC overlapping an in-flight deploy could otherwise delete that deploy's just-
// uploaded, not-yet-referenced blobs and corrupt it. The GC therefore refuses to
// delete an orphan younger than GCPolicy.MinAge (presign TTL + safety margin),
// using LastModified.
type BlobInfo struct {
	SHA          string
	LastModified time.Time
}

// Store is the object-store surface the deploy/serve loop depends on.
type Store interface {
	// PresignPut returns a URL the client can PUT the blob's bytes to directly.
	// The key is derived server-side from orgID + the server-validated sha256 —
	// never a client-supplied path. The URL expires after ttl.
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

	// PutSkillManifest writes the immutable per-skill-version manifest JSON
	// (skill-manifests/<org>/<skill>/<version>.json — skills reuse the blob
	// store; only the manifest namespace differs from deploys).
	PutSkillManifest(ctx context.Context, orgID, skillID, versionID string, manifest []byte) error

	// GetSkillManifest reads a skill manifest back (downloads + the GC's
	// referenced-blob collection). Returns ErrNotFound if absent.
	GetSkillManifest(ctx context.Context, orgID, skillID, versionID string) ([]byte, error)

	// PutChatTranscript writes the compiled transcript JSON of one shared chat
	// log (chat-transcripts/<org>/<chat_id>.json). Unlike deploy/skill
	// manifests it is MUTABLE — rewritten after every append/delete — and a
	// pure projection of Postgres (rebuildable, never authoritative). The
	// serving Worker reads it directly from the bucket at the reserved
	// /__dropway/chat path.
	PutChatTranscript(ctx context.Context, orgID, chatID string, transcript []byte) error

	// GetChatTranscript reads a compiled transcript back. Returns ErrNotFound
	// if absent.
	GetChatTranscript(ctx context.Context, orgID, chatID string) ([]byte, error)

	// DeleteChatTranscript removes a log's compiled transcript (log deletion /
	// panel teardown). Deleting an absent key is a no-op.
	DeleteChatTranscript(ctx context.Context, orgID, chatID string) error

	// ListBlobInfos returns every blob stored under the org's prefix
	// (blobs/<org_id>/) as a {SHA, LastModified} pair. It is the input to the R2
	// version GC: blobs no longer referenced by any retained deploy manifest are
	// orphans ("R2 version GC"), but the GC only deletes an
	// orphan OLDER than GCPolicy.MinAge — so the LastModified time is surfaced here
	// to guard against deleting an in-flight deploy's just-uploaded (not-yet-
	// referenced) blobs. It must paginate internally so a large org enumerates fully.
	ListBlobInfos(ctx context.Context, orgID string) ([]BlobInfo, error)

	// DeleteBlob removes a single blob by its content-addressed key. Used by the GC
	// to delete an orphaned blob. Deleting an absent blob is not an error
	// (idempotent), so a re-run of the GC is safe.
	DeleteBlob(ctx context.Context, orgID, sha256 string) error
}

// BlobKey returns the canonical R2/S3 key for a blob. Exported so callers
// (and tests) build keys consistently; the key is content-addressed and
// per-org so dedup never leaks across tenants.
func BlobKey(orgID, sha256 string) string {
	return fmt.Sprintf("blobs/%s/%s", orgID, sha256)
}

// ManifestKey returns the canonical key for a deploy manifest.
func ManifestKey(orgID, siteID, versionID string) string {
	return fmt.Sprintf("manifests/%s/%s/%s.json", orgID, siteID, versionID)
}

// SkillManifestKey returns the canonical key for a skill-version manifest.
// Skills share the per-org content-addressed blob namespace with deploys
// (dedup applies across both); only the manifest namespace is separate.
func SkillManifestKey(orgID, skillID, versionID string) string {
	return fmt.Sprintf("skill-manifests/%s/%s/%s.json", orgID, skillID, versionID)
}

// ChatTranscriptKey returns the canonical key for a shared chat log's
// compiled transcript. The serving Worker derives the same key from a
// RouteValue's org_id + chat_id, so the layout is a cross-service contract.
func ChatTranscriptKey(orgID, chatID string) string {
	return fmt.Sprintf("chat-transcripts/%s/%s.json", orgID, chatID)
}
