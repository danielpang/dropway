// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/customdomains"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/api/internal/store"
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
	a.recordAudit(r, t, audit.ActionDomainAdd, "domain:"+domain.ID, map[string]any{
		"site_id":  siteID,
		"hostname": hostname,
	})
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
		a.recordAudit(r, t, audit.ActionDomainVerify, "domain:"+res.Domain.ID, map[string]any{
			"site_id":  res.Domain.SiteID,
			"hostname": res.Host,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, toDomainResponse(res.Domain))
}

// DeleteDomain removes a custom domain (admin/owner only). It deletes the
// app.domains row + the global host route in one tx (so serve/edge stop resolving
// the host immediately), then best-effort removes the edge route projection and the
// Cloudflare custom hostname. The local removal is authoritative: a failure to clean
// up Cloudflare/KV is logged, not surfaced, since the host already no longer serves.
func (a *API) DeleteDomain(w http.ResponseWriter, r *http.Request) {
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
	domainID := chi.URLParam(r, "domainID")

	res, err := a.Store.DeleteDomain(r.Context(), t, domainID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Best-effort cleanup of the edge route projection (route:<host>) so the Worker
	// stops serving the custom host. The host_routes row is already gone (the DB is
	// authoritative); a projection error is logged, not fatal.
	if a.Projection != nil && res.Hostname != "" {
		if err := a.Projection.DeleteRoute(r.Context(), res.Hostname); err != nil {
			logger(r).Error("projection delete failed after domain remove", "host", res.Hostname, "err", err)
		}
	}
	// Best-effort delete of the Cloudflare custom hostname (idempotent provider-side).
	if res.CFHostnameID != "" {
		if err := a.Domains.DeleteCustomHostname(r.Context(), res.CFHostnameID); err != nil {
			logger(r).Error("cloudflare delete custom hostname failed", "cf_id", res.CFHostnameID, "err", err)
		}
	}

	logger(r).Info("custom domain removed", "domain_id", domainID, "hostname", res.Hostname)
	a.recordAudit(r, t, audit.ActionDomainRemove, "domain:"+domainID, map[string]any{
		"hostname": res.Hostname,
	})
	w.WriteHeader(http.StatusNoContent)
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

// looksLikeHostname is a conservative sanity check for a custom hostname. Beyond
// the basic shape (length, no whitespace/slash/colon, at least one dot), it blocks
// hosts a tenant must never be allowed to claim as a "custom domain":
//
//   - the platform content domain itself and any subdomain of it. Otherwise a
//     tenant could squat another tenant's future canonical host (e.g.
//     "victim--blog.dropwaycontent.com") before its host_routes row exists, so the
//     global unique constraint can't fire yet.
//   - bare IPv4/IPv6 literals (a custom domain must be a name, not an address).
//   - single-label hosts (no dot), which can't be a real custom domain.
//   - labels with a leading/trailing dot or hyphen (malformed DNS names).
//
// NOTE (future): this does not reject registering a public-suffix apex directly
// (e.g. "co.uk") because that needs the public-suffix list, and we deliberately
// avoid pulling in that dependency here. Worth adding if/when a PSL is available.
func looksLikeHostname(s string) bool {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, " \t/:") {
		return false
	}
	// Must have at least one dot (rejects single-label hosts).
	if strings.IndexByte(s, '.') <= 0 {
		return false
	}
	// Reject leading/trailing dots and any empty/hyphen-edged label.
	for _, label := range strings.Split(s, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	// Reject bare IP literals (an IPv4 like "1.2.3.4" otherwise passes the dot check).
	if _, err := netip.ParseAddr(s); err == nil {
		return false
	}
	// Reject the platform content domain and any subdomain of it (platform squat).
	cd := strings.ToLower(projection.ContentDomain)
	if s == cd || strings.HasSuffix(s, "."+cd) {
		return false
	}
	return true
}
