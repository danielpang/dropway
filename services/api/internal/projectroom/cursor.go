// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package projectroom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrBadCursor = errors.New("projectroom: bad cursor")

const CursorTTL = 30 * time.Minute
const cursorStartID = "00000000-0000-0000-0000-000000000000"

// CursorCodec authenticates and encrypts cursor payloads. The binding is AEAD
// associated data: a cursor cannot be moved across users, tenants, routes or
// filters. Encryption keeps IDs and traversal timing opaque to consumers.
type CursorCodec struct{ aead cipher.AEAD }

type PageCursor struct {
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Watermark time.Time `json:"watermark"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewCursorCodec derives a purpose-specific key, never a hardcoded default.
func NewCursorCodec(secret []byte) (*CursorCodec, error) {
	if len(secret) < 32 {
		return nil, errors.New("review cursor key must contain at least 32 bytes")
	}
	h := hmac.New(sha256.New, secret)
	h.Write([]byte("dropway/review-cursor/aes-gcm/v1"))
	block, err := aes.NewCipher(h.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CursorCodec{aead: aead}, nil
}

func (c *CursorCodec) Begin(token, binding string, now time.Time, descending bool) (PageCursor, error) {
	if c == nil || c.aead == nil {
		return PageCursor{}, ErrBadCursor
	}
	now = now.UTC()
	if token == "" {
		at := time.Unix(0, 0).UTC()
		id := cursorStartID
		if descending {
			at = now
			id = "ffffffff-ffff-ffff-ffff-ffffffffffff"
		}
		return PageCursor{At: at, ID: id, Watermark: now, ExpiresAt: now.Add(CursorTTL)}, nil
	}
	if len(token) > 4096 {
		return PageCursor{}, ErrBadCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < c.aead.NonceSize()+c.aead.Overhead() {
		return PageCursor{}, ErrBadCursor
	}
	n := c.aead.NonceSize()
	plain, err := c.aead.Open(nil, raw[:n], raw[n:], []byte("v1\x00"+binding))
	if err != nil {
		return PageCursor{}, ErrBadCursor
	}
	var p PageCursor
	if err := json.Unmarshal(plain, &p); err != nil {
		return PageCursor{}, ErrBadCursor
	}
	if _, err := uuid.Parse(p.ID); err != nil {
		return PageCursor{}, ErrBadCursor
	}
	if p.At.IsZero() || p.Watermark.IsZero() || p.At.After(p.Watermark) || p.Watermark.After(now) || !now.Before(p.ExpiresAt) || !p.ExpiresAt.Equal(p.Watermark.Add(CursorTTL)) {
		return PageCursor{}, ErrBadCursor
	}
	return p, nil
}

func (c *CursorCodec) Next(p PageCursor, binding string, at time.Time, kind, id string) (string, error) {
	if c == nil || c.aead == nil {
		return "", ErrBadCursor
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", ErrBadCursor
	}
	p.At, p.Kind, p.ID = at.UTC(), kind, id
	if p.At.After(p.Watermark) {
		return "", ErrBadCursor
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, raw, []byte("v1\x00"+binding))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}
