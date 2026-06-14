// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/shipped/internal/audit"
	"github.com/danielpang/shipped/internal/edgerevoke"
	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/pwhash"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// ---------------------------------------------------------------------------
// PUT /v1/sites/{id}/access  {mode, password?, expires_at?, unlisted?}
// ---------------------------------------------------------------------------

type setAccessRequest struct {
	Mode      string  `json:"mode"`
	Password  string  `json:"password,omitempty"`   // plaintext, hashed server-side; only for mode=password
	ExpiresAt *string `json:"expires_at,omitempty"` // RFC3339 link expiry (optional)
	Unlisted  bool    `json:"unlisted,omitempty"`   // public-tier unlisted flag
}

// SetSiteAccess changes a site's access_mode + policy (ADMIN/OWNER only — re-checked
// against the member table). It hashes a password mode's password (never stores
// plaintext), sets optional expiry + unlisted, rewrites the KV RouteValue (mode +
// expires_at), and rejects a public site when allow_external_sharing=false (the
// 0004 trigger → ErrExternalSharingDisabled → 403).
func (a *API) SetSiteAccess(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	siteID := chi.URLParam(r, "id")

	var req setAccessRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	switch req.Mode {
	case projection.AccessPublic, projection.AccessPassword, projection.AccessAllowlist, projection.AccessOrgOnly:
	default:
		httpx.WriteError(w, fmt.Errorf("%w: invalid mode %q", httpx.ErrBadRequest, req.Mode))
		return
	}

	p := store.SetAccessParams{SiteID: siteID, Mode: req.Mode, Unlisted: req.Unlisted}

	// password mode must carry a password; hash it (bcrypt) — never store plaintext.
	if req.Mode == projection.AccessPassword {
		if req.Password == "" {
			httpx.WriteError(w, fmt.Errorf("%w: password is required for password mode", httpx.ErrBadRequest))
			return
		}
		hash, err := pwhash.Hash(req.Password)
		if err != nil {
			httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
			return
		}
		p.PasswordHash = hash
	}

	// Optional expiry.
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		ts, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			httpx.WriteError(w, fmt.Errorf("%w: expires_at must be RFC3339", httpx.ErrBadRequest))
			return
		}
		p.ExpiresAt = &ts
	}

	res, err := a.Store.SetSiteAccess(r.Context(), t, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Rewrite the KV route to reflect the new mode (+ expires_at for the public
	// tier) for EVERY host of the site — the canonical host AND every verified
	// custom-domain host (each has its own route:<host> entry; leaving a custom
	// host at the old mode keeps it serving under the old tier — FIX 1). The DB is
	// authoritative; the projection is a reconcilable cache.
	if a.Projection != nil {
		for _, ru := range res.Routes {
			if err := a.Projection.PutRoute(r.Context(), ru.Host, ru.Route); err != nil {
				logger(r).Error("projection write failed after access change",
					"host", ru.Host, "site_id", siteID, "err", err)
				httpx.WriteError(w, err)
				return
			}
		}
	}

	// Hard revocation: an access-mode / policy change can TIGHTEN access (a viewer
	// who was allowed under the old mode must no longer be), so write the site
	// denylist key (revoked:site:<id>). Every edge token for this site issued before
	// now is invalidated immediately — the Worker + /authz reject it and force a
	// re-auth against the NEW mode, rather than honoring the stale grant until the
	// short TTL lapses (ARCHITECTURE.md §6/§10). Writing on every change is correct
	// and fail-closed: it only affects gated tokens, and a loosen-then-write at worst
	// forces one harmless extra re-auth. Idempotent (max min_iat).
	if a.Revoker != nil {
		minIAT := time.Now().Unix()
		if err := a.Revoker.Revoke(r.Context(), edgerevoke.KindSite, siteID, minIAT); err != nil {
			logger(r).Error("denylist write failed after access change", "site_id", siteID, "err", err)
		}
	}

	logger(r).Info("site access changed", "site_id", siteID, "mode", req.Mode, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSiteAccessChange, "site:"+siteID, map[string]any{
		"mode":     req.Mode,
		"unlisted": req.Unlisted,
		"expires":  req.ExpiresAt != nil,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site_id":  siteID,
		"mode":     req.Mode,
		"unlisted": req.Unlisted,
	})
}

// ---------------------------------------------------------------------------
// allowlist CRUD: POST/DELETE/GET /v1/sites/{id}/allowlist
// ---------------------------------------------------------------------------

type allowlistRequest struct {
	Email string `json:"email"`
}

type allowlistEntryResponse struct {
	Email      string  `json:"email"`
	IsExternal bool    `json:"is_external"`
	ClaimedAt  *string `json:"claimed_at,omitempty"`
	ClaimedBy  *string `json:"claimed_by,omitempty"`
}

// AddAllowlistEntry adds an email grant to a site's allowlist (ADMIN/OWNER only).
// is_external is set automatically when the email's domain is not one of the org's
// VERIFIED domains; the 0004 trigger then rejects it under a false org policy.
func (a *API) AddAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	siteID := chi.URLParam(r, "id")

	var req allowlistRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !looksLikeEmail(email) {
		httpx.WriteError(w, fmt.Errorf("%w: a valid email is required", httpx.ErrBadRequest))
		return
	}

	// Mark external when the email domain is not a verified org domain.
	isExternal, err := a.emailIsExternal(r, t, email)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	entry, err := a.Store.AddAllowlistEntry(r.Context(), t, store.AddAllowlistEntryParams{
		SiteID: siteID, Email: email, IsExternal: isExternal,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	logger(r).Info("allowlist entry added", "site_id", siteID, "email", email, "external", isExternal)
	a.recordAudit(r, t, audit.ActionAllowlistAdd, "site:"+siteID, map[string]any{
		"email":    email,
		"external": isExternal,
	})
	httpx.WriteJSON(w, http.StatusCreated, toAllowlistEntryResponse(entry))
}

// RemoveAllowlistEntry deletes an email grant (ADMIN/OWNER only).
func (a *API) RemoveAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	siteID := chi.URLParam(r, "id")

	var req allowlistRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		httpx.WriteError(w, fmt.Errorf("%w: email is required", httpx.ErrBadRequest))
		return
	}
	if err := a.Store.RemoveAllowlistEntry(r.Context(), t, siteID, email); err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionAllowlistRemove, "site:"+siteID, map[string]any{"email": email})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"removed": email})
}

