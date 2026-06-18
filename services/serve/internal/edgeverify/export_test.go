// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgeverify

import (
	"net/http"
	"time"
)

// NewRemoteForTest builds a Verifier backed by a remote JWKS client with an
// injected HTTP client + clock, so tests can exercise the cache TTL / stale-grace
// / cold-cache-fail-closed behavior without real time or network. Internal test
// seam (package-internal so it cannot leak into production callers).
func NewRemoteForTest(jwksURL string, client *http.Client, now func() time.Time, revoked RevocationReader) *Verifier {
	jc := newJWKSClient(jwksURL, client, now)
	return &Verifier{keys: jc, revoked: revoked}
}
