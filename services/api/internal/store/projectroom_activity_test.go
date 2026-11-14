// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"errors"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"testing"
	"time"
)

func TestReviewCursorBinding(t *testing.T) {
	s := New(nil, nil)
	tenant := Tenant{OrgID: "org", UserID: "user"}
	page, err := s.beginReviewPage(tenant, "room-activity", "room", "", true)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.nextReviewPage(page, page.Watermark.Add(-time.Second), "activity", "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.beginReviewPage(tenant, "room-activity", "room", token, true); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		tenant            Tenant
		namespace, target string
	}{
		{Tenant{OrgID: "other", UserID: "user"}, "room-activity", "room"},
		{Tenant{OrgID: "org", UserID: "other"}, "room-activity", "room"},
		{tenant, "room-threads", "room"}, {tenant, "room-activity", "other"},
	}
	for _, tc := range cases {
		if _, err := s.beginReviewPage(tc.tenant, tc.namespace, tc.target, token, true); !errors.Is(err, projectroom.ErrBadCursor) {
			t.Fatalf("accepted foreign binding %+v: %v", tc, err)
		}
	}
}
func TestMarkRoomReadRequiresHumanBeforeDatabase(t *testing.T) {
	var s Store
	for _, ctx := range []context.Context{context.Background(), WithHumanSession(context.Background(), false)} {
		if _, err := s.MarkProjectRoomRead(ctx, Tenant{}, "room"); !errors.Is(err, ErrRoomForbidden) {
			t.Fatalf("unclassified mark-read = %v", err)
		}
	}
}
