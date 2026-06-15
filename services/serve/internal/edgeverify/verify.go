// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgeverify

import (
	"context"
	"crypto/ed25519"

	"github.com/danielpang/shipped/internal/edgerevoke"
	"github.com/danielpang/shipped/internal/edgetoken"
)

// keySource supplies the trusted kid→key map for a verification (remote JWKS or a
// static in-process set). A cold-cache fetch failure returns an error so the
// caller fails closed (deny / 302) rather than serving on an unverifiable token.
type keySource interface {
	loadKeys(ctx context.Context) (map[string]ed25519.PublicKey, error)
}

// staticKeys is a fixed kid→key source (used by NewForKeys / NewForSigner in
// tests and any in-process co-located deployment).
type staticKeys struct{ keys map[string]ed25519.PublicKey }

func (s staticKeys) loadKeys(context.Context) (map[string]ed25519.PublicKey, error) {
	return s.keys, nil
}

// RevocationReader reads the hard-revocation denylist min_iat for a (kind, id),
// returning ok=false on a CLEAN miss (key absent). It must return an ERROR on any
// read failure; the caller fails CLOSED (treats the error as revoked). The org
// dimension's id comes from the ROUTE, never a token claim.
type RevocationReader interface {
	MinIAT(ctx context.Context, kind edgerevoke.Kind, id string) (minIAT int64, ok bool, err error)
}

// Verifier verifies an edge token against a route. It composes the shared
// edgetoken.Verifier (alg/iss/aud/exp/kid + site_id-present + mode-enum) with the
// route bindings (site_id==route, mode==route) and the hard-revocation check.
type Verifier struct {
	keys keySource
	// revoked reads the denylist. Nil ⇒ FAIL CLOSED (every gated request is treated
	// as revoked), matching the Worker's "no denylist binding ⇒ revoked=true".
	revoked RevocationReader
}

// New builds a Verifier backed by a remote JWKS endpoint (the production /
// self-host wiring). The revocation reader is required for any gated serving; a
// nil reader fails closed on every request.
func New(jwksURL string, revoked RevocationReader) *Verifier {
	return &Verifier{keys: newJWKSClient(jwksURL, nil, nil), revoked: revoked}
}

// NewForKeys builds a Verifier over a static kid→key map (tests / co-located).
func NewForKeys(keys map[string]ed25519.PublicKey, revoked RevocationReader) *Verifier {
	cp := make(map[string]ed25519.PublicKey, len(keys))
	for k, v := range keys {
		cp[k] = v
	}
	return &Verifier{keys: staticKeys{cp}, revoked: revoked}
}

// NewForSigner builds a Verifier trusting a single signer's public key (tests).
func NewForSigner(s *edgetoken.Signer, revoked RevocationReader) *Verifier {
	return NewForKeys(map[string]ed25519.PublicKey{s.Kid(): s.PublicKey()}, revoked)
}

// Result is the subset of verified claims the serving path consumes.
type Result struct {
	Sub    string
	SiteID string
	Mode   string
	IAT    int64
}

// Verify fully verifies token for (host, route site_id, route access_mode) and
// runs the hard-revocation check using orgID from the ROUTE. It returns ok=false
// for ANY failure — bad signature, wrong alg/iss/aud, expired, unknown kid,
// site_id mismatch, mode mismatch, empty sub, a key-source outage, or a
// revocation hit / revocation read error (fail closed). The caller treats
// ok=false uniformly as "no valid credential" → 302 to /authz.
func (v *Verifier) Verify(ctx context.Context, token, host, routeSiteID, routeMode, routeOrgID string) (Result, bool) {
	if token == "" || host == "" || routeSiteID == "" {
		return Result{}, false
	}

	keys, err := v.keys.loadKeys(ctx)
	if err != nil {
		// Cold-cache JWKS outage ⇒ fail closed.
		return Result{}, false
	}

	// Core JWT verification via the shared verifier: pins alg=EdDSA (rejects
	// none/HS*), iss==Issuer, aud==host (anti-replay), exp required + clockTolerance
	// 0 (golang-jwt default), kid lookup, plus site_id non-empty + mode enum.
	claims, err := edgetoken.NewVerifier(keys).Verify(token, host)
	if err != nil {
		return Result{}, false
	}

	// Route bindings the shared verifier does NOT do:
	// (a) site_id claim == route's site_id (no sibling-site reuse).
	if claims.SiteID != routeSiteID {
		return Result{}, false
	}
	// (b) mode claim == route's CURRENT access_mode (H1 mode-binding).
	if routeMode != "" && claims.Mode != routeMode {
		return Result{}, false
	}
	// sub present + non-empty (RegisteredClaims.Subject).
	if claims.Subject == "" {
		return Result{}, false
	}

	// iat (unix seconds); absent/garbled ⇒ 0 so any non-zero min_iat revokes.
	var iat int64
	if claims.IssuedAt != nil {
		iat = claims.IssuedAt.Unix()
	}

	// (c) hard-revocation: reject if ANY of revoked:user:<sub> / site:<site_id> /
	// org:<routeOrgID> has min_iat > iat. orgID is from the ROUTE. FAIL CLOSED on a
	// missing reader or any read error.
	if v.isRevoked(ctx, claims.Subject, routeSiteID, routeOrgID, iat) {
		return Result{}, false
	}

	return Result{Sub: claims.Subject, SiteID: claims.SiteID, Mode: claims.Mode, IAT: iat}, true
}

// isRevoked returns true (revoked) if any denylist dimension has min_iat > iat, or
// if the reader is missing or any read errors (fail closed). A clean miss for a
// dimension is "not revoked" for that dimension.
func (v *Verifier) isRevoked(ctx context.Context, sub, siteID, orgID string, iat int64) bool {
	if v.revoked == nil {
		return true // no denylist reader ⇒ cannot prove not-revoked ⇒ fail closed
	}
	dims := []struct {
		kind edgerevoke.Kind
		id   string
	}{
		{edgerevoke.KindUser, sub},
		{edgerevoke.KindSite, siteID},
		{edgerevoke.KindOrg, orgID},
	}
	for _, d := range dims {
		minIAT, ok, err := v.revoked.MinIAT(ctx, d.kind, d.id)
		if err != nil {
			return true // a read error is indistinguishable from "maybe revoked"
		}
		if ok && minIAT > iat {
			return true
		}
	}
	return false
}
