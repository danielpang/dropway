package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer = "https://app.dropway.test"
	testAud    = "https://api.dropway.test"
)

// jwksServer serves a JWKS for a set of Ed25519 keys and counts hits so tests
// can assert refresh behaviour. Keys can be swapped to simulate rotation.
type jwksServer struct {
	*httptest.Server
	hits atomic.Int64

	mu chan map[string]ed25519.PublicKey // 1-buffered "mutex+value"
}

func newJWKSServer(keys map[string]ed25519.PublicKey) *jwksServer {
	js := &jwksServer{mu: make(chan map[string]ed25519.PublicKey, 1)}
	js.mu <- keys
	js.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		js.hits.Add(1)
		cur := <-js.mu
		js.mu <- cur
		out := jwkSet{}
		for kid, pub := range cur {
			out.Keys = append(out.Keys, jwk{
				Kty: "OKP", Crv: "Ed25519", Kid: kid,
				X: base64.RawURLEncoding.EncodeToString(pub),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	return js
}

func (js *jwksServer) setKeys(keys map[string]ed25519.PublicKey) {
	<-js.mu
	js.mu <- keys
}

func signEdDSA(t *testing.T, priv ed25519.PrivateKey, kid string, claims jwt.Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func goodClaims() Claims {
	return Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Audience:  jwt.ClaimStrings{testAud},
			Subject:   "user_123",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
		},
		OrgID: "org_abc",
		Role:  "admin",
	}
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	return pub, priv
}

// Happy path: a correctly-signed EdDSA token verifies and yields claims.
func TestVerify_ValidEdDSA(t *testing.T) {
	pub, priv := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub})
	defer js.Close()

	v := NewVerifier(js.URL, testIssuer, testAud, WithHTTPClient(js.Client()))
	got, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", goodClaims()))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.UserID() != "user_123" || got.OrgID != "org_abc" || got.Role != "admin" {
		t.Fatalf("claims = %+v", got)
	}
}

// alg:none must be rejected (forged unsigned token).
func TestVerify_RejectsNone(t *testing.T) {
	pub, _ := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub})
	defer js.Close()

	tok := jwt.NewWithClaims(jwt.SigningMethodNone, &jwt.RegisteredClaims{
		Issuer: testIssuer, Audience: jwt.ClaimStrings{testAud}, Subject: "attacker",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	tok.Header["kid"] = "k1"
	raw, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}

	v := NewVerifier(js.URL, testIssuer, testAud, WithHTTPClient(js.Client()))
	if _, err := v.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected alg:none to be rejected, got nil error")
	}
}

// HS256 signed with the public key bytes as the HMAC secret (alg-confusion
// attack) must be rejected because verification is pinned to EdDSA.
func TestVerify_RejectsHS256Confusion(t *testing.T) {
	pub, _ := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub})
	defer js.Close()

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, &jwt.RegisteredClaims{
		Issuer: testIssuer, Audience: jwt.ClaimStrings{testAud}, Subject: "attacker",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	tok.Header["kid"] = "k1"
	raw, err := tok.SignedString([]byte(pub)) // public key as HMAC secret
	if err != nil {
		t.Fatalf("sign hs256: %v", err)
	}

	v := NewVerifier(js.URL, testIssuer, testAud, WithHTTPClient(js.Client()))
	if _, err := v.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected HS256 confusion token to be rejected, got nil error")
	}
}

// An unknown kid triggers exactly one JWKS refresh and then verifies (key
// rotation survival).
func TestVerify_RefreshesOnUnknownKid(t *testing.T) {
	pub1, priv1 := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub1})
	defer js.Close()

	v := NewVerifier(js.URL, testIssuer, testAud,
		WithHTTPClient(js.Client()), WithMinRefreshInterval(0))

	// Prime with k1 so the verifier has a populated cache.
	if err := v.Prime(context.Background()); err != nil {
		t.Fatalf("prime: %v", err)
	}
	hitsAfterPrime := js.hits.Load()

	// Rotate: server now serves k2 only; sign with k2 (unknown to the cache).
	pub2, priv2 := newKey(t)
	_ = priv1
	js.setKeys(map[string]ed25519.PublicKey{"k2": pub2})

	got, err := v.Verify(context.Background(), signEdDSA(t, priv2, "k2", goodClaims()))
	if err != nil {
		t.Fatalf("verify after rotation: %v", err)
	}
	if got.UserID() != "user_123" {
		t.Fatalf("claims = %+v", got)
	}
	if js.hits.Load() <= hitsAfterPrime {
		t.Fatal("expected a JWKS refresh on unknown kid")
	}
}

