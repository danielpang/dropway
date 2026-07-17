// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// siteResponse is the API representation of a site. StorageBytes is the site's
// LOGICAL storage (its current live version's size; 0 before the first deploy) —
// NOT deduplicated across sites, so it's the "how big is this site on its own"
// number, like a Dropbox/Drive folder size.
type siteResponse struct {
	ID               string  `json:"id"`
	OrgID            string  `json:"org_id"`
	Slug             string  `json:"slug"`
	OwnerID          string  `json:"owner_id"`
	AccessMode       string  `json:"access_mode"`
	CurrentVersionID *string `json:"current_version_id,omitempty"`
	LiveURL          string  `json:"live_url"`
	StorageBytes     int64   `json:"storage_bytes"`
	// FeedVisible is the org-feed discovery flag: true (default) shares the site to
	// teammates' feed; false keeps it private (off the feed). Orthogonal to access.
	FeedVisible bool `json:"feed_visible"`
	// Title / Description are the owner-set human feed metadata (empty when unset;
	// the feed UI falls back to the slug for the title).
	Title       string `json:"title"`
	Description string `json:"description"`
	// AllowMemberEdits is the collaboration toggle (default true): false
	// restricts content edits (deploy/publish/previews) to creator-or-admin.
	AllowMemberEdits bool      `json:"allow_member_edits"`
	CreatedAt        time.Time `json:"created_at"`
}

// toSiteResponse renders a site for the API. orgSlug is the org half of the
// canonical content host (projection.HostForSite); the display LiveURL is built
// from the configured scheme/port (API.ContentURL). All sites in one request
// share the active tenant's org, so the caller resolves orgSlug once. storageBytes
// is the site's logical size (0 for a just-created site with no live version).
func (a *API) toSiteResponse(s store.Site, orgSlug string, storageBytes int64) siteResponse {
	return siteResponse{
		ID:               s.ID,
		OrgID:            s.OrgID,
		Slug:             s.Slug,
		OwnerID:          s.OwnerUserID,
		AccessMode:       s.AccessMode,
		CurrentVersionID: s.CurrentVersionID,
		LiveURL:          a.ContentURL(projection.HostForSite(orgSlug, s.Slug)),
		StorageBytes:     storageBytes,
		FeedVisible:      s.FeedVisible,
		Title:            s.Title,
		Description:      s.Description,
		AllowMemberEdits: s.AllowMemberEdits,
		CreatedAt:        s.CreatedAt,
	}
}

// createSiteRequest is the POST /v1/sites body. access_mode is optional: omit it
// to inherit the org's default_visibility (org_only for a fresh org). Only the
// no-extra-config modes are accepted at create time; password/allowlist are
// configured afterward via PUT /v1/sites/{id}/access (they need a password / entries).
type createSiteRequest struct {
	Slug       string `json:"slug"`
	AccessMode string `json:"access_mode,omitempty"`
}

