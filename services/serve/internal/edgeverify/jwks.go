// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package edgeverify is the serving plane's edge-token verifier. It wraps the
// shared internal/edgetoken.Verifier (which already pins alg=EdDSA, iss, aud, a
// required exp, the kid lookup, and a non-empty site_id + valid mode) with the
// three route-bindings it lacks — site_id == route.site_id, mode == route's
// CURRENT access_mode (H1), and the hard-revocation min_iat check — plus a remote
// JWKS client that fetches the edge signer's public keys, caches them with a
// bounded stale-grace, and FAILS CLOSED on a cold-cache fetch error.
//
// This is the Go port of edge/serving-worker/src/edgetoken.ts (loadKeys +
// verifyEdgeToken) layered over the Go edgetoken/edgerevoke contracts.
package edgeverify

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

	"github.com/danielpang/dropway/internal/edgetoken"
)

// Default cache policy, mirroring edgetoken.ts JWKS_TTL_MS / JWKS_STALE_GRACE_MS.
const (
	defaultTTL        = 5 * time.Minute
	defaultStaleGrace = 10 * time.Minute
)

// jwksClient fetches + caches the edge JWKS (OKP/Ed25519 keys keyed by kid). On a
// fetch/parse failure it serves the last-good key set ONLY within a bounded
// stale-grace window past TTL, then FAILS CLOSED. A cold cache with a failing
// fetch returns an error so the caller denies (302) rather than serving on an
// unverifiable token. Safe for concurrent use.
type jwksClient struct {
	url        string
	client     *http.Client
	ttl        time.Duration
	staleGrace time.Duration
	now        func() time.Time

	mu        sync.Mutex
	keys      map[string]ed25519.PublicKey
	expiresAt time.Time // keys valid (no refetch) until here
	haveGood  bool      // a successful fetch has populated keys at least once
}

func newJWKSClient(url string, c *http.Client, now func() time.Time) *jwksClient {
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	if now == nil {
		now = time.Now
	}
	return &jwksClient{
		url:        url,
		client:     c,
		ttl:        defaultTTL,
		staleGrace: defaultStaleGrace,
		now:        now,
	}
}

// loadKeys returns the kid→key map, refetching when the cache is past TTL. On a
// fetch/parse failure it returns the last-good keys within the stale-grace window,
// else an error (fail closed). Mirrors edgetoken.ts loadKeys.
func (j *jwksClient) loadKeys(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	now := j.now()
	if j.haveGood && now.Before(j.expiresAt) {
		return j.keys, nil
	}

	keys, ferr := j.fetch(ctx)
	if ferr != nil || len(keys) == 0 {
		// Fetch/parse failed OR a fetched-but-empty/all-filtered key set: serve the
		// last good keys ONLY within the bounded grace window, then fail closed.
		if j.haveGood && now.Before(j.expiresAt.Add(j.staleGrace)) {
			return j.keys, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("edge JWKS unavailable: %w", ferr)
		}
		return nil, errors.New("edge JWKS has no usable Ed25519 keys")
	}

	j.keys = keys
	j.expiresAt = now.Add(j.ttl)
	j.haveGood = true
	return keys, nil
}

// fetch GETs the JWKS and imports only OKP/Ed25519 keys keyed by kid (mirrors the
// Worker's importJWK filter). A non-200, a transport error, or unparseable JSON
// returns an error; non-Ed25519 / malformed keys are skipped.
func (j *jwksClient) fetch(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("edge JWKS endpoint returned %d", resp.StatusCode)
	}

	var doc edgetoken.JWKS
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}

	keys := make(map[string]ed25519.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid == "" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		keys[k.Kid] = ed25519.PublicKey(raw)
	}
	return keys, nil
}
