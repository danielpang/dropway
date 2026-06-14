package edgetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signRaw builds and signs an edge-token JWT with arbitrary claims using a fresh
// key, returning the token plus a Verifier that trusts that key under the given
// kid. It lets the defensive-claim branches be reached with tokens that carry a
// VALID signature + iss/aud/exp but a malformed edge-specific claim.
func signRaw(t *testing.T, kid string, claims Claims) (string, *Verifier) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signed, NewVerifier(map[string]ed25519.PublicKey{kid: pub})
}

func baseClaims(host string) Claims {
	return Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   "u",
			Audience:  jwt.ClaimStrings{host},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		SiteID: "11111111-1111-1111-1111-111111111111",
		Mode:   ModeOrgOnly,
	}
}

// TestVerify_EmptyHost asserts an empty expected host is rejected before any parse
// (a caller must always bind the token to a concrete content host).
func TestVerify_EmptyHost(t *testing.T) {
	s := newTestSigner(t)
	v := VerifierForSigner(s)
	if _, err := v.Verify("anything", ""); err == nil {
		t.Fatal("empty expected host should be rejected")
	}
}

// TestVerify_MissingSiteIDClaim asserts a token with a valid signature/iss/aud/exp
// but an empty site_id is rejected by the defensive claim check.
func TestVerify_MissingSiteIDClaim(t *testing.T) {
	const host = "acme.shippedusercontent.com"
	c := baseClaims(host)
	c.SiteID = "" // strip the required edge claim
	tok, v := signRaw(t, "edge-test", c)
	if _, err := v.Verify(tok, host); err == nil {
		t.Fatal("token with empty site_id should be rejected")
	}
}

// TestVerify_BadModeClaim asserts a token carrying an unrecognized mode is rejected
// (the mode drives the Worker's gate; a bogus value must not slip through).
func TestVerify_BadModeClaim(t *testing.T) {
	const host = "acme.shippedusercontent.com"
	c := baseClaims(host)
	c.Mode = "superuser" // not password/allowlist/org_only
	tok, v := signRaw(t, "edge-test", c)
	if _, err := v.Verify(tok, host); err == nil {
		t.Fatal("token with an invalid mode claim should be rejected")
	}
}

// TestVerify_WrongIssuer asserts a token whose iss is not the fixed edge issuer is
// rejected (anti-cross-issuer confusion).
func TestVerify_WrongIssuer(t *testing.T) {
	const host = "acme.shippedusercontent.com"
	c := baseClaims(host)
	c.Issuer = "https://evil.example/edge"
	tok, v := signRaw(t, "edge-test", c)
	if _, err := v.Verify(tok, host); err == nil {
		t.Fatal("token with a foreign issuer should be rejected")
	}
}

// TestVerify_MissingExp asserts a token without exp is rejected (exp is required;
// a non-expiring edge token would defeat the short-TTL revocation window).
func TestVerify_MissingExp(t *testing.T) {
	const host = "acme.shippedusercontent.com"
	c := baseClaims(host)
	c.ExpiresAt = nil // no exp
	tok, v := signRaw(t, "edge-test", c)
	if _, err := v.Verify(tok, host); err == nil {
		t.Fatal("token without exp should be rejected (exp required)")
	}
}

// TestVerify_MalformedToken asserts a structurally broken token string is rejected
// (not parsed into bogus claims).
func TestVerify_MalformedToken(t *testing.T) {
	v := VerifierForSigner(newTestSigner(t))
	for _, garbage := range []string{"", "not.a.jwt", "a.b", "....", "header.payload.sig.extra"} {
		if _, err := v.Verify(garbage, "acme.shippedusercontent.com"); err == nil {
			t.Errorf("malformed token %q should be rejected", garbage)
		}
	}
}
