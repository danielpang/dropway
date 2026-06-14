// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// siteResponse is the API representation of a site.
type siteResponse struct {
	ID               string    `json:"id"`
	OrgID            string    `json:"org_id"`
	Slug             string    `json:"slug"`
	OwnerID          string    `json:"owner_id"`
	AccessMode       string    `json:"access_mode"`
	CurrentVersionID *string   `json:"current_version_id,omitempty"`
	LiveURL          string    `json:"live_url"`
	CreatedAt        time.Time `json:"created_at"`
}

func toSiteResponse(s store.Site) siteResponse {
	return siteResponse{
		ID:               s.ID,
		OrgID:            s.OrgID,
		Slug:             s.Slug,
		OwnerID:          s.OwnerUserID,
		AccessMode:       s.AccessMode,
		CurrentVersionID: s.CurrentVersionID,
		LiveURL:          "https://" + projection.HostForSlug(s.Slug),
		CreatedAt:        s.CreatedAt,
	}
}

// createSiteRequest is the POST /v1/sites body.
type createSiteRequest struct {
	Slug string `json:"slug"`
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
	// Reject reserved slugs early with a 400 (before spending a quota reservation).
	if store.IsReservedSlug(req.Slug) {
		httpx.WriteError(w, fmt.Errorf("%w: slug %q is reserved", httpx.ErrBadRequest, req.Slug))
		return
	}

	// Synchronous hard-cap check at the cost-creating action (§9). OSS: Unlimited
	// → nil. Cloud: real per-user site cap → may 402 with the upgrade payload.
	if err := a.Quota.CheckAndReserve(
		r.Context(), t.OrgID, t.UserID, quota.ResourceSitePerUser,
	); err != nil {
		httpx.WriteError(w, err)
		return
	}

	site, err := a.Store.CreateSite(r.Context(), t, req.Slug, projection.AccessPublic)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("site created", "site_id", site.ID, "slug", site.Slug, "org_id", t.OrgID)
	httpx.WriteJSON(w, http.StatusCreated, toSiteResponse(site))
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
	out := make([]siteResponse, len(sites))
	for i, s := range sites {
		out[i] = toSiteResponse(s)
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
	httpx.WriteJSON(w, http.StatusOK, toSiteResponse(site))
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
	switch {
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
		// another org/site — a cross-tenant collision (§6). 409 Conflict, not 400:
		// the request is well-formed, the resource just isn't available.
		httpx.WriteError(w, fmt.Errorf("%w: site slug/host already in use", httpx.ErrConflict))
	case errors.Is(err, store.ErrExternalSharingDisabled):
		// The org's allow_external_sharing policy forbids a public site (§5.4).
		httpx.WriteError(w, fmt.Errorf("%w: external sharing is disabled for this org; an admin must enable it", httpx.ErrForbidden))
	default:
		httpx.WriteError(w, err) // unknown → opaque 500 (logged by httpx)
	}
}
