// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package projectroom

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func TestAuthenticatedOpaqueCursor(t *testing.T) {
	secret := bytes.Repeat([]byte{41}, 32)
	codec, err := NewCursorCodec(secret)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	page, err := codec.Begin("", "caller/site/namespace/filters", now, false)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(-time.Minute)
	token, err := codec.Next(page, "caller/site/namespace/filters", at, "feed", "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	// Replicas use the same key; restarts do not invalidate configured cursors.
	replica, _ := NewCursorCodec(secret)
	got, err := replica.Begin(token, "caller/site/namespace/filters", now.Add(time.Minute), false)
	if err != nil || got.At != at || !got.Watermark.Equal(now) || !got.ExpiresAt.Equal(now.Add(CursorTTL)) {
		t.Fatalf("round trip %+v %v", got, err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	if bytes.Contains(raw, []byte("11111111")) || bytes.Contains(raw, []byte("feed")) {
		t.Fatal("cursor payload is not opaque")
	}
	raw[len(raw)-1] ^= 1
	for _, bad := range []string{base64.RawURLEncoding.EncodeToString(raw), "unsigned", "", token} {
		if bad == "" {
			continue
		}
		binding := "caller/site/namespace/filters"
		if bad == token {
			binding = "other caller/site/namespace/filters"
		}
		if _, err := replica.Begin(bad, binding, now, false); !errors.Is(err, ErrBadCursor) {
			t.Fatalf("accepted tamper/binding: %v", err)
		}
	}
	if _, err := replica.Begin(token, "caller/site/namespace/filters", now.Add(CursorTTL), false); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("accepted expired cursor %v", err)
	}
	if _, err := replica.Begin(token, "caller/site/namespace/filters", now.Add(-time.Second), false); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("accepted future watermark %v", err)
	}
	rotated, _ := NewCursorCodec(bytes.Repeat([]byte{42}, 32))
	if _, err := rotated.Begin(token, "caller/site/namespace/filters", now, false); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("accepted rotated key %v", err)
	}
	next, err := replica.Next(got, "caller/site/namespace/filters", at.Add(time.Second), "room", "22222222-2222-2222-2222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	nextPage, err := replica.Begin(next, "caller/site/namespace/filters", now.Add(2*time.Minute), false)
	if err != nil || !nextPage.Watermark.Equal(now) || !nextPage.ExpiresAt.Equal(page.ExpiresAt) {
		t.Fatalf("next extended traversal lifetime: %+v %v", nextPage, err)
	}
}
func TestCursorKeyFailsClosed(t *testing.T) {
	for _, secret := range [][]byte{nil, make([]byte, 31)} {
		if _, err := NewCursorCodec(secret); err == nil {
			t.Fatal("accepted short key")
		}
	}
	var codec *CursorCodec
	if _, err := codec.Begin("", "binding", time.Now(), false); !errors.Is(err, ErrBadCursor) {
		t.Fatal("unconfigured codec accepted cursor")
	}
}
