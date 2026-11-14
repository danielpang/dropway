// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"time"

	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// ReviewActivity contains references only; it never embeds private comment bodies
// or executable preview content.
type ReviewActivity struct {
	ID          string    `json:"id"`
	RoomID      string    `json:"room_id"`
	ActorUserID string    `json:"actor_user_id"`
	Kind        string    `json:"kind"`
	ThreadID    *string   `json:"thread_id,omitempty"`
	SiteID      *string   `json:"site_id,omitempty"`
	VersionID   *string   `json:"version_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type ActivityPage struct {
	Items      []ReviewActivity `json:"items"`
	NextCursor string           `json:"next_cursor"`
	Unread     int64            `json:"unread"`
}

// PageRoomActivity rechecks membership and enabled state on every page. Activity
// referencing detached/deleted sites is excluded by the query, including counts.
func (s *Store) PageRoomActivity(ctx context.Context, t Tenant, roomID, cursor string, limit int) (ActivityPage, error) {
	page, err := s.beginReviewPage(t, "room-activity", roomID, cursor, true)
	at, id := page.At, page.ID
	if err != nil {
		return ActivityPage{}, err
	}
	limit = clampPage(limit)
	out := ActivityPage{Items: []ReviewActivity{}}
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		if _, err := roomMember(ctx, q, t, roomID); err != nil {
			return err
		}
		rows, err := q.ListReviewActivity(ctx, db.ListReviewActivityParams{UserID: t.UserID, OrgID: t.OrgID, RoomID: roomID, CursorAt: at, CursorID: id, Lim: int32(limit + 1), Watermark: page.Watermark})
		if err != nil {
			return err
		}
		rows, more, err := boundedReviewRows(rows, limit)
		if err != nil {
			return err
		}
		if more {
			last := rows[len(rows)-1]
			out.NextCursor, err = s.nextReviewPage(page, last.CreatedAt, "activity", last.ID)
			if err != nil {
				return err
			}
		}
		for _, row := range rows {
			out.Items = append(out.Items, ReviewActivity{ID: row.ID, RoomID: row.RoomID, ActorUserID: row.ActorUserID, Kind: row.Kind, ThreadID: row.ThreadID, SiteID: row.SiteID, VersionID: row.VersionID, CreatedAt: row.CreatedAt})
		}
		out.Unread, err = q.CountUnreadReviewActivity(ctx, db.CountUnreadReviewActivityParams{OrgID: t.OrgID, RoomID: roomID, UserID: t.UserID})
		return err
	})
	return out, err
}

// MarkProjectRoomRead updates only the authenticated human's read state. Reading
// through MCP never acknowledges notifications on behalf of a human.
func (s *Store) MarkProjectRoomRead(ctx context.Context, t Tenant, roomID string) (time.Time, error) {
	if !HumanSessionFromContext(ctx) {
		return time.Time{}, ErrRoomForbidden
	}
	var at time.Time
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		if _, err := roomMember(ctx, q, t, roomID); err != nil {
			return err
		}
		row, err := q.UpsertReviewRead(ctx, db.UpsertReviewReadParams{OrgID: t.OrgID, RoomID: roomID, UserID: t.UserID})
		at = row.LastReadAt
		return err
	})
	return at, err
}
