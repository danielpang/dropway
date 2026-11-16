// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"time"

	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// ExecutionReviewContext preserves the original comment and recent discussion
// as of queue time. Later comments cannot change an already-authorized request.
func (s *Store) ExecutionReviewContext(ctx context.Context, t Tenant, threadID string, at time.Time) (ReplyPage, error) {
	out, err := s.PageReviewReplies(ctx, t, threadID, "", 1)
	if err != nil {
		return out, err
	}
	original := out.Replies
	out.Replies = nil
	err = s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		if _, err := q.GetReadableReviewThread(ctx, db.GetReadableReviewThreadParams{UserID: t.UserID, ID: threadID, OrgID: t.OrgID}); err != nil {
			return ErrNotFound
		}
		rows, err := tx.Query(ctx, `SELECT id::text,author_user_id::text,body,created_at FROM
    (SELECT id,author_user_id,body,created_at FROM app.review_replies
     WHERE org_id=$1 AND thread_id=$2 AND created_at<=$3
     ORDER BY created_at DESC,id DESC LIMIT 8) recent
    ORDER BY created_at,id`, t.OrgID, threadID, at)
		if err != nil {
			return err
		}
		defer rows.Close()
		if len(original) > 0 && !original[0].CreatedAt.After(at) {
			out.Replies = append(out.Replies, original[0])
		}
		for rows.Next() {
			var reply ReviewReply
			if err := rows.Scan(&reply.ID, &reply.AuthorID, &reply.Body, &reply.CreatedAt); err != nil {
				return err
			}
			if len(out.Replies) == 0 || reply.ID != out.Replies[0].ID {
				out.Replies = append(out.Replies, reply)
			}
		}
		return rows.Err()
	})
	return out, err
}
