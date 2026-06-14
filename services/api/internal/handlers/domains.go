// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/shipped/internal/customdomains"
	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/services/api/internal/store"
)

type domainResponse struct {
	ID           string `json:"id"`
	SiteID       string `json:"site_id"`
	Hostname     string `json:"hostname"`
	VerifyStatus string `json:"verify_status"`
	TLSStatus    string `json:"tls_status"`
	DCVRecord    string `json:"dcv_record,omitempty"`
}

func toDomainResponse(d store.Domain) domainResponse {
	return domainResponse{
		ID:           d.ID,
		SiteID:       d.SiteID,
		Hostname:     d.Hostname,
		VerifyStatus: d.VerifyStatus,
		TLSStatus:    d.TLSStatus,
		DCVRecord:    d.DCVRecord,
	}
}

type addDomainRequest struct {
	Hostname string `json:"hostname"`
}

// AddDomain registers a custom hostname for a site (ADMIN/OWNER only): it creates
// the Cloudflare-for-SaaS custom hostname, stores a pending app.domains row with
// the returned hostname id + DNS DCV record, and returns the row (verify_status=
// pending, dcv_record to surface to the user). hostname is GLOBALLY UNIQUE → 409
// if taken by another org/site.
func (a *API) AddDomain(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireDomains(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	siteID := chi.URLParam(r, "id")

	var req addDomainRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	hostname := strings.ToLower(strings.TrimSpace(req.Hostname))
	if !looksLikeHostname(hostname) {
		httpx.WriteError(w, fmt.Errorf("%w: a valid hostname is required", httpx.ErrBadRequest))
		return
	}

	// Confirm site ownership BEFORE calling Cloudflare (don't create a CF hostname
	// for a site the caller can't see).
	if _, err := a.Store.GetSite(r.Context(), t, siteID); err != nil {
		writeStoreError(w, err)
		return
	}

	created, err := a.Domains.CreateCustomHostname(r.Context(), hostname)
	if err != nil {
		logger(r).Error("cloudflare create custom hostname failed", "hostname", hostname, "err", err)
		httpx.WriteError(w, fmt.Errorf("%w: could not register custom hostname", httpx.ErrBadRequest))
		return
	}

	domain, err := a.Store.CreateDomain(r.Context(), t, store.CreateDomainParams{
		SiteID:       siteID,
		Hostname:     hostname,
		CFHostnameID: created.ID,
		DCVRecord:    created.DCV.String(),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("custom domain registered", "site_id", siteID, "hostname", hostname, "cf_id", created.ID)
	httpx.WriteJSON(w, http.StatusCreated, toDomainResponse(domain))
}

// ListDomains returns a site's custom domains.
func (a *API) ListDomains(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")
	domains, err := a.Store.ListDomainsForSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]domainResponse, len(domains))
	for i, d := range domains {
		out[i] = toDomainResponse(d)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"domains": out})
}

// GetDomainStatus polls Cloudflare for the custom hostname's verification + TLS
// state, advances the app.domains state machine, and — on verified+TLS — writes the
// global host route → site so the custom host serves (asserting global uniqueness
// → 409 if taken). Returns the updated domain row.
func (a *API) GetDomainStatus(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireDomains(w) {
		return
	}
	domainID := chi.URLParam(r, "domainID")

	domain, err := a.Store.GetDomain(r.Context(), t, domainID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if domain.CFHostnameID == "" {
		// Nothing to poll (no CF hostname recorded) — return current state.
		httpx.WriteJSON(w, http.StatusOK, toDomainResponse(domain))
		return
	}

	status, err := a.Domains.Status(r.Context(), domain.CFHostnameID)
	if err != nil {
		logger(r).Error("cloudflare status poll failed", "cf_id", domain.CFHostnameID, "err", err)
		httpx.WriteError(w, fmt.Errorf("%w: could not poll custom hostname status", httpx.ErrBadRequest))
		return
	}

	verifyStatus, tlsStatus := mapDomainStatus(status)
	res, err := a.Store.UpdateDomainStatus(r.Context(), t, domainID, verifyStatus, tlsStatus)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// On verified, register the KV route so the custom host serves (only when the
	// site has a live version — Route.VersionID is set in that case).
	if res.Registered && res.Host != "" && res.Route.VersionID != "" && a.Projection != nil {
		if err := a.Projection.PutRoute(r.Context(), res.Host, res.Route); err != nil {
			logger(r).Error("projection write failed after domain verify", "host", res.Host, "err", err)
			httpx.WriteError(w, err)
			return
		}
		logger(r).Info("custom domain verified; route written", "host", res.Host, "site_id", res.Domain.SiteID)
	}

	httpx.WriteJSON(w, http.StatusOK, toDomainResponse(res.Domain))
}

// mapDomainStatus maps the provider VerifyState onto the app.domains
// (verify_status, tls_status) pair driving the pending→verifying→active machine.
func mapDomainStatus(s customdomains.StatusResult) (verify, tls string) {
	switch s.State {
	case customdomains.StateActive:
		verify = store.DomainVerified
	case customdomains.StateVerifying:
		verify = store.DomainVerifying
	case customdomains.StateFailed:
		verify = store.DomainFailed
	default:
		verify = store.DomainPending
	}
	if s.TLSIssued {
		tls = store.TLSIssued
	} else if s.State == customdomains.StateFailed {
		tls = store.TLSFailed
	} else {
		tls = store.TLSPending
	}
	return verify, tls
}

// looksLikeHostname is a minimal sanity check for a custom hostname.
func looksLikeHostname(s string) bool {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, " \t/:") {
		return false
	}
	return strings.IndexByte(s, '.') > 0
}
