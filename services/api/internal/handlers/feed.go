// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
)

// Feed-metadata length bounds (defensive caps; the dashboard also limits input).
const (
	maxFeedTitleLen       = 120
	maxFeedDescriptionLen = 500
)

// ---------------------------------------------------------------------------
// GET /v1/feed — the org feed
// ---------------------------------------------------------------------------

// ListFeed returns the active org's feed: every site any member has shared (i.e.
// not marked private), newest first so freshly created/published sites sit at the
// top and older ones sink to the bottom. Any org member may read it (RLS scopes
// the rows to their org); it's the cross-user discovery surface that complements
// the per-user dashboard list. A site joins the feed automatically on create /
// publish and leaves it only when its owner (or an admin) marks it private.
//
// The response reuses the standard siteResponse shape (owner_id, access_mode,
// live_url, created_at, …) so the dashboard can render each feed item exactly
// like a site card and attribute it to its owner.
func (a *API) ListFeed(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}

	sites, err := a.Store.ListFeedSites(r.Context(), t)
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
	// each feed item carries its size without an N+1 — same approach as ListSites.
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

// ---------------------------------------------------------------------------
// PUT /v1/sites/{id}/feed  {visible}
// ---------------------------------------------------------------------------

type setFeedVisibilityRequest struct {
	// Visible shares the site to the org feed (true) or makes it private (false).
	Visible bool `json:"visible"`
}

// SetSiteFeedVisibility shares a site to the org feed or makes it private. Unlike
// the access endpoints (admin/owner only), a site's OWNER may toggle their own
// site's feed visibility — it's their site to share — and admins/owners may
// toggle any site in the org. Feed visibility is the discovery axis, orthogonal
// to access_mode, so this changes nothing at the edge (no projection rewrite, no
// token revocation): a private site keeps serving under its existing access mode,
// it's just hidden from the feed listing.
func (a *API) SetSiteFeedVisibility(w http.ResponseWriter, r *http.Request) {
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
	// owner so a non-owner member is held to the admin gate.
	site, err := a.Store.GetSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Owner-or-admin: the site owner manages their own feed sharing; everyone else
	// must be an org admin/owner (requireAdmin writes the 403 on failure).
	if site.OwnerUserID != t.UserID && !a.requireAdmin(w, r, t) {
		return
	}

	var req setFeedVisibilityRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}

	updated, err := a.Store.SetSiteFeedVisible(r.Context(), t, siteID, req.Visible)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	logger(r).Info("site feed visibility changed",
		"site_id", siteID, "visible", updated.FeedVisible, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSiteFeedVisibility, "site:"+siteID, map[string]any{
		"visible": updated.FeedVisible,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site_id":      siteID,
		"feed_visible": updated.FeedVisible,
	})
}

// ---------------------------------------------------------------------------
// PUT /v1/sites/{id}/feed-meta  {title, description}
// ---------------------------------------------------------------------------

type setFeedMetaRequest struct {
	// Title / Description are the human feed metadata. Empty clears the field.
	Title       string `json:"title"`
	Description string `json:"description"`
}

// SetSiteFeedMeta sets the owner-facing Title + Description a site shows in the
// org feed. Authorized for the site's OWNER or an org admin/owner (same gate as
// the feed-visibility toggle — it's the owner's site to describe). Empty strings
// clear the corresponding field (stored as NULL).
func (a *API) SetSiteFeedMeta(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	site, err := a.Store.GetSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if site.OwnerUserID != t.UserID && !a.requireAdmin(w, r, t) {
		return
	}

	var req setFeedMetaRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	title := strings.TrimSpace(req.Title)
	description := strings.TrimSpace(req.Description)
	if len(title) > maxFeedTitleLen {
		httpx.WriteError(w, fmt.Errorf("%w: title must be at most %d characters", httpx.ErrBadRequest, maxFeedTitleLen))
		return
	}
	if len(description) > maxFeedDescriptionLen {
		httpx.WriteError(w, fmt.Errorf("%w: description must be at most %d characters", httpx.ErrBadRequest, maxFeedDescriptionLen))
		return
	}

	updated, err := a.Store.SetSiteFeedMeta(r.Context(), t, siteID, title, description)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	logger(r).Info("site feed metadata changed", "site_id", siteID, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSiteFeedMeta, "site:"+siteID, map[string]any{
		"title_set":       updated.Title != "",
		"description_set": updated.Description != "",
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site_id":     siteID,
		"title":       updated.Title,
		"description": updated.Description,
	})
}
