// Package auth implements verification of Better Auth-issued EdDSA JWTs against
// a remote JWKS endpoint. This is the Phase-0 "JWKS/JWT spike" from the
// architecture plan: the Go API is the authz
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
	"strings"
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
// honored only for a VERIFIED email — [HIGH]). These are a fast
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
	// extraAudiences are ADDITIONAL accepted `aud` values beyond `audience`. When
	// non-empty the verifier accepts a token whose audience matches ANY of
	// {audience} ∪ extraAudiences. Used by the MCP server, whose OAuth clients
	// canonicalize the resource URL differently (e.g. mcp-remote appends a trailing
	// slash: "http://host" → "http://host/"). Empty by default → the API keeps its
	// strict single-audience check unchanged.
	extraAudiences []string
	client         *http.Client

	minRefreshInterval time.Duration

	mu          sync.RWMutex
	keys        map[string]ed25519.PublicKey
	lastRefresh time.Time // last SUCCESSFUL refresh (observability)
	lastAttempt time.Time // last refresh ATTEMPT (success OR failure) — the rate-limit gate
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

// WithExtraAudiences adds ADDITIONAL accepted `aud` values beyond the primary one
// passed to NewVerifier. A token is accepted if its audience matches ANY of them.
// Use this when clients may send the resource identifier in more than one canonical
// form (e.g. with/without a trailing slash). With no extra audiences the verifier
// keeps its strict single-audience behavior.
func WithExtraAudiences(auds ...string) Option {
	return func(v *Verifier) { v.extraAudiences = append(v.extraAudiences, auds...) }
}

// MCPResourceAudiences returns every canonical `aud` form an OAuth client may mint
// for the Dropway MCP resource at publicURL: the bare URL, a trailing-slash variant,
// and the ".../mcp" and ".../mcp/" connection-URL forms (the last is the RFC 8707
// resource Claude's built-in connector requests). Better Auth issues the token with
// whichever form the client sent, so BOTH the MCP server's own gate AND the Go API
// (which accepts forwarded MCP tokens for control-plane writes) must accept this same
// set — otherwise a token minted for, e.g., ".../mcp/" verifies at the MCP (reads
// work) but 401s at the API (writes fail). Centralized here so the two call sites
// (services/mcp + services/api) cannot drift. publicURL's trailing slash is ignored.
func MCPResourceAudiences(publicURL string) []string {
	base := strings.TrimRight(publicURL, "/")
	return []string{base, base + "/", base + "/mcp", base + "/mcp/"}
}

// UnverifiedAudIss decodes a JWT payload WITHOUT verifying the signature,
// returning the `aud` and `iss` claims for DIAGNOSTIC LOGGING ONLY (never an
// authz decision). It lets an audience/issuer mismatch be read straight from the
// logs when a token is rejected — the difference between a diagnosable 401 and a
// silent one. Returns empty strings when the token can't be parsed.
func UnverifiedAudIss(token string) (aud, iss string) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var p struct {
		Aud json.RawMessage `json:"aud"` // string or []string — logged as-is
		Iss string          `json:"iss"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", ""
	}
	return string(p.Aud), p.Iss
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

	parserOpts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{allowedAlg}), // rejects none + HMAC
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
	}
	// With no extra audiences keep the library's strict single-audience check (the
	// API path, unchanged). With extras, validate the audience ourselves against the
	// allowed set after parsing.
	if len(v.extraAudiences) == 0 {
		parserOpts = append(parserOpts, jwt.WithAudience(v.audience))
	}
	parser := jwt.NewParser(parserOpts...)
	if _, err := parser.ParseWithClaims(token, claims, keyfunc); err != nil {
		return nil, err
	}
	if len(v.extraAudiences) > 0 && !v.audienceAllowed(claims.Audience) {
		return nil, jwt.ErrTokenInvalidAudience
	}
	return claims, nil
}

// audienceAllowed reports whether the token's audience claim contains any of the
// accepted audiences ({audience} ∪ extraAudiences). Only consulted when extra
// audiences are configured.
func (v *Verifier) audienceAllowed(aud jwt.ClaimStrings) bool {
	for _, a := range aud {
		if a == v.audience {
			return true
		}
		for _, e := range v.extraAudiences {
			if a == e {
				return true
			}
		}
	}
	return false
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
// within minRefreshInterval of the previous ATTEMPT are skipped (anti-DoS).
//
// The rate-limit gate keys off lastAttempt, which is stamped BEFORE the fetch and
// thus on every attempt — success OR failure (M4). Stamping only on success (the old
// behavior) meant that while the JWKS endpoint was erroring, every unknown-kid token
// (the kid is attacker-controlled) fired an unthrottled outbound GET — turning a
// flood of forged kids into a fetch storm against the already-unhealthy endpoint.
//
// A successful fetch that yields ZERO usable keys does NOT overwrite the cache (M5):
// replacing live keys with an empty map would fail ALL verification (a fleet-wide
// control-plane auth outage) until the next refresh, which the rate-limit then
// suppresses. We keep the last-known-good keys and return an error instead.
func (v *Verifier) refresh(ctx context.Context, force bool) error {
	v.mu.Lock()
	if !force && !v.lastAttempt.IsZero() && time.Since(v.lastAttempt) < v.minRefreshInterval {
		v.mu.Unlock()
		return nil
	}
	// Stamp the attempt NOW, under the lock, so the gate throttles even when the fetch
	// below fails. Only one goroutine per interval gets past the gate to fetch.
	v.lastAttempt = time.Now()
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

	// M5: never clobber a live key set with an empty one — keep last-known-good.
	if len(keys) == 0 {
		return fmt.Errorf("auth: jwks returned no usable Ed25519 keys; keeping last-known-good")
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
