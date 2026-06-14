// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/shipped/internal/audit"
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

	// The hard-cap check (§9) happens INSIDE store.CreateSite's tx (advisory lock
	// + COUNT → quota.Provider.Allow → INSERT), so it's race-safe. OSS = Unlimited;
	// cloud returns a *quota.ExceededError that writeStoreError renders as 402.
	site, err := a.Store.CreateSite(r.Context(), t, req.Slug, projection.AccessPublic)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("site created", "site_id", site.ID, "slug", site.Slug, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSiteCreate, "site:"+site.ID, map[string]any{
		"slug":        site.Slug,
		"access_mode": site.AccessMode,
	})
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
	// A quota cap (cloud build) surfaces from the store as *quota.ExceededError →
	// HTTP 402 with the rich upgrade body (httpx renders ExceededError natively).
	if _, ok := quota.AsExceeded(err); ok {
		httpx.WriteError(w, err)
		return
	}
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
	case errors.Is(err, store.ErrNotOrgMember), errors.Is(err, store.ErrNotAllowlisted):
		// Authorization failed for the gated site → 403 with a typed reason.
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrForbidden, err.Error()))
	case errors.Is(err, store.ErrNotGated):
		httpx.WriteError(w, fmt.Errorf("%w: this site is not gated", httpx.ErrBadRequest))
	default:
		httpx.WriteError(w, err) // unknown → opaque 500 (logged by httpx)
	}
}
