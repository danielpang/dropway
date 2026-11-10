// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgetoken

import (
	"crypto/hmac"
	"crypto/sha256"
)

// ReviewCursorKey derives a separate symmetric key for authenticated review
// pagination. It never exposes the signing seed, and stays stable across API
// replicas/restarts using the same EDGE_SIGNING_KEY. Rotating that key invalidates
// outstanding cursors; callers must start a fresh traversal.
func (s *Signer) ReviewCursorKey() []byte {
	mac := hmac.New(sha256.New, s.priv.Seed())
	mac.Write([]byte("dropway/review-cursor/v1"))
	return mac.Sum(nil)
}
