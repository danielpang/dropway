// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgeverify

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/edgerevoke"
	"github.com/danielpang/dropway/internal/edgetoken"
)

const (
	host   = "acme.dropwaycontent.com"
	siteID = "22222222-2222-2222-2222-222222222222"
	orgID  = "11111111-1111-1111-1111-111111111111"
)

func newSigner(t *testing.T) *edgetoken.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := edgetoken.NewSigner(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// permissiveRevoked never revokes (clean miss on every dimension).
type permissiveRevoked struct{}

func (permissiveRevoked) MinIAT(context.Context, edgerevoke.Kind, string) (int64, bool, error) {
	return 0, false, nil
}

func mintTok(t *testing.T, s *edgetoken.Signer, h, site, mode string, ttl time.Duration) string {
	t.Helper()
	tok, err := s.Mint(edgetoken.MintParams{ContentHost: h, Subject: "u1", SiteID: site, Mode: mode, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestVerify_StaticKeysHappyPath(t *testing.T) {
	s := newSigner(t)
	v := NewForSigner(s, permissiveRevoked{})
	tok := mintTok(t, s, host, siteID, edgetoken.ModeOrgOnly, time.Minute)

	res, ok := v.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID)
	if !ok {
		t.Fatalf("valid token rejected")
	}
	if res.Sub != "u1" || res.SiteID != siteID || res.Mode != edgetoken.ModeOrgOnly {
		t.Errorf("claims = %+v", res)
	}
}

// jwksHandler serves the signer's JWKS and counts requests so we can assert TTL
// caching + refetch behavior.
func jwksHandler(s *edgetoken.Signer, fail *atomic.Bool, calls *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		b, _ := s.JWKSJSON()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

func TestVerify_RemoteJWKS_ColdCacheFailsClosed(t *testing.T) {
	s := newSigner(t)
	var fail atomic.Bool
	var calls atomic.Int64
	fail.Store(true) // endpoint is down from the start (cold cache)
	srv := httptest.NewServer(jwksHandler(s, &fail, &calls))
	defer srv.Close()

	now := time.Now()
	v := NewRemoteForTest(srv.URL, srv.Client(), func() time.Time { return now }, permissiveRevoked{})
	tok := mintTok(t, s, host, siteID, edgetoken.ModeOrgOnly, time.Minute)

	if _, ok := v.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); ok {
		t.Fatalf("cold-cache JWKS outage must FAIL CLOSED (deny)")
	}
}

func TestVerify_RemoteJWKS_StaleGraceThenFailClosed(t *testing.T) {
	s := newSigner(t)
	var fail atomic.Bool
	var calls atomic.Int64
	srv := httptest.NewServer(jwksHandler(s, &fail, &calls))
	defer srv.Close()

	cur := time.Now()
	clock := func() time.Time { return cur }
	v := NewRemoteForTest(srv.URL, srv.Client(), clock, permissiveRevoked{})
	tok := mintTok(t, s, host, siteID, edgetoken.ModeOrgOnly, time.Hour)

	// 1. Warm the cache with a successful fetch.
	if _, ok := v.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); !ok {
		t.Fatalf("initial verify should succeed")
	}

	// 2. Endpoint goes down; advance past TTL but within stale-grace → still serves.
	fail.Store(true)
	cur = cur.Add(defaultTTL + time.Minute) // past TTL (5m), within grace (10m)
	if _, ok := v.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); !ok {
		t.Fatalf("within stale-grace, last-good keys should still verify")
	}

	// 3. Advance past TTL+grace with the endpoint still down → fail closed.
	cur = cur.Add(defaultTTL + defaultStaleGrace + time.Minute)
	if _, ok := v.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); ok {
		t.Fatalf("past TTL+grace with a down endpoint must fail closed")
	}
}

func TestVerify_RouteBindingsAndRevocation(t *testing.T) {
	s := newSigner(t)

	// site_id mismatch.
	v := NewForSigner(s, permissiveRevoked{})
	tok := mintTok(t, s, host, "99999999-9999-9999-9999-999999999999", edgetoken.ModeOrgOnly, time.Minute)
	if _, ok := v.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); ok {
		t.Errorf("site_id mismatch should be rejected")
	}

	// mode mismatch (token password, route org_only).
	tok = mintTok(t, s, host, siteID, edgetoken.ModePassword, time.Minute)
	if _, ok := v.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); ok {
		t.Errorf("mode mismatch should be rejected")
	}

	// wrong aud.
	tok = mintTok(t, s, "other.dropwaycontent.com", siteID, edgetoken.ModeOrgOnly, time.Minute)
	if _, ok := v.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); ok {
		t.Errorf("wrong aud should be rejected")
	}

	// revoked by org min_iat.
	tok = mintTok(t, s, host, siteID, edgetoken.ModeOrgOnly, time.Minute)
	future := time.Now().Add(time.Hour).Unix()
	revOrg := mapRevoked{"org:" + orgID: future}
	v2 := NewForKeys(map[string]ed25519.PublicKey{s.Kid(): s.PublicKey()}, revOrg)
	if _, ok := v2.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); ok {
		t.Errorf("org-revoked token should be rejected")
	}

	// nil revocation reader fails closed.
	v3 := NewForKeys(map[string]ed25519.PublicKey{s.Kid(): s.PublicKey()}, nil)
	if _, ok := v3.Verify(context.Background(), tok, host, siteID, edgetoken.ModeOrgOnly, orgID); ok {
		t.Errorf("nil revocation reader should fail closed")
	}
}

// mapRevoked is a simple revocation reader; "kind:id" → min_iat.
type mapRevoked map[string]int64

func (m mapRevoked) MinIAT(_ context.Context, kind edgerevoke.Kind, id string) (int64, bool, error) {
	v, ok := m[string(kind)+":"+id]
	return v, ok, nil
}