// M5: a successful fetch that yields ZERO usable keys must NOT wipe the cache — the
// last-known-good keys keep verifying (no fleet-wide auth outage).
func TestVerify_EmptyJWKSKeepsLastKnownGood(t *testing.T) {
	pub1, priv1 := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub1})
	defer js.Close()
	v := NewVerifier(js.URL, testIssuer, testAud,
		WithHTTPClient(js.Client()), WithMinRefreshInterval(0))
	if err := v.Prime(context.Background()); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// The server now publishes NO keys (a transient bad publish / rotation race).
	js.setKeys(map[string]ed25519.PublicKey{})

	// A token with an UNKNOWN kid triggers a refresh that returns an empty set. The
	// refresh must keep the cached keys (M5), so that token fails for "unknown kid"...
	_, priv2 := newKey(t)
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv2, "k2", goodClaims())); err == nil {
		t.Fatal("a token with an unknown kid against an empty JWKS must fail")
	}
	// ...but the previously-good k1 key SURVIVED the empty refresh and still verifies.
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv1, "k1", goodClaims())); err != nil {
		t.Fatalf("k1 must still verify after an empty refresh (last-known-good kept): %v", err)
	}
}

// M4: the refresh rate-limit must apply even while the JWKS endpoint is ERRORING —
// otherwise a flood of unknown (attacker-controlled) kids becomes an unthrottled
// fetch storm. Two unknown-kid verifies within the interval hit the endpoint once.
func TestVerify_RefreshRateLimitedWhenEndpointErrors(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// A long interval so the second attempt within it MUST be throttled.
	v := NewVerifier(srv.URL, testIssuer, testAud,
		WithHTTPClient(srv.Client()), WithMinRefreshInterval(time.Hour))

	_, priv := newKey(t)
	_, _ = v.Verify(context.Background(), signEdDSA(t, priv, "kX", goodClaims()))
	_, _ = v.Verify(context.Background(), signEdDSA(t, priv, "kX", goodClaims()))
	if got := hits.Load(); got != 1 {
		t.Fatalf("JWKS endpoint hit %d times; want exactly 1 (rate-limited despite the 500 — M4)", got)
	}
}

// Expired tokens are rejected (exp is required and enforced).
func TestVerify_RejectsExpired(t *testing.T) {
	pub, priv := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub})
	defer js.Close()

	c := goodClaims()
	c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	v := NewVerifier(js.URL, testIssuer, testAud, WithHTTPClient(js.Client()))
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", c)); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

// Wrong audience is rejected (confused-deputy / token-reuse across services).
func TestVerify_RejectsWrongAudience(t *testing.T) {
	pub, priv := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub})
	defer js.Close()

	c := goodClaims()
	c.Audience = jwt.ClaimStrings{"https://evil.example"}
	v := NewVerifier(js.URL, testIssuer, testAud, WithHTTPClient(js.Client()))
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", c)); err == nil {
		t.Fatal("expected wrong-audience token to be rejected")
	}
}

// WithExtraAudiences accepts ANY of {audience} ∪ extras (e.g. a trailing-slash
// canonicalization from an MCP client) but still rejects an unlisted audience.
func TestVerify_ExtraAudiences(t *testing.T) {
	pub, priv := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub})
	defer js.Close()

	v := NewVerifier(js.URL, testIssuer, testAud,
		WithHTTPClient(js.Client()), WithExtraAudiences(testAud+"/"))

	// The trailing-slash variant is accepted.
	cSlash := goodClaims()
	cSlash.Audience = jwt.ClaimStrings{testAud + "/"}
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", cSlash)); err != nil {
		t.Fatalf("trailing-slash audience should be accepted: %v", err)
	}

	// The primary audience is still accepted.
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", goodClaims())); err != nil {
		t.Fatalf("primary audience should still be accepted: %v", err)
	}

	// An unlisted audience is still rejected (no blanket acceptance).
	cBad := goodClaims()
	cBad.Audience = jwt.ClaimStrings{"https://evil.example"}
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", cBad)); err == nil {
		t.Fatal("unlisted audience must still be rejected with extra audiences set")
	}
}

