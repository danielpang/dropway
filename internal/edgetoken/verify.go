// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgetoken

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"

	"github.com/danielpang/dropway/internal/auth"
)

// Verifier verifies edge tokens against a set of trusted Ed25519 public keys
// (keyed by kid), pinning alg=EdDSA, the fixed issuer, the expected audience
// (the content host), and a required exp. It is the Go-side mirror of the Worker's
// `jose` verification, used in tests and any in-process re-check.
//
// The production Worker fetches the JWKS from /.well-known/edge-jwks and verifies
// there; this verifier exists so the mint→verify round-trip is exercised in Go
// (unit test) and so the Go API could re-validate a token it issued.
type Verifier struct {
	keys map[string]ed25519.PublicKey
}

// NewVerifier builds a Verifier trusting the supplied public keys (kid → key).
func NewVerifier(keys map[string]ed25519.PublicKey) *Verifier {
	cp := make(map[string]ed25519.PublicKey, len(keys))
	for k, v := range keys {
		cp[k] = v
	}
	return &Verifier{keys: cp}
}

// VerifierForSigner builds a Verifier that trusts a single signer's public key.
func VerifierForSigner(s *Signer) *Verifier {
	return NewVerifier(map[string]ed25519.PublicKey{s.Kid(): s.PublicKey()})
}

// ErrUnknownKey is returned when the token's kid is not in the trusted set.
var ErrUnknownKey = errors.New("edgetoken: unknown signing key id")

// Verify parses and fully validates token, binding it to expectedHost (the aud).
// It enforces: alg=EdDSA (rejects none + HS*), iss==Issuer, aud==expectedHost, a
// present unexpired exp, and a known kid. On success it returns the claims.
func (v *Verifier) Verify(token, expectedHost string) (*Claims, error) {
	if expectedHost == "" {
		return nil, errors.New("edgetoken: empty expected host")
	}
	claims := &Claims{}
	keyfunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		key, ok := v.keys[kid]
		if !ok {
			return nil, ErrUnknownKey
		}
		return key, nil
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{signingAlg}), // rejects none + HMAC
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(expectedHost), // aud must equal the content host (anti-replay)
		jwt.WithExpirationRequired(),
		// Same drift tolerance as the user-JWT verifier and the Worker's
		// clockTolerance (edge/serving-worker/src/edgetoken.ts) — the API mints on
		// one clock, the edge verifies on another. Keep all three in lockstep.
		jwt.WithLeeway(auth.ClockSkewLeeway),
	)
	if _, err := parser.ParseWithClaims(token, claims, keyfunc); err != nil {
		return nil, err
	}
	// Defensive: the edge-specific claims must be well-formed too.
	if claims.SiteID == "" {
		return nil, errors.New("edgetoken: missing site_id claim")
	}
	switch claims.Mode {
	case ModePassword, ModeAllowlist, ModeOrgOnly:
	default:
		return nil, fmt.Errorf("edgetoken: invalid mode claim %q", claims.Mode)
	}
	return claims, nil
}
