// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Vanity platform subdomains: an optional bare `<slug>.<ContentDomain>` a site
// can claim (first come, first served, one per site). A vanity host is a single
// DNS label under the platform domain, so the existing wildcard cert and DNS
// cover it — unlike a custom domain there is nothing to provision or verify,
// which is why this path needs no Cloudflare-for-SaaS provider and works on
// self-host too (no requireDomains gate).

type vanityRequest struct {
	Slug string `json:"slug"`
}

type vanityResponse struct {
	VanityHost string `json:"vanity_host"`
	LiveURL    string `json:"live_url"`
}

// RegisterVanity claims `<slug>.<ContentDomain>` for a site (ADMIN/OWNER only,
// like custom domains). The label obeys the site-slug grammar + reserved-word
// blocklist; an unavailable label is 409 (taken globally) and a site that
// already holds one is 409 (release it first). When the site is live the route
// is projected immediately; otherwise the next publish projects it.
func (a *API) RegisterVanity(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireProjection(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	siteID := chi.URLParam(r, "id")

	var req vanityRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	label := strings.ToLower(strings.TrimSpace(req.Slug))
	if label == "" {
		httpx.WriteError(w, fmt.Errorf("%w: slug is required", httpx.ErrBadRequest))
		return
	}

	res, err := a.Store.RegisterVanityHost(r.Context(), t, siteID, label)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Live site: project the route now so the vanity host serves immediately
	// (mirrors the custom-domain verify path). The DB row is authoritative —
	// on projection failure surface the error; a retry or the next publish/
	// rebuild reconciles.
	if res.Registered {
		if err := a.Projection.PutRoute(r.Context(), res.Host, res.Route); err != nil {
			logger(r).Error("projection write failed after vanity claim", "host", res.Host, "err", err)
			httpx.WriteError(w, err)
			return
		}
	}

	logger(r).Info("vanity host claimed", "site_id", siteID, "host", res.Host, "live", res.Registered)
	a.recordAudit(r, t, audit.ActionVanityAdd, "site:"+siteID, map[string]any{
		"host": res.Host,
	})
	httpx.WriteJSON(w, http.StatusCreated, vanityResponse{
		VanityHost: res.Host,
		LiveURL:    a.ContentURL(res.Host),
	})
}

// ReleaseVanity removes a site's vanity host (ADMIN/OWNER only). The DB delete
// is authoritative; the edge route removal is best-effort (a rebuild derives
// from Postgres and won't resurrect it). 404 when the site has none. Returns
// 204; the freed label is immediately claimable again.
func (a *API) ReleaseVanity(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireProjection(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	siteID := chi.URLParam(r, "id")

	host, err := a.Store.ReleaseVanityHost(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := a.Projection.DeleteRoute(r.Context(), host); err != nil {
		logger(r).Error("vanity route delete failed", "host", host, "site_id", siteID, "err", err)
	}

	logger(r).Info("vanity host released", "site_id", siteID, "host", host)
	a.recordAudit(r, t, audit.ActionVanityRemove, "site:"+siteID, map[string]any{
		"host": host,
	})
	w.WriteHeader(http.StatusNoContent)
}
