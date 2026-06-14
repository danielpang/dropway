// Package auth implements verification of Better Auth-issued EdDSA JWTs against
// a remote JWKS endpoint. This is the Phase-0 "JWKS/JWT spike" from the
// architecture plan (docs/ARCHITECTURE.md §13): the Go API is the authz
// boundary, so it must verify every JWT itself with the algorithm pinned to
// EdDSA, explicitly rejecting `none` and HMAC ("alg confusion") tokens, and
// must survive key rotation by refreshing the JWKS on an unknown `kid`.
package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// allowedAlg is the ONLY accepted signing algorithm. jwt.WithValidMethods pins
// verification to this set, which rejects `alg: none` and any HMAC family
// (HS256/384/512) — the classic public-key-as-HMAC-secret confusion attack.
const allowedAlg = "EdDSA"

// Claims is the verified token payload. Better Auth's JWT plugin carries the
// subject (user id); org_id and role are injected via the organization plugin /
// custom claims. Email/EmailVerified back the allowlist authz path (a grant is
// honored only for a VERIFIED email — ARCHITECTURE.md §10 [HIGH]). These are a fast
// hint — sensitive writes re-check live tables (the "confused-deputy guard").
type Claims struct {
	jwt.RegisteredClaims
	OrgID         string `json:"org_id,omitempty"`
	Role          string `json:"role,omitempty"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
}

// UserID returns the token subject (Better Auth user id).
func (c *Claims) UserID() string { return c.Subject }

// Verifier verifies EdDSA JWTs against a cached JWKS. It is safe for concurrent
// use. Keys are refreshed lazily on an unknown kid, rate-limited so a flood of
// bogus kids cannot turn into a JWKS-fetch DoS.
type Verifier struct {
	jwksURL  string
	issuer   string
	audience string
	client   *http.Client

	minRefreshInterval time.Duration

	mu          sync.RWMutex
	keys        map[string]ed25519.PublicKey
	lastRefresh time.Time
}

// Option configures a Verifier.
type Option func(*Verifier)

// WithHTTPClient overrides the HTTP client used to fetch the JWKS (tests inject
// the httptest client).
func WithHTTPClient(c *http.Client) Option { return func(v *Verifier) { v.client = c } }

// WithMinRefreshInterval sets the minimum spacing between JWKS refreshes
// triggered by unknown kids (rate-limit / anti-DoS). Default 15s.
func WithMinRefreshInterval(d time.Duration) Option {
	return func(v *Verifier) { v.minRefreshInterval = d }
}

// NewVerifier builds a Verifier for the given JWKS URL, expected issuer and
// audience. issuer/audience are enforced on every token.
func NewVerifier(jwksURL, issuer, audience string, opts ...Option) *Verifier {
	v := &Verifier{
		jwksURL:            jwksURL,
		issuer:             issuer,
		audience:           audience,
		client:             &http.Client{Timeout: 5 * time.Second},
		minRefreshInterval: 15 * time.Second,
		keys:               map[string]ed25519.PublicKey{},
	}
	for _, o := range opts {
		o(v)
	}
	return v
}

// ErrUnknownKey is returned when the token's kid is absent even after a refresh.
var ErrUnknownKey = errors.New("auth: unknown signing key id")

// Verify parses and fully validates a token string, returning its claims.
// It enforces: EdDSA signature, matching issuer & audience, and a present,
// unexpired `exp`. On an unknown `kid` it refreshes the JWKS once (rate-limited)
// before failing, so freshly rotated keys are picked up automatically.
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	claims := &Claims{}

	keyfunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if key, ok := v.lookup(kid); ok {
			return key, nil
		}
		// Unknown kid: refresh once (rate-limited) and retry the lookup.
		if err := v.refresh(ctx, false); err != nil {
			return nil, fmt.Errorf("auth: jwks refresh: %w", err)
		}
		if key, ok := v.lookup(kid); ok {
			return key, nil
		}
		return nil, ErrUnknownKey
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{allowedAlg}), // rejects none + HMAC
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
	)
	if _, err := parser.ParseWithClaims(token, claims, keyfunc); err != nil {
		return nil, err
	}
	return claims, nil
}

func (v *Verifier) lookup(kid string) (ed25519.PublicKey, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	k, ok := v.keys[kid]
	return k, ok
}

// jwk is a single Ed25519 (OKP) key as it appears in a JWKS document.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// refresh fetches and parses the JWKS. Unless force is true, refreshes that land
// within minRefreshInterval of the previous one are skipped (anti-DoS).
func (v *Verifier) refresh(ctx context.Context, force bool) error {
	v.mu.Lock()
	if !force && !v.lastRefresh.IsZero() && time.Since(v.lastRefresh) < v.minRefreshInterval {
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: jwks endpoint returned %d", resp.StatusCode)
	}

	var set jwkSet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return err
	}

	keys := make(map[string]ed25519.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid == "" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		keys[k.Kid] = ed25519.PublicKey(raw)
	}

	v.mu.Lock()
	v.keys = keys
	v.lastRefresh = time.Now()
	v.mu.Unlock()
	return nil
}

// Prime eagerly loads the JWKS (e.g. at startup) so the first request doesn't
// pay the fetch.
func (v *Verifier) Prime(ctx context.Context) error { return v.refresh(ctx, true) }
