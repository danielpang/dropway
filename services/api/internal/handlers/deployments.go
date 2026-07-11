// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

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

// storedManifest / manifestTarget are the immutable per-deploy manifest written
// to R2 at manifests/<org>/<site>/<version>.json (serving resolves request-path →
// sha256 via Files, then streams blobs/<org>/<sha256>). They are ALIASES of the
// canonical shape in internal/manifest so the deploy finalize path, the skill
// upload path, and the AI-draft ingest path all marshal the exact same type —
// a field/tag change can never drift one writer from the others.
type storedManifest = manifest.Stored
type manifestTarget = manifest.Target

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

	resp, err := a.prepareMissingBlobs(r, t.OrgID, req.Manifest)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	logger(r).Info("deployment prepared",
		"site_id", siteID, "org_id", t.OrgID,
		"files", len(req.Manifest), "missing", len(resp.Missing))
	httpx.WriteJSON(w, http.StatusOK, resp)
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
	// PreviewExpiresAt is the RFC3339 deadline of the preview host (default 7
	// days; renewable via POST /v1/sites/{id}/versions/{versionID}/preview).
	PreviewExpiresAt string `json:"preview_expires_at,omitempty"`
	// Warnings are non-fatal advisories about the finalized deploy (e.g. a missing
	// root index.html). The deploy still succeeds; clients (dashboard, MCP, CLI)
	// surface these so a publishable-but-likely-broken upload doesn't 404 silently.
	Warnings []string `json:"warnings,omitempty"`
}

// rootIndexFile is the manifest key the serving Worker resolves the root URL ("/")
// to — exactly this, lowercase, at the upload root (see edge/serving-worker
// resolveManifestEntry / candidatePaths). Without it the root has no page, so the
// server falls back to an autoindex (a browsable file listing).
const rootIndexFile = "index.html"

// deployWarnings returns non-fatal advisories about a finalized deploy's file set.
// Today it warns when there is no root index.html: the Worker resolves "/" to
// exactly that key, so its absence means the root URL shows a generated file
// listing instead of a rendered page. It is a WARNING, not an error — a site may
// intentionally publish a folder of files to browse — but a missing root
// index.html is by far the most common cause of a "my site looks wrong" report
// (usually an upload nested one folder too deep), so we surface it instead of
// letting it pass silently.
func deployWarnings(files map[string]manifestTarget) []string {
	var warnings []string
	if _, ok := files[rootIndexFile]; !ok {
		warnings = append(warnings, "No index.html at the site root, so the root URL (/) shows a file listing "+
			"instead of a web page. If you uploaded a folder that wraps your site (e.g. its files are under a "+
			"subfolder), deploy the inner folder instead, or rename your entry page to index.html.")
	}
	return warnings
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

	// Confused-deputy guard: the site must belong to the active tenant.
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

	// Server-verify the manifest: recompute the digest (FIX 2 — the client digest
	// is never trusted as the content_hash idempotency key) and re-hash every blob
	// with server-observed sizes (FIX 3). Shared verbatim with skill uploads.
	vm, err := a.verifyManifest(r, t.OrgID, req.Manifest, req.Digest)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	files, totalSize := vm.Files, vm.TotalSize

	// Insert the immutable version (idempotent on per-site content_hash). The
	// content_hash is the SERVER-computed digest, never the client's claim.
	ver, err := a.Store.CreateSiteVersion(r.Context(), t, store.CreateSiteVersionParams{
		SiteID:      siteID,
		ContentHash: vm.Digest,
		SizeBytes:   totalSize,
		Status:      "ready",
		Blobs:       vm.Blobs,
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
	//
	// The preview host is DETERMINISTIC (org slug + site slug + version id), so we
	// ALWAYS compute and return it — a deploy that just returned 201 must never come
	// back without a preview link (that was the pre-preview contract). Registering
	// the host_routes row and projecting it to the edge are BEST-EFFORT: the version
	// + manifest are already committed, so neither failure fails the deploy. When
	// the row succeeds we use its authoritative host + expiry; when it (or the KV
	// write) fails the URL is briefly unresolvable until the reconcile/rebuild
	// backstop runs or the caller re-requests a preview — the same eventual
	// consistency as publish's live_url.
	resp := finalizeResponse{
		VersionID: ver.ID,
		VersionNo: ver.VersionNo,
		Warnings:  deployWarnings(files),
	}
	orgSlug, slugErr := a.Store.OrgSlug(r.Context(), t)
	if slugErr == nil {
		resp.PreviewURL = a.ContentURL(projection.PreviewHostForSite(ver.ID, orgSlug, site.Slug))
		resp.PreviewExpiresAt = time.Now().UTC().Add(a.previewTTL()).Format(time.RFC3339)
	}
	if prev, err := a.Store.CreatePreviewRoute(r.Context(), t, siteID, ver.ID, a.previewTTL()); err != nil {
		logger(r).Error("preview route registration failed after finalize (deploy still succeeded)",
			"site_id", siteID, "version_id", ver.ID, "err", err)
	} else {
		// Authoritative host + expiry from the committed row.
		resp.PreviewURL = a.ContentURL(prev.Host)
		resp.PreviewExpiresAt = prev.ExpiresAt.UTC().Format(time.RFC3339)
		if a.Projection != nil {
			if err := a.Projection.PutRoute(r.Context(), prev.Host, prev.Route); err != nil {
				logger(r).Error("preview projection write failed after finalize",
					"host", prev.Host, "site_id", siteID, "version_id", ver.ID, "err", err)
			}
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, resp)
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

	// Publishing deletes the published version's preview: the store already
	// removed the rows in the publish tx; drop the KV keys too. Best-effort — a
	// missed delete only means the (now published) draft stays reachable on its
	// preview host until the deadline; the ops sweep cleans stragglers.
	for _, host := range res.DeletedPreviewHosts {
		if err := a.Projection.DeleteRoute(r.Context(), host); err != nil {
			logger(r).Error("preview route delete failed after publish",
				"host", host, "site_id", siteID, "version_id", req.VersionID, "err", err)
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
