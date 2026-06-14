// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/danielpang/shipped/internal/edgetoken"
	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/pwhash"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// EdgeJWKS serves the edge signer's public JWKS at GET /.well-known/edge-jwks
// (docs/ARCHITECTURE.md edge-token spec). The Worker fetches this and pins
// alg=EdDSA when verifying the host-scoped edge token. Unauthenticated + cacheable
// (public keys). Separate keypair from Better Auth's user JWKS.
func (a *API) EdgeJWKS(w http.ResponseWriter, r *http.Request) {
	if a.EdgeSigner == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "edge signer not configured"})
		return
	}
	body, err := a.EdgeSigner.JWKSJSON()
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// POST /v1/authz/mint  {host, next} → {token}
// ---------------------------------------------------------------------------

type mintRequest struct {
	Host string `json:"host"`
	Next string `json:"next,omitempty"` // path the dashboard redirects back to (not trusted for authz)
}

type mintResponse struct {
	Token string `json:"token"`
	Host  string `json:"host"`
	Mode  string `json:"mode"`
}

// AuthzMint authorizes the VERIFIED viewer (Better Auth JWT) for an
// org_only/allowlist site resolved from {host} and mints a host-scoped edge token
// (sub = viewer). It enforces the AUTHZ RULES in the store (live re-check; claim a
// pending allowlist entry; external grants require allow_external_sharing; expired
// policy refuses). On failure it returns 403/404 with a typed reason; password-mode
// sites must use /v1/authz/password instead.
//
// `next` is echoed by the caller only as a redirect target on the CONTENT host the
// Worker controls — it is never used for authorization (no open-redirect; §10).
func (a *API) AuthzMint(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireSigner(w) {
		return
	}

	var req mintRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	host := normalizeHost(req.Host)
	if host == "" {
		httpx.WriteError(w, fmt.Errorf("%w: host is required", httpx.ErrBadRequest))
		return
	}

	viewer := store.MintViewer{
		UserID:        claims.UserID(),
		OrgID:         claims.OrgID,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
	}
	decision, err := a.Store.AuthorizeMint(r.Context(), viewer, host)
	if err != nil {
		// Password-mode site reached via the mint endpoint → 400 pointing at the
		// password exchange.
		if errors.Is(err, store.ErrPasswordModeUsesPasswordEndpoint) {
			httpx.WriteError(w, fmt.Errorf("%w: this site uses a password; use /v1/authz/password", httpx.ErrBadRequest))
			return
		}
		writeStoreError(w, err)
		return
	}

	token, err := a.EdgeSigner.Mint(edgetoken.MintParams{
		ContentHost: decision.Host,
		Subject:     decision.Subject,
		SiteID:      decision.SiteID,
		Mode:        decision.Mode,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	logger(r).Info("edge token minted",
		"host", decision.Host, "site_id", decision.SiteID, "mode", decision.Mode,
		"sub", decision.Subject, "org_id", decision.OrgID)
	httpx.WriteJSON(w, http.StatusOK, mintResponse{Token: token, Host: decision.Host, Mode: decision.Mode})
}

// ---------------------------------------------------------------------------
// POST /v1/authz/password  {host, password} → {token}
// ---------------------------------------------------------------------------

type passwordRequest struct {
	Host     string `json:"host"`
	Password string `json:"password"`
}

// AuthzPassword verifies a site password against site_access_policy.password_hash
// (constant-time bcrypt compare in pwhash) and, on success, mints an ANON edge
// token (sub = "anon:<random>") — no viewer identity. Wrong password → 401; an
// expired policy → 403 ("link expired"); a non-password site → 400. This endpoint
// is JWT-free (the dashboard renders a platform-controlled password form and posts
// here), so it must NOT echo whether the host exists vs the password is wrong in a
// way that aids enumeration — both map to a generic 401 for the password path.
func (a *API) AuthzPassword(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) || !a.requireSigner(w) {
		return
	}
	var req passwordRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	host := normalizeHost(req.Host)
	if host == "" || req.Password == "" {
		httpx.WriteError(w, fmt.Errorf("%w: host and password are required", httpx.ErrBadRequest))
		return
	}

	decision, hash, err := a.Store.ResolveForPassword(r.Context(), host)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrPolicyExpired):
			httpx.WriteError(w, fmt.Errorf("%w: this share link has expired", httpx.ErrForbidden))
		case errors.Is(err, store.ErrHostNotFound), errors.Is(err, store.ErrNoPolicy), errors.Is(err, store.ErrNotGated):
			// Don't distinguish "no such host" from "not a password site" — return a
			// generic 401 so the password gate isn't an existence oracle. Burn an
			// equivalent bcrypt cost so the response time doesn't leak existence.
			_ = pwhash.DummyVerify(req.Password)
			httpx.WriteError(w, wrapPasswordUnauthorized())
		default:
			writeStoreError(w, err)
		}
		return
	}

	if err := pwhash.Verify(hash, req.Password); err != nil {
		// Wrong password (or any verify failure) → 401, constant-time compare done.
		httpx.WriteError(w, wrapPasswordUnauthorized())
		return
	}

	anon, err := edgetoken.AnonSubject()
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	token, err := a.EdgeSigner.Mint(edgetoken.MintParams{
		ContentHost: decision.Host,
		Subject:     anon,
		SiteID:      decision.SiteID,
		Mode:        projection.AccessPassword,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	logger(r).Info("password edge token minted", "host", decision.Host, "site_id", decision.SiteID)
	httpx.WriteJSON(w, http.StatusOK, mintResponse{Token: token, Host: decision.Host, Mode: projection.AccessPassword})
}

// wrapPasswordUnauthorized maps to 401 for the password gate.
func wrapPasswordUnauthorized() error {
	return fmt.Errorf("%w: incorrect password", httpx.ErrUnauthorized)
}

// normalizeHost lowercases + trims a host (content hosts are case-insensitive).
func normalizeHost(h string) string {
	out := make([]rune, 0, len(h))
	for _, c := range h {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}
