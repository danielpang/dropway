// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projectroom

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// ErrBadCursor is a client error for a page cursor this API did not issue.
var ErrBadCursor = errors.New("projectroom: bad cursor")

const cursorStartID = "00000000-0000-0000-0000-000000000000"

// EncodePageCursor returns an opaque cursor for (time, kind, id).
func EncodePageCursor(at time.Time, kind, id string) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "\n" + kind + "\n" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodePageCursor parses a cursor. An empty cursor is the start of the sequence.
func DecodePageCursor(s string) (time.Time, string, string, error) {
	if s == "" {
		return time.Unix(0, 0).UTC(), "", cursorStartID, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", "", ErrBadCursor
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) != 3 || parts[2] == "" {
		return time.Time{}, "", "", ErrBadCursor
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", "", ErrBadCursor
	}
	return at, parts[1], parts[2], nil
}
