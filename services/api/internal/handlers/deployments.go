// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// ---------------------------------------------------------------------------
// Manifest wire shapes (shared with the CLI / dashboard drag-and-drop).
// ---------------------------------------------------------------------------

// ManifestFile is one file in a deploy: request-path → content hash (+ size +
// content-type). The server derives the R2 blob key from the authenticated org +
// this sha256 — never a client path.
type ManifestFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}

// storedManifest is the immutable per-deploy manifest written to R2 at
// manifests/<org>/<site>/<version>.json. Serving resolves request-path → sha256
// via this map, then streams blobs/<org>/<sha256>.
type storedManifest struct {
	SchemaVersion int                       `json:"schema_version"`
	Files         map[string]manifestTarget `json:"files"` // request-path → target
}

type manifestTarget struct {
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
}

// ---------------------------------------------------------------------------
// POST /v1/sites/{id}/deployments/prepare
// ---------------------------------------------------------------------------

type prepareRequest struct {
	Manifest []ManifestFile `json:"manifest"`
}

type prepareResponse struct {
	// Missing is the set of blob sha256s not already present for this org.
	Missing []string `json:"missing"`
	// Uploads maps each missing sha256 → a presigned PUT URL for direct upload.
	Uploads map[string]string `json:"uploads"`
}

// PrepareDeployment computes which referenced blobs the org doesn't already have
// (only-changed-blob upload) and returns a presigned PUT URL for each. Dedup is
// scoped to the caller's org via the per-org blob key prefix.
func (a *API) PrepareDeployment(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	// Confused-deputy guard: the site must belong to the active tenant.
	if _, err := a.Store.GetSite(r.Context(), t, siteID); err != nil {
		writeStoreError(w, err)
		return
	}

	var req prepareRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if len(req.Manifest) == 0 {
		httpx.WriteError(w, fmt.Errorf("%w: manifest is empty", httpx.ErrBadRequest))
		return
	}

	missing := make([]string, 0)
	uploads := make(map[string]string)
	seen := make(map[string]struct{})
	for _, f := range req.Manifest {
		if !validSHA256(f.SHA256) {
			httpx.WriteError(w, fmt.Errorf("%w: bad sha256 %q", httpx.ErrBadRequest, f.SHA256))
			return
		}
		if _, dup := seen[f.SHA256]; dup {
			continue // same content referenced by multiple paths: upload once
		}
		seen[f.SHA256] = struct{}{}

		exists, _, err := a.Objects.HeadBlob(r.Context(), t.OrgID, f.SHA256)
		if err != nil {
			httpx.WriteError(w, err)
			return
		}
		if exists {
			continue
		}
		url, err := a.Objects.PresignPut(r.Context(), t.OrgID, f.SHA256, presignTTL)
		if err != nil {
			httpx.WriteError(w, err)
			return
		}
		missing = append(missing, f.SHA256)
		uploads[f.SHA256] = url
	}

	logger(r).Info("deployment prepared",
		"site_id", siteID, "org_id", t.OrgID,
		"files", len(req.Manifest), "missing", len(missing))
	httpx.WriteJSON(w, http.StatusOK, prepareResponse{Missing: missing, Uploads: uploads})
}

// ---------------------------------------------------------------------------
// POST /v1/sites/{id}/deployments  (finalize)
// ---------------------------------------------------------------------------

type finalizeRequest struct {
	Manifest []ManifestFile `json:"manifest"`
	// Digest is the whole-deploy content address (sha256 over the sorted
	// "<sha256>  <path>\n" lines) — the version's content_hash and idempotency key.
	Digest string `json:"digest"`
}

type finalizeResponse struct {
	VersionID  string `json:"version_id"`
	VersionNo  int32  `json:"version_no"`
	PreviewURL string `json:"preview_url"`
}

