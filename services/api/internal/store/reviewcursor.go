// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"encoding/json"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"time"
)

// SetCursorKey must be called before serving requests. Replicas must use the
// same secret to accept each other's cursors; key rotation invalidates old ones.
func (s *Store) SetCursorKey(secret []byte) error {
	codec, err := projectroom.NewCursorCodec(secret)
	if err != nil {
		return err
	}
	s.reviewCursors = codec
	return nil
}

type reviewPage struct {
	projectroom.PageCursor
	binding string
}

func (s *Store) beginReviewPage(t Tenant, namespace, target, cursor string, descending bool) (reviewPage, error) {
	// No version/resolved filters are exposed on these store methods. If filters
	// are added, their canonical representation must also enter this binding.
	binding, _ := json.Marshal([]string{t.OrgID, t.UserID, namespace, target, "all"})
	p, err := s.reviewCursors.Begin(cursor, string(binding), time.Now(), descending)
	return reviewPage{PageCursor: p, binding: string(binding)}, err
}
func (s *Store) nextReviewPage(p reviewPage, at time.Time, kind, id string) (string, error) {
	return s.reviewCursors.Next(p.PageCursor, p.binding, at, kind, id)
}
