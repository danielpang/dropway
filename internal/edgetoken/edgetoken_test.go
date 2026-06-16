package edgetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newTestSigner(t *testing.T) *Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSigner(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMintVerify_RoundTrip(t *testing.T) {
	s := newTestSigner(t)
	v := VerifierForSigner(s)

	const host = "acme.dropwaycontent.com"
	tok, err := s.Mint(MintParams{
		ContentHost: host,
		Subject:     "user-123",
		SiteID:      "11111111-1111-1111-1111-111111111111",
		Mode:        ModeOrgOnly,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	claims, err := v.Verify(tok, host)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Issuer != Issuer {
		t.Errorf("iss = %q", claims.Issuer)
	}
	if claims.Subject != "user-123" {
		t.Errorf("sub = %q", claims.Subject)
	}
	if claims.SiteID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("site_id = %q", claims.SiteID)
	}
	if claims.Mode != ModeOrgOnly {
		t.Errorf("mode = %q", claims.Mode)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != host {
		t.Errorf("aud = %v", claims.Audience)
	}
	if claims.ID == "" {
		t.Error("jti empty")
	}
}

// TestVerify_AudBinding asserts a token minted for host A is rejected when
// presented at host B (anti-replay across content hosts).
func TestVerify_AudBinding(t *testing.T) {
	s := newTestSigner(t)
	v := VerifierForSigner(s)

	tok, err := s.Mint(MintParams{
		ContentHost: "a.dropwaycontent.com",
		Subject:     "user-1",
		SiteID:      "11111111-1111-1111-1111-111111111111",
		Mode:        ModeAllowlist,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(tok, "b.dropwaycontent.com"); err == nil {
		t.Fatal("token for host A wrongly verified at host B (aud not bound)")
	}
	// Sanity: it DOES verify at its own host.
	if _, err := v.Verify(tok, "a.dropwaycontent.com"); err != nil {
		t.Fatalf("token rejected at its own host: %v", err)
	}
}

func TestVerify_Expired(t *testing.T) {
	s := newTestSigner(t)
	v := VerifierForSigner(s)
	const host = "acme.dropwaycontent.com"
	tok, err := s.Mint(MintParams{
		ContentHost: host, Subject: "u", SiteID: "11111111-1111-1111-1111-111111111111",
		Mode: ModePassword, TTL: -time.Minute, // already expired
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(tok, host); err == nil {
		t.Fatal("expired token wrongly verified")
	}
}

// TestVerify_RejectsAlgNoneAndHS asserts the alg is pinned to EdDSA: a `none`
// token and an HS256 token signed with the public key as the secret are rejected
// (the classic alg-confusion attack).
func TestVerify_RejectsAlgNoneAndHS(t *testing.T) {
	s := newTestSigner(t)
	v := VerifierForSigner(s)
	const host = "acme.dropwaycontent.com"

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   "u",
			Audience:  jwt.ClaimStrings{host},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		SiteID: "11111111-1111-1111-1111-111111111111",
		Mode:   ModeOrgOnly,
	}

	// alg:none.
	noneTok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	noneTok.Header["kid"] = s.Kid()
	noneStr, err := noneTok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(noneStr, host); err == nil {
		t.Fatal("alg:none token wrongly accepted")
	}

	// HS256 signed with the (public) key bytes as the HMAC secret.
	hsTok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	hsTok.Header["kid"] = s.Kid()
	hsStr, err := hsTok.SignedString([]byte(s.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(hsStr, host); err == nil {
		t.Fatal("HS256 (alg-confusion) token wrongly accepted")
	}
}

func TestVerify_UnknownKid(t *testing.T) {
	signer := newTestSigner(t)
	other := newTestSigner(t)
	// Verifier trusts only `other`, not `signer`.
	v := VerifierForSigner(other)
	const host = "acme.dropwaycontent.com"
	tok, _ := signer.Mint(MintParams{
		ContentHost: host, Subject: "u", SiteID: "11111111-1111-1111-1111-111111111111", Mode: ModeOrgOnly,
	})
	if _, err := v.Verify(tok, host); err == nil {
		t.Fatal("token from an untrusted signer wrongly accepted")
	}
}

func TestJWKS_RoundTripsPublicKey(t *testing.T) {
	s := newTestSigner(t)
	set := s.JWKS()
	if len(set.Keys) != 1 {
		t.Fatalf("want 1 key, got %d", len(set.Keys))
	}
	k := set.Keys[0]
	if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Alg != "EdDSA" || k.Use != "sig" {
		t.Errorf("jwk = %+v", k)
	}
	if k.Kid != s.Kid() {
		t.Errorf("kid mismatch: %q != %q", k.Kid, s.Kid())
	}
	raw, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(s.PublicKey()) {
		t.Error("jwk x does not match the signer public key")
	}

	// Build a verifier from the JWKS-derived public key and verify a real token —
	// proves the JWKS the Worker fetches is sufficient to verify.
	pub := ed25519.PublicKey(raw)
	v := NewVerifier(map[string]ed25519.PublicKey{k.Kid: pub})
	const host = "acme.dropwaycontent.com"
	tok, _ := s.Mint(MintParams{ContentHost: host, Subject: "u", SiteID: "11111111-1111-1111-1111-111111111111", Mode: ModeOrgOnly})
	if _, err := v.Verify(tok, host); err != nil {
		t.Fatalf("token rejected by JWKS-derived verifier: %v", err)
	}
}

func TestLoadOrGenerateSigner(t *testing.T) {
	// Empty → generates, returns a reusable seed.
	s1, seed, generated, err := LoadOrGenerateSigner("")
	if err != nil {
		t.Fatal(err)
	}
	if !generated || seed == "" {
		t.Fatalf("expected generated key + seed, got generated=%v seed=%q", generated, seed)
	}

	// Reloading with that seed yields the SAME kid (stable key id) and verifies a
	// token minted by the first signer.
	s2, _, generated2, err := LoadOrGenerateSigner(seed)
	if err != nil {
		t.Fatal(err)
	}
	if generated2 {
		t.Fatal("reload should not regenerate")
	}
	if s1.Kid() != s2.Kid() {
		t.Fatalf("kid not stable across reload: %q != %q", s1.Kid(), s2.Kid())
	}
	const host = "acme.dropwaycontent.com"
	tok, _ := s1.Mint(MintParams{ContentHost: host, Subject: "u", SiteID: "11111111-1111-1111-1111-111111111111", Mode: ModeOrgOnly})
	if _, err := VerifierForSigner(s2).Verify(tok, host); err != nil {
		t.Fatalf("seed-reloaded signer can't verify first signer's token: %v", err)
	}
}

func TestAnonSubject(t *testing.T) {
	a, err := AnonSubject()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, "anon:") {
		t.Errorf("anon subject = %q", a)
	}
	b, _ := AnonSubject()
	if a == b {
		t.Error("anon subjects should be random/unique")
	}
}

func TestMint_Validation(t *testing.T) {
	s := newTestSigner(t)
	cases := []MintParams{
		{ContentHost: "", Subject: "u", SiteID: "s", Mode: ModeOrgOnly},
		{ContentHost: "h", Subject: "", SiteID: "s", Mode: ModeOrgOnly},
		{ContentHost: "h", Subject: "u", SiteID: "", Mode: ModeOrgOnly},
		{ContentHost: "h", Subject: "u", SiteID: "s", Mode: "bogus"},
	}
	for i, c := range cases {
		if _, err := s.Mint(c); err == nil {
			t.Errorf("case %d: expected mint error for %+v", i, c)
		}
	}
}