// ListAllowlist returns a site's allowlist (any org member may read; the data is
// org-scoped by RLS).
func (a *API) ListAllowlist(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")
	entries, err := a.Store.ListAllowlistEntries(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]allowlistEntryResponse, len(entries))
	for i, e := range entries {
		out[i] = toAllowlistEntryResponse(e)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"allowlist": out})
}

// ---------------------------------------------------------------------------
// PUT /v1/orgs/allow-external-sharing  {enabled}
// ---------------------------------------------------------------------------

type allowExternalRequest struct {
	Enabled bool `json:"enabled"`
}

// SetAllowExternalSharing toggles the org sharing policy (ADMIN/OWNER only). When
// DISABLING it reconciles: the store downgrades public sites to org_only and drops
// external allowlist grants; this handler then rewrites each downgraded site's KV
// route (revoking external/public access at the edge).
func (a *API) SetAllowExternalSharing(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}

	var req allowExternalRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}

	res, err := a.Store.SetAllowExternalSharing(r.Context(), t, req.Enabled)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Rewrite the routes of any downgraded sites so external/public access is
	// revoked at the edge within the propagation window.
	if a.Projection != nil {
		for _, d := range res.Downgraded {
			if err := a.Projection.PutRoute(r.Context(), d.Host, d.Route); err != nil {
				logger(r).Error("projection rewrite failed during reconcile",
					"host", d.Host, "err", err)
				httpx.WriteError(w, err)
				return
			}
		}
	}

	// Hard revocation: disabling external sharing tightens org-wide access, so write
	// the org denylist key (revoked:org:<org>) — every edge token issued before now
	// for this org's external/public viewers is invalidated immediately, not just at
	// the next short-TTL expiry. Idempotent (max min_iat). Best-effort: the routes
	// were already rewritten above; a denylist hiccup only loses the IMMEDIATE
	// revocation, the short TTL still backstops it (and a rebuild re-asserts it).
	if !res.AllowExternalSharing && a.Revoker != nil {
		minIAT := time.Now().Unix()
		if err := a.Revoker.Revoke(r.Context(), edgerevoke.KindOrg, t.OrgID, minIAT); err != nil {
			logger(r).Error("denylist write failed disabling external sharing", "org_id", t.OrgID, "err", err)
		}
	}

	logger(r).Info("allow_external_sharing toggled",
		"org_id", t.OrgID, "enabled", res.AllowExternalSharing, "downgraded", len(res.Downgraded))
	a.recordAudit(r, t, audit.ActionAllowExternalSharing, "org:"+t.OrgID, map[string]any{
		"enabled":          res.AllowExternalSharing,
		"downgraded_sites": len(res.Downgraded),
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"allow_external_sharing": res.AllowExternalSharing,
		"downgraded_sites":       len(res.Downgraded),
	})
}