// MCPResourceAudiences must list exactly the four canonical resource forms (bare,
// trailing slash, /mcp, /mcp/) and ignore a trailing slash on the input — the API
// and the MCP server both build their accepted set from this, so the list is the
// contract that keeps them in sync.
func TestMCPResourceAudiences(t *testing.T) {
	want := []string{
		"https://mcp.dropway.dev",
		"https://mcp.dropway.dev/",
		"https://mcp.dropway.dev/mcp",
		"https://mcp.dropway.dev/mcp/",
	}
	for _, in := range []string{"https://mcp.dropway.dev", "https://mcp.dropway.dev/"} {
		got := MCPResourceAudiences(in)
		if len(got) != len(want) {
			t.Fatalf("MCPResourceAudiences(%q) = %v; want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("MCPResourceAudiences(%q)[%d] = %q; want %q", in, i, got[i], want[i])
			}
		}
	}
}

// Regression: a verifier configured from MCPResourceAudiences accepts a token whose
// aud is the ".../mcp/" connection-URL form (what Claude's built-in connector mints
// and forwards to the API for writes). Before the fix the API only accepted the bare
// and trailing-slash forms, so this token verified at the MCP gate (reads) but 401'd
// at the API (writes). Every form in the set is accepted; an outsider is not.
func TestVerify_AcceptsMCPResourceForms(t *testing.T) {
	pub, priv := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub})
	defer js.Close()

	const resource = "https://mcp.dropway.test"
	auds := MCPResourceAudiences(resource)
	// Primary audience is the API's own; the MCP forms are accepted as extras —
	// exactly how the API wires it (services/api/cmd/api/main.go).
	v := NewVerifier(js.URL, testIssuer, testAud,
		WithHTTPClient(js.Client()), WithExtraAudiences(auds...))

	for _, aud := range append([]string{testAud}, auds...) {
		c := goodClaims()
		c.Audience = jwt.ClaimStrings{aud}
		if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", c)); err != nil {
			t.Fatalf("aud %q should be accepted: %v", aud, err)
		}
	}

	cBad := goodClaims()
	cBad.Audience = jwt.ClaimStrings{resource + "/mcp/evil"}
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", cBad)); err == nil {
		t.Fatal("a non-canonical MCP audience must still be rejected")
	}
}

// UnverifiedAudIss extracts aud/iss without verification (diagnostic logging),
// and degrades to empty strings on garbage input.
func TestUnverifiedAudIss(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"aud":"https://mcp.dropway.test/mcp/","iss":"https://app.dropway.test"}`))
	aud, iss := UnverifiedAudIss("eyJhbGciOiJFZERTQSJ9." + payload + ".sig")
	if aud != `"https://mcp.dropway.test/mcp/"` || iss != "https://app.dropway.test" {
		t.Errorf("UnverifiedAudIss = (%q, %q)", aud, iss)
	}

	// Array-form aud comes back as raw JSON.
	payload = base64.RawURLEncoding.EncodeToString([]byte(`{"aud":["a","b"],"iss":"i"}`))
	aud, iss = UnverifiedAudIss("h." + payload + ".s")
	if aud != `["a","b"]` || iss != "i" {
		t.Errorf("array aud: UnverifiedAudIss = (%q, %q)", aud, iss)
	}

	for _, bad := range []string{"", "not-a-jwt", "a.!!!.c", "a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c"} {
		if aud, iss := UnverifiedAudIss(bad); aud != "" || iss != "" {
			t.Errorf("UnverifiedAudIss(%q) = (%q, %q), want empty", bad, aud, iss)
		}
	}
}