// FinalizeDeployment verifies every referenced blob is present AND its stored
// bytes hash == the claimed sha256 (server-verify), writes the immutable
// per-deploy manifest to R2, and inserts the immutable site_version (status=ready,
// next version_no). It returns the version id + a preview URL.
func (a *API) FinalizeDeployment(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireObjects(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	site, err := a.Store.GetSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
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

	// Recompute the whole-deploy digest SERVER-SIDE from the manifest and reject a
	// mismatch (FIX 2): the client digest is never trusted as an identifier.
	// content_hash is the UNIQUE(site_id,content_hash) idempotency key, so a client
	// that could forge it could short-circuit GetSiteVersionByContentHash and
	// republish the wrong version. The server-derived value is what we persist.
	manifestFiles := make([]manifest.File, len(req.Manifest))
	for i, f := range req.Manifest {
		manifestFiles[i] = manifest.File{Path: f.Path, SHA256: f.SHA256}
	}
	serverDigest := manifest.Digest(manifestFiles)
	if serverDigest != req.Digest {
		httpx.WriteError(w, fmt.Errorf("%w: digest mismatch: client sent %s, server computed %s",
			httpx.ErrBadRequest, req.Digest, serverDigest))
		return
	}

	// Server-verify each referenced blob: present + stored bytes hash == key, and
	// record the SERVER-OBSERVED byte length. Verify once per unique sha (a blob
	// may back multiple paths). size_bytes is summed from the stored objects, never
	// the client-claimed f.Size (FIX 3) — and a client size that disagrees with the
	// stored object is rejected so the manifest can't lie about a file's size.
	sizeBySHA := make(map[string]int64, len(req.Manifest))
	files := make(map[string]manifestTarget, len(req.Manifest))
	for _, f := range req.Manifest {
		if !validSHA256(f.SHA256) {
			httpx.WriteError(w, fmt.Errorf("%w: bad sha256 %q", httpx.ErrBadRequest, f.SHA256))
			return
		}
		observed, ok := sizeBySHA[f.SHA256]
		if !ok {
			n, err := a.verifyBlob(r, t.OrgID, f.SHA256)
			if err != nil {
				httpx.WriteError(w, err)
				return
			}
			observed = n
			sizeBySHA[f.SHA256] = n
		}
		// Reject a client-claimed size that disagrees with the stored object.
		if f.Size != observed {
			httpx.WriteError(w, fmt.Errorf("%w: file %q claims size %d but stored blob %s is %d bytes",
				httpx.ErrBadRequest, f.Path, f.Size, f.SHA256, observed))
			return
		}
		// The manifest records the server-observed size, not the client's claim.
		files[f.Path] = manifestTarget{SHA256: f.SHA256, ContentType: f.ContentType, Size: observed}
	}

	// Total size from server-observed blob lengths (one count per unique blob).
	var totalSize int64
	for _, n := range sizeBySHA {
		totalSize += n
	}

	// The distinct content-addressed blobs (+ server-observed sizes) for the per-org
	// storage meter; dedup-aware accounting + the cap happen in
	// the store tx. sizeBySHA is already keyed by unique sha.
	blobs := make([]store.BlobSize, 0, len(sizeBySHA))
	for sha, n := range sizeBySHA {
		blobs = append(blobs, store.BlobSize{SHA: sha, Size: n})
	}

	// Insert the immutable version (idempotent on per-site content_hash). The
	// content_hash is the SERVER-computed digest, never the client's claim.
	ver, err := a.Store.CreateSiteVersion(r.Context(), t, store.CreateSiteVersionParams{
		SiteID:      siteID,
		ContentHash: serverDigest,
		SizeBytes:   totalSize,
		Status:      "ready",
		Blobs:       blobs,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Write the immutable per-deploy manifest at manifests/<org>/<site>/<ver>.json.
	// The manifest's schema_version is the MANIFEST contract (manifest.SchemaVersion,
	// pinned to the Worker's SUPPORTED_MANIFEST_SCHEMA_VERSION) — NOT the KV route
	// contract (projection.SchemaVersion), which versions independently. Sourcing it
	// from projection.SchemaVersion previously made every deploy's manifest unreadable
	// after the route contract bumped to v2 (the Worker rejects it → 404).
	mani := storedManifest{SchemaVersion: manifest.SchemaVersion, Files: files}
	body, err := json.Marshal(mani)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := a.Objects.PutManifest(r.Context(), t.OrgID, siteID, ver.ID, body); err != nil {
		httpx.WriteError(w, err)
		return
	}

	logger(r).Info("deployment finalized",
		"site_id", siteID, "version_id", ver.ID, "version_no", ver.VersionNo,
		"org_id", t.OrgID, "size_bytes", totalSize)
	a.recordAudit(r, t, audit.ActionDeployFinalize, "site:"+siteID, map[string]any{
		"version_id": ver.ID,
		"version_no": ver.VersionNo,
		"size_bytes": totalSize,
	})

	// Preview URL enforces the SAME access tier as the live site. It is the
	// canonical org-namespaced host with the per-version short id PREPENDED as a
	// further single label: <shortid>--<orgSlug>--<appSlug>.<ContentDomain>, rendered
	// with the configured scheme/port.
	orgSlug, err := a.Store.OrgSlug(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	previewHost := shortID(ver.ID) + "--" + projection.HostForSite(orgSlug, site.Slug)
	httpx.WriteJSON(w, http.StatusCreated, finalizeResponse{
		VersionID:  ver.ID,
		VersionNo:  ver.VersionNo,
		PreviewURL: a.ContentURL(previewHost),
	})
}

// verifyBlob streams the stored blob, asserts its bytes hash == the key, and
// returns the SERVER-OBSERVED byte length. A hash mismatch is a 400 (the client
// lied about the content) — never trust the request-body hash without re-deriving
// it from the stored bytes. The returned size is the authoritative
// size_bytes source (FIX 3), so size metering never trusts the client manifest.
func (a *API) verifyBlob(r *http.Request, orgID, sha string) (int64, error) {
	rc, err := a.Objects.GetBlob(r.Context(), orgID, sha)
	if err != nil {
		if err == storage.ErrNotFound {
			return 0, fmt.Errorf("%w: blob %s was not uploaded", httpx.ErrBadRequest, sha)
		}
		return 0, err
	}
	defer rc.Close()
	h := sha256.New()
	n, err := io.Copy(h, rc)
	if err != nil {
		return 0, err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != sha {
		return 0, fmt.Errorf("%w: blob %s bytes hash to %s", httpx.ErrBadRequest, sha, got)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// POST /v1/sites/{id}/publish
// ---------------------------------------------------------------------------

type publishRequest struct {
	VersionID string `json:"version_id"`
}

type publishResponse struct {
	LiveURL   string `json:"live_url"`
	VersionID string `json:"version_id"`
}

// Publish flips the site's live-version pointer to version_id (publish OR
// rollback — rollback is publishing an older version) and writes the edge route
// projection. The pointer flip is Postgres-authoritative; the projection write
// follows and is reconcilable, so a transient KV failure never corrupts the DB.
func (a *API) Publish(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireProjection(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	var req publishRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.VersionID == "" {
		httpx.WriteError(w, fmt.Errorf("%w: version_id is required", httpx.ErrBadRequest))
		return
	}

	res, err := a.Store.Publish(r.Context(), t, siteID, req.VersionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Project the route to the edge AFTER the authoritative pointer flip committed.
	// Rewrite EVERY host of the site (canonical + verified custom domains) so a
	// custom domain never keeps serving the OLD version after a publish/rollback
	// (H3). res.Routes always includes the canonical host; fall back to the single
	// Host/Route pair only if a caller didn't populate Routes.
	routeUpdates := res.Routes
	if len(routeUpdates) == 0 {
		routeUpdates = []store.RouteUpdate{{Host: res.Host, Route: res.Route}}
	}
	for _, ru := range routeUpdates {
		if err := a.Projection.PutRoute(r.Context(), ru.Host, ru.Route); err != nil {
			// The DB is already authoritative; surface the projection failure so the
			// caller can retry, but log it loudly — the reconciler/rebuild backstops it.
			logger(r).Error("projection write failed after publish",
				"host", ru.Host, "site_id", siteID, "version_id", req.VersionID, "err", err)
			httpx.WriteError(w, err)
			return
		}
	}

	logger(r).Info("published",
		"site_id", siteID, "version_id", req.VersionID, "host", res.Host, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionDeployPublish, "site:"+siteID, map[string]any{
		"version_id": req.VersionID,
		"host":       res.Host,
	})
	httpx.WriteJSON(w, http.StatusOK, publishResponse{
		LiveURL:   a.ContentURL(res.Host),
		VersionID: req.VersionID,
	})
}

// ---------------------------------------------------------------------------
// dependency guards + small helpers
// ---------------------------------------------------------------------------

func (a *API) requireObjects(w http.ResponseWriter) bool {
	if a.Objects == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "object storage not configured"})
		return false
	}
	return true
}

func (a *API) requireProjection(w http.ResponseWriter) bool {
	if a.Projection == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "projection writer not configured"})
		return false
	}
	return true
}

// validSHA256 reports whether s is a 64-char lowercase hex string.
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// shortID returns a short, URL-safe prefix of an id for the preview host label.
func shortID(id string) string {
	const n = 8
	stripped := ""
	for _, c := range id {
		if c != '-' {
			stripped += string(c)
		}
		if len(stripped) == n {
			break
		}
	}
	if stripped == "" {
		return "preview"
	}
	return stripped
}
