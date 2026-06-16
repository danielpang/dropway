// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package storeadapter

import (
	"context"

	"github.com/danielpang/dropway/internal/edgerevoke"
)

// RevocationReader adapts an edgerevoke denylist reader (Cloudflare KV or the
// dev/self-host Local writer, both of which read the "revoked:" prefix) to the
// serve verifier's edgeverify.RevocationReader.
type RevocationReader struct {
	lookup DenylistLookup
}

// DenylistLookup is the denylist read surface (read side of the hard-revocation
// denylist): return the stored entry for (kind, id), ok=false on a clean miss, and
// an error on a read failure (the verifier fails closed on the error). Both
// internal/projection.CloudflareKV and *projection.Local satisfy this directly.
type DenylistLookup interface {
	LookupRevoked(ctx context.Context, kind edgerevoke.Kind, id string) (edgerevoke.Value, bool, error)
}

// NewRevocationReader wraps a DenylistLookup.
func NewRevocationReader(lookup DenylistLookup) *RevocationReader {
	return &RevocationReader{lookup: lookup}
}

// MinIAT implements edgeverify.RevocationReader, projecting the denylist Value's
// min_iat. A clean miss is (_, false, nil); a read error propagates so the
// verifier fails closed.
func (r *RevocationReader) MinIAT(ctx context.Context, kind edgerevoke.Kind, id string) (int64, bool, error) {
	v, ok, err := r.lookup.LookupRevoked(ctx, kind, id)
	if err != nil {
		return 0, false, err
	}
	if !ok {
		return 0, false, nil
	}
	return v.MinIAT, true, nil
}
