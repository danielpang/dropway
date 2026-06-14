// Package edgetoken mints and verifies the host-scoped EDGE TOKEN that gates
// `password`/`allowlist`/`org_only` sites at the Cloudflare serving Worker
// (docs/ARCHITECTURE.md §1/§6, Phase 2).
//
// The edge token is a compact EdDSA JWT signed by the Go API's "edge signer" — a
// SEPARATE Ed25519 keypair from Better Auth's user JWT. The Worker verifies it
// (with `jose`, alg pinned to EdDSA) against the public JWKS the Go API serves at
// GET /.well-known/edge-jwks. The signer/verifier here is the Go side of that
// contract; the Worker MUST follow the same claim set:
//
//	iss = "https://api.shipped.app/edge"
//	aud = <content_host>            e.g. "acme.shippedusercontent.com"
//	sub = <viewer user_id>          (org_only/allowlist) OR "anon:<random>" (password)
//	exp = now + 15m, iat, jti
//	+ { "site_id": <uuid>, "mode": "password"|"allowlist"|"org_only" }
//
// SECURITY: the alg is pinned to EdDSA on verify (rejects `none` and HS*), aud is
// bound to the exact content host (replay at another host fails), and exp is
// required — mirroring the internal/auth.Verifier hardening for the user JWT.
package edgetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer is the fixed `iss` claim of every edge token (the Go API edge signer).
// The Worker pins this exact value.
const Issuer = "https://api.shipped.app/edge"

// DefaultTTL is the lifetime of a minted edge token (spec: now + 15m). Short so a
// revoked Better Auth session can't keep re-minting for long.
const DefaultTTL = 15 * time.Minute

// signingAlg is the ONLY accepted algorithm, pinned on both sign and verify.
const signingAlg = "EdDSA"

// Mode is the access mode the token authorizes. Mirrors app.sites.access_mode
// (minus public, which needs no token).
const (
	ModePassword  = "password"
	ModeAllowlist = "allowlist"
	ModeOrgOnly   = "org_only"
)

// Claims is the edge token payload. RegisteredClaims carries iss/aud/sub/exp/iat/
// jti; SiteID/Mode are the edge-specific claims.
type Claims struct {
	jwt.RegisteredClaims
	SiteID string `json:"site_id"`
	Mode   string `json:"mode"`
}

// MintParams are the inputs to Mint. Subject is the viewer user id for
// org_only/allowlist, or any anon subject for password (use AnonSubject()).
type MintParams struct {
	ContentHost string        // aud — the exact content host (e.g. acme.shippedusercontent.com)
	Subject     string        // sub — viewer user id or "anon:<random>"
	SiteID      string        // site_id claim
	Mode        string        // mode claim (password|allowlist|org_only)
	TTL         time.Duration // 0 → DefaultTTL
}

// Signer mints edge tokens with one Ed25519 private key, identified by kid. It is
// immutable after construction and safe for concurrent use.
type Signer struct {
	kid  string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// NewSigner builds a Signer from an Ed25519 private key. The kid is derived from
// the public key so it is stable across restarts that reuse the same key (and so
// the JWKS the Worker fetches is consistent).
func NewSigner(priv ed25519.PrivateKey) (*Signer, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("edgetoken: bad private key size %d", len(priv))
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("edgetoken: private key has no ed25519 public key")
	}
	return &Signer{kid: KidFor(pub), priv: priv, pub: pub}, nil
}

// Kid returns the signer's key id (the `kid` header of every minted token and the
// JWKS entry).
func (s *Signer) Kid() string { return s.kid }

// PublicKey returns the signer's Ed25519 public key (for the JWKS).
func (s *Signer) PublicKey() ed25519.PublicKey { return s.pub }

// Mint produces a signed, compact edge token for params.
func (s *Signer) Mint(p MintParams) (string, error) {
	if p.ContentHost == "" {
		return "", errors.New("edgetoken: empty content host (aud)")
	}
	if p.Subject == "" {
		return "", errors.New("edgetoken: empty subject")
	}
	if p.SiteID == "" {
		return "", errors.New("edgetoken: empty site_id")
	}
	switch p.Mode {
	case ModePassword, ModeAllowlist, ModeOrgOnly:
	default:
		return "", fmt.Errorf("edgetoken: invalid mode %q", p.Mode)
	}
	// TTL == 0 means "unset" → default; a negative TTL is honored verbatim so a
	// caller (e.g. a test) can mint an already-expired token.
	ttl := p.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	jti, err := newJTI()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   p.Subject,
			Audience:  jwt.ClaimStrings{p.ContentHost},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        jti,
		},
		SiteID: p.SiteID,
		Mode:   p.Mode,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = s.kid
	signed, err := tok.SignedString(s.priv)
	if err != nil {
		return "", fmt.Errorf("edgetoken: sign: %w", err)
	}
	return signed, nil
}

// AnonSubject returns a random "anon:<hex>" subject for password-mode tokens (no
// viewer identity). The random suffix is rotatable so anon tokens can be rate-
// limited / revoked per-subject in a later phase.
func AnonSubject() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "anon:" + hex.EncodeToString(b[:]), nil
}

// KidFor derives a stable key id from an Ed25519 public key (first 16 bytes of the
// key, hex). Deterministic so a restart that reuses the key keeps the same kid.
func KidFor(pub ed25519.PublicKey) string {
	if len(pub) == 0 {
		return ""
	}
	n := len(pub)
	if n > 16 {
		n = 16
	}
	return "edge-" + hex.EncodeToString(pub[:n])
}

// newJTI returns a random token id (base64url, no padding).
func newJTI() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