// CreateSite reserves quota for one more site for the caller and inserts it under
// the RLS tenant context. Reserved slugs → 400; quota cap → 402 (the ExceededError
// body); duplicate slug → 400.
func (a *API) CreateSite(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}

	var req createSiteRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.Slug == "" {
		httpx.WriteError(w, fmt.Errorf("%w: slug is required", httpx.ErrBadRequest))
		return
	}
	// Reject malformed slugs early with a 400 (before spending a quota reservation).
	// The slug becomes a DNS label in the canonical content host and part of the
	// Cloudflare KV route-key path, so it must be a single safe lowercase label.
	// The CLI and MCP reach this handler directly (only the dashboard slugifies
	// client-side), so this server-side check is the real boundary.
	if !store.ValidSlug(req.Slug) {
		httpx.WriteError(w, fmt.Errorf("%w: slug must be 1-63 chars, lowercase letters/digits/hyphens, no leading/trailing or doubled hyphens", httpx.ErrBadRequest))
		return
	}
	// Reject reserved slugs early with a 400 (before spending a quota reservation).
	if store.IsReservedSlug(req.Slug) {
		httpx.WriteError(w, fmt.Errorf("%w: slug %q is reserved", httpx.ErrBadRequest, req.Slug))
		return
	}
	// Only the no-extra-config modes are valid at create. "" → the store inherits
	// the org's default_visibility (org_only for a fresh org). password/allowlist
	// require a follow-up PUT /v1/sites/{id}/access to set the password / entries.
	switch req.AccessMode {
	case "", projection.AccessPublic, projection.AccessOrgOnly:
		// ok
	case projection.AccessPassword, projection.AccessAllowlist:
		httpx.WriteError(w, fmt.Errorf("%w: set access_mode %q via PUT /v1/sites/{id}/access (it needs a password/allowlist)", httpx.ErrBadRequest, req.AccessMode))
		return
	default:
		httpx.WriteError(w, fmt.Errorf("%w: invalid access_mode %q", httpx.ErrBadRequest, req.AccessMode))
		return
	}

	// The hard-cap check happens INSIDE store.CreateSite's tx (advisory lock
	// + COUNT → quota.Provider.Allow → INSERT), so it's race-safe. OSS = Unlimited;
	// cloud returns a *quota.ExceededError that writeStoreError renders as 402.
	// access_mode "" → store inherits the org's default_visibility (org_only).
	site, err := a.Store.CreateSite(r.Context(), t, req.Slug, req.AccessMode)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	orgSlug, err := a.Store.OrgSlug(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("site created", "site_id", site.ID, "slug", site.Slug, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSiteCreate, "site:"+site.ID, map[string]any{
		"slug":        site.Slug,
		"access_mode": site.AccessMode,
	})
	// Best-effort product analytics: a new site was created. Attributed to the
	// acting user (DistinctID) and rolled up per org via group analytics + an
	// org_id property, so the "new sites created" dashboard can break down by org.
	// Capture is non-blocking and never errors; a nil emitter (tests / no key) is a
	// no-op.
	if a.Analytics != nil {
		a.Analytics.Capture(r.Context(), analytics.Event{
			DistinctID: t.UserID,
			Event:      "site_created",
			Properties: map[string]any{
				"org_id":      t.OrgID,
				"site_id":     site.ID,
				"slug":        site.Slug,
				"access_mode": site.AccessMode,
			},
			Groups: map[string]string{"organization": t.OrgID},
		})
	}
	// A just-created site has no live version yet → 0 logical bytes.
	httpx.WriteJSON(w, http.StatusCreated, a.toSiteResponse(site, orgSlug, 0))
}

