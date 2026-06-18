// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// JWK is a single Ed25519 (OKP) public key in JWKS form, matching what the Worker
// parses with `jose` and what internal/auth's verifier reads for the user JWT.
type JWK struct {
	Kty string `json:"kty"` // "OKP"
	Crv string `json:"crv"` // "Ed25519"
	Kid string `json:"kid"`
	X   string `json:"x"`   // base64url(raw public key)
	Use string `json:"use"` // "sig"
	Alg string `json:"alg"` // "EdDSA"
}

// JWKS is the document served at GET /.well-known/edge-jwks.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWKS returns the public JWKS for this signer (one OKP/Ed25519 key). The Worker
// fetches this and pins alg=EdDSA when verifying.
func (s *Signer) JWKS() JWKS {
	return JWKS{Keys: []JWK{{
		Kty: "OKP",
		Crv: "Ed25519",
		Kid: s.kid,
		X:   base64.RawURLEncoding.EncodeToString(s.pub),
		Use: "sig",
		Alg: signingAlg,
	}}}
}

// JWKSJSON returns the marshaled JWKS bytes for the HTTP handler.
func (s *Signer) JWKSJSON() ([]byte, error) { return json.Marshal(s.JWKS()) }

// LoadOrGenerateSigner builds a Signer from the EDGE_SIGNING_KEY env value, or
// GENERATES a fresh key (dev convenience) when it is empty, returning the
// generated seed (base64url) so a caller can log it for reuse. The bool reports
// whether a key was generated (so main can warn it's ephemeral).
//
// Accepted EDGE_SIGNING_KEY encodings (auto-detected):
//   - base64url / base64-std of a 32-byte Ed25519 SEED (preferred, compact)
//   - hex of a 32-byte seed
//   - base64/hex of a full 64-byte Ed25519 private key
//
// The SEPARATE keypair requirement (vs Better Auth's user JWT) is satisfied by
// reading a dedicated env var; never reuse the user-JWT key here.
func LoadOrGenerateSigner(envValue string) (signer *Signer, generatedSeedB64 string, generated bool, err error) {
	envValue = strings.TrimSpace(envValue)
	if envValue == "" {
		pub, priv, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			return nil, "", false, gerr
		}
		s, serr := NewSigner(priv)
		if serr != nil {
			return nil, "", false, serr
		}
		seed := priv.Seed()
		_ = pub
		return s, base64.RawURLEncoding.EncodeToString(seed), true, nil
	}

	priv, perr := parsePrivateKey(envValue)
	if perr != nil {
		return nil, "", false, perr
	}
	s, serr := NewSigner(priv)
	if serr != nil {
		return nil, "", false, serr
	}
	return s, "", false, nil
}

// parsePrivateKey decodes a seed (32 bytes) or full private key (64 bytes) from a
// base64url / base64-std / hex string.
func parsePrivateKey(s string) (ed25519.PrivateKey, error) {
	raw, err := decodeKeyBytes(s)
	if err != nil {
		return nil, fmt.Errorf("edgetoken: EDGE_SIGNING_KEY not base64/hex: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize: // 32-byte seed
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize: // 64-byte full key
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, fmt.Errorf("edgetoken: EDGE_SIGNING_KEY must be a 32-byte seed or 64-byte key, got %d bytes", len(raw))
	}
}

// decodeKeyBytes tries base64url (no pad), base64 std, then hex.
func decodeKeyBytes(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("unrecognized encoding")
}
