// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"strings"

	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5"
)

type MentionExecution struct {
	CredentialRef  string        `json:"credential_ref"`
	Provider       string        `json:"provider"`
	ModelID        string        `json:"model_id"`
	Budget         hosted.Budget `json:"budget"`
	IdempotencyKey string        `json:"idempotency_key"`
}

// AddMentionReply atomically records the human request and queues one bounded
// revision. Neither posting ordinary feedback nor AI output calls this path.
func (s *Store) AddMentionReply(ctx context.Context, t Tenant, roomID, threadID, body string, run MentionExecution) (ReviewReply, ExecutionJob, error) {
	var reply ReviewReply
	var job ExecutionJob
	body = strings.TrimSpace(body)
	if !HumanSessionFromContext(ctx) || !hosted.HasDropwayMention(body) || len(run.IdempotencyKey) < 8 || len(run.IdempotencyKey) > 112 || len(body) > 4000 || hasControl(body) {
		return reply, job, hosted.ErrInvalid
	}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		th, err := q.GetReviewThreadForMember(ctx, db.GetReviewThreadForMemberParams{UserID: t.UserID, ID: threadID, OrgID: t.OrgID, RoomID: roomID})
		if err != nil {
			return hosted.ErrDenied
		}
		config := hosted.Config{Provider: run.Provider, ModelID: run.ModelID, Prompt: body, ThreadID: threadID, Budget: run.Budget}
		job, err = s.queueExecutionTx(ctx, t, tx, q, roomID, th.SiteID, th.VersionID, run.CredentialRef, "mention:"+run.IdempotencyKey, config)
		if err != nil {
			return err
		}
		var replyID *string
		if err := tx.QueryRow(ctx, `SELECT trigger_reply_id::text FROM app.execution_jobs WHERE org_id=$1 AND id=$2`, t.OrgID, job.ID).Scan(&replyID); err != nil {
			return err
		}
		if replyID != nil {
			if err := tx.QueryRow(ctx, `SELECT id::text,author_user_id::text,body,created_at FROM app.review_replies WHERE org_id=$1 AND id=$2 AND thread_id=$3`, t.OrgID, *replyID, threadID).Scan(&reply.ID, &reply.AuthorID, &reply.Body, &reply.CreatedAt); err != nil {
				return err
			}
			if reply.Body != body || reply.AuthorID != t.UserID {
				return hosted.ErrConflict
			}
			return nil
		}
		row, err := q.InsertReviewReply(ctx, db.InsertReviewReplyParams{OrgID: t.OrgID, ThreadID: threadID, AuthorUserID: t.UserID, Body: body})
		if err != nil {
			return err
		}
		reply = ReviewReply{ID: row.ID, AuthorID: row.AuthorUserID, Body: row.Body, CreatedAt: row.CreatedAt}
		if _, err := tx.Exec(ctx, `UPDATE app.execution_jobs SET trigger_reply_id=$3 WHERE org_id=$1 AND id=$2`, t.OrgID, job.ID, reply.ID); err != nil {
			return err
		}
		return writeActivity(ctx, q, t, roomID, "reply_created", &threadID, &th.SiteID, &th.VersionID)
	})
	return reply, job, err
}