// ListSites returns the caller org's sites.
func (a *API) ListSites(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	sites, err := a.Store.ListSites(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	orgSlug, err := a.Store.OrgSlug(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Logical storage per site (one query for the whole org), keyed by site id so
	// each response carries its size without an N+1.
	storage, err := a.Store.ListSiteStorage(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	bytesBySite := make(map[string]int64, len(storage))
	for _, s := range storage {
		bytesBySite[s.SiteID] = s.Bytes
	}
	out := make([]siteResponse, len(sites))
	for i, s := range sites {
		out[i] = a.toSiteResponse(s, orgSlug, bytesBySite[s.ID])
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sites": out})
}

// GetSite returns one site by id (404 if absent or another tenant's).
func (a *API) GetSite(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	id := chi.URLParam(r, "id")
	site, err := a.Store.GetSite(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	orgSlug, err := a.Store.OrgSlug(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	bytes, err := a.Store.SiteStorageBytes(r.Context(), t, site.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a.toSiteResponse(site, orgSlug, bytes))
}

// versionResponse is one row of a site's deploy history.
type versionResponse struct {
	ID          string    `json:"id"`
	VersionNo   int32     `json:"version_no"`
	Status      string    `json:"status"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentHash string    `json:"content_hash"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	// IsCurrent marks the version the site is currently serving (the live one).
	IsCurrent bool `json:"is_current"`
}

// ListVersions returns a site's deploy history (newest first), each flagged with
// whether it is the live version, so the dashboard can offer a one-click rollback
// instead of asking for a version id.
func (a *API) ListVersions(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	// Resolve the site first: 404 for an absent/other-tenant site, and to read the
	// current live version so each row can be flagged is_current.
	site, err := a.Store.GetSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	versions, err := a.Store.ListSiteVersions(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]versionResponse, len(versions))
	for i, v := range versions {
		out[i] = versionResponse{
			ID:          v.ID,
			VersionNo:   v.VersionNo,
			Status:      v.Status,
			SizeBytes:   v.SizeBytes,
			ContentHash: v.ContentHash,
			CreatedBy:   v.CreatedBy,
			CreatedAt:   v.CreatedAt,
			IsCurrent:   site.CurrentVersionID != nil && *site.CurrentVersionID == v.ID,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"versions": out})
}

// decodeJSON strictly decodes the request body into v (unknown fields rejected),
// bounding the body so a hostile client can't OOM the server.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20)) // 8 MiB cap
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// writeStoreError maps store sentinels to the right HTTP status via httpx.
func writeStoreError(w http.ResponseWriter, err error) {
	// A quota cap (cloud build) surfaces from the store as *quota.ExceededError →
	// HTTP 402 with the rich upgrade body (httpx renders ExceededError natively).
	if _, ok := quota.AsExceeded(err); ok {
		httpx.WriteError(w, err)
		return
	}
	switch {
	case errors.Is(err, middleware.ErrMissingTenant), errors.Is(err, store.ErrMissingViewer):
		// The verified JWT carries no user/org (a session minted before the org
		// existed). The store fails closed; to the client this is a credential
		// problem, so answer 401 and let the dashboard re-authenticate instead of
		// surfacing an opaque 500.
		httpx.WriteError(w, fmt.Errorf("%w: session has no active organization, please sign in again", httpx.ErrUnauthorized))
	case errors.Is(err, store.ErrInvalidSlug):
		httpx.WriteError(w, fmt.Errorf("%w: slug is not a valid single DNS label", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrReservedSlug):
		httpx.WriteError(w, fmt.Errorf("%w: slug is reserved", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrSlugTaken):
		httpx.WriteError(w, fmt.Errorf("%w: slug already in use", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrNotFound):
		httpx.WriteError(w, fmt.Errorf("%w: not found", httpx.ErrNotFound))
	case errors.Is(err, store.ErrVersionMismatch):
		httpx.WriteError(w, fmt.Errorf("%w: version does not belong to site", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrHostTaken):
		// The global host (slug under the content domain) is already owned by
		// another org/site — a cross-tenant collision. 409 Conflict, not 400:
		// the request is well-formed, the resource just isn't available.
		httpx.WriteError(w, fmt.Errorf("%w: site slug/host already in use", httpx.ErrConflict))
	case errors.Is(err, store.ErrSiteHasChatLog):
		// One attached chat log per site: detach/move the existing one first.
		httpx.WriteError(w, fmt.Errorf("%w: site already has an attached chat log", httpx.ErrConflict))
	case errors.Is(err, store.ErrExternalSharingDisabled):
		// The org's allow_external_sharing policy forbids a public site.
		httpx.WriteError(w, fmt.Errorf("%w: external sharing is disabled for this org; an admin must enable it", httpx.ErrForbidden))
	case errors.Is(err, store.ErrInvalidMode):
		httpx.WriteError(w, fmt.Errorf("%w: invalid access mode", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrInvalidDomainStatus):
		httpx.WriteError(w, fmt.Errorf("%w: invalid domain status", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrBadEmail):
		httpx.WriteError(w, fmt.Errorf("%w: invalid email", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrBadHostname):
		httpx.WriteError(w, fmt.Errorf("%w: invalid hostname", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrNoPolicy):
		httpx.WriteError(w, fmt.Errorf("%w: site has no access policy", httpx.ErrNotFound))
	case errors.Is(err, store.ErrPolicyExpired):
		// The share link has expired → refuse to mint ("link expired").
		httpx.WriteError(w, fmt.Errorf("%w: this share link has expired", httpx.ErrForbidden))
	case errors.Is(err, store.ErrHostNotFound):
		httpx.WriteError(w, fmt.Errorf("%w: host not found", httpx.ErrNotFound))
	case errors.Is(err, store.ErrOrgSlugNotFound):
		// The org has no identity.organization row, so the canonical content host can't
		// be formed. This is a provisioning gap, not a client error → opaque 500.
		httpx.WriteError(w, fmt.Errorf("org is not fully provisioned (missing organization slug): %w", err))
	case errors.Is(err, store.ErrNotOrgMember), errors.Is(err, store.ErrNotAllowlisted):
		// Authorization failed for the gated site → 403 with a typed reason.
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrForbidden, err.Error()))
	case errors.Is(err, store.ErrNotGated):
		httpx.WriteError(w, fmt.Errorf("%w: this site is not gated", httpx.ErrBadRequest))
	case errors.Is(err, store.ErrFolderNotFound):
		httpx.WriteError(w, fmt.Errorf("%w: skill folder not found", httpx.ErrNotFound))
	default:
		httpx.WriteError(w, err) // unknown → opaque 500 (logged by httpx)
	}
}
