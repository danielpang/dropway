// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgetoken

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

func TestReviewCursorKey(t *testing.T) {
	seed := bytes.Repeat([]byte{1}, ed25519.SeedSize)
	a, err := NewSigner(ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSigner(ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatal(err)
	}
	key := a.ReviewCursorKey()
	if len(key) != 32 || bytes.Equal(key, seed) || !bytes.Equal(key, b.ReviewCursorKey()) {
		t.Fatal("cursor key must be stable, purpose-separated, and 32 bytes")
	}
	key[0] ^= 1
	if !bytes.Equal(a.ReviewCursorKey(), b.ReviewCursorKey()) {
		t.Fatal("returned key aliases signer state")
	}
	c, err := NewSigner(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.ReviewCursorKey(), c.ReviewCursorKey()) {
		t.Fatal("rotated signer must invalidate review cursors")
	}
}