// ---------------------------------------------------------------------------
// GET /v1/members
// ---------------------------------------------------------------------------

type memberResponse struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// ListMembers lists the caller org's members (read from the Better Auth member
// table). Any member may list; the read is org-scoped. If the Better Auth table is
// unavailable, returns an empty list with a note (self-host pre-Better-Auth).
func (a *API) ListMembers(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	members, err := a.Store.ListMembers(r.Context(), t.OrgID)
	if err != nil {
		if err.Error() == store.ErrAuthSchemaUnavailable.Error() {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": []memberResponse{}})
			return
		}
		writeStoreError(w, err)
		return
	}
	out := make([]memberResponse, len(members))
	for i, m := range members {
		out[i] = memberResponse{UserID: m.UserID, Role: m.Role}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": out})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func toAllowlistEntryResponse(e store.AllowlistEntry) allowlistEntryResponse {
	out := allowlistEntryResponse{Email: e.Email, IsExternal: e.IsExternal}
	if e.ClaimedAt != nil {
		s := e.ClaimedAt.UTC().Format(time.RFC3339)
		out.ClaimedAt = &s
	}
	out.ClaimedBy = e.ClaimedBy
	return out
}

// emailIsExternal reports whether email's domain is NOT one of the org's verified
// domains (app.domains, verify_status=verified). A grant for an external domain is
// flagged is_external and is rejected under a false org policy by the 0004 trigger.
func (a *API) emailIsExternal(r *http.Request, t store.Tenant, email string) (bool, error) {
	at := strings.LastIndexByte(email, '@')
	if at < 0 || at == len(email)-1 {
		return true, nil // malformed → treat as external (most restrictive)
	}
	domain := email[at+1:]

	// The org's verified custom domains define "internal" email domains. We can't
	// list domains org-wide without a site context, so we approximate: an email is
	// internal only if its domain matches a VERIFIED org domain. Absent any verified
	// domain, every external-looking email is external. We list via the member's
	// org sites is overkill; instead rely on the org policy gate. For Phase 2 we
	// mark external unless the domain matches a verified org domain hostname suffix.
	//
	// Implementation: there is no org-wide domain list query (domains are per-site);
	// to keep this correct + cheap we treat an email as internal when its domain
	// equals a verified domain hostname of ANY of the org's sites. We fetch the
	// org's sites and check their verified domains.
	sites, err := a.Store.ListSites(r.Context(), t)
	if err != nil {
		return true, err
	}
	for _, s := range sites {
		domains, err := a.Store.ListDomainsForSite(r.Context(), t, s.ID)
		if err != nil {
			return true, err
		}
		for _, d := range domains {
			if d.VerifyStatus != store.DomainVerified {
				continue
			}
			if domainMatches(d.Hostname, domain) {
				return false, nil // internal
			}
		}
	}
	return true, nil
}

// domainMatches reports whether the email domain matches a verified hostname
// (exact, or the email domain is a parent of the hostname e.g. acme.com matches
// docs.acme.com).
func domainMatches(hostname, emailDomain string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	emailDomain = strings.ToLower(strings.TrimSpace(emailDomain))
	if hostname == emailDomain {
		return true
	}
	return strings.HasSuffix(hostname, "."+emailDomain)
}

// looksLikeEmail is a minimal sanity check (a full RFC 5322 validator is overkill;
// the verified-email match at mint time is the real gate).
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && strings.IndexByte(s[at+1:], '.') >= 0
}
