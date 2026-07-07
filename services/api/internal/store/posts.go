// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"time"

	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// Feed post subject kinds (the polymorphic subject_type on post_votes /
// post_comments). A feed post is either a site or a skill.
const (
	SubjectSite  = "site"
	SubjectSkill = "skill"
)

// maxCommentsPerThread bounds how many comments a single thread read returns (the
// most recent ones), so a long-lived thread can't load an unbounded result into
// memory or over the wire. Full pagination is a future follow-up.
const maxCommentsPerThread = 200

// PostComment is one org-internal comment on a feed post (a site or a skill),
// optionally tagging teammates. SubjectType is SubjectSite / SubjectSkill.
type PostComment struct {
	ID               string
	OrgID            string
	SubjectType      string
	SubjectID        string
	AuthorUserID     string
	Body             string
	MentionedUserIDs []string
	CreatedAt        time.Time
}

// CreatePostCommentParams is the input to CreatePostComment.
type CreatePostCommentParams struct {
	SubjectType      string
	SubjectID        string
	Body             string
	MentionedUserIDs []string
}

// CreatePostComment appends a comment to a feed post's thread under the active
// tenant. RLS (the tenant WITH CHECK policy) guarantees the row lands under the
// caller's org. MentionedUserIDs is stored as-is (the handler validates they're
// org members first). Returns the persisted comment.
func (s *Store) CreatePostComment(ctx context.Context, t Tenant, p CreatePostCommentParams) (PostComment, error) {
	mentions := p.MentionedUserIDs
	if mentions == nil {
		mentions = []string{}
	}
	var out PostComment
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.CreatePostComment(ctx, db.CreatePostCommentParams{
			OrgID:            t.OrgID,
			SubjectType:      p.SubjectType,
			SubjectID:        p.SubjectID,
			AuthorUserID:     t.UserID,
			Body:             p.Body,
			MentionedUserIds: mentions,
		})
		if err != nil {
			return err
		}
		out = commentFromDB(row)
		return nil
	})
	return out, err
}

// ListPostComments returns a feed post's comment thread, oldest first. RLS scopes
// the read to the active org (another org's subject resolves to an empty thread).
func (s *Store) ListPostComments(ctx context.Context, t Tenant, subjectType, subjectID string) ([]PostComment, error) {
	var out []PostComment
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListPostComments(ctx, db.ListPostCommentsParams{
			SubjectType: subjectType,
			SubjectID:   subjectID,
			Limit:       maxCommentsPerThread,
		})
		if err != nil {
			return err
		}
		out = make([]PostComment, len(rows))
		for i, r := range rows {
			out[i] = commentFromDB(r)
		}
		return nil
	})
	return out, err
}

func commentFromDB(r db.AppPostComment) PostComment {
	mentions := r.MentionedUserIds
	if mentions == nil {
		mentions = []string{}
	}
	return PostComment{
		ID:               r.ID,
		OrgID:            r.OrgID,
		SubjectType:      r.SubjectType,
		SubjectID:        r.SubjectID,
		AuthorUserID:     r.AuthorUserID,
		Body:             r.Body,
		MentionedUserIDs: mentions,
		CreatedAt:        r.CreatedAt,
	}
}

// SetPostVote records the caller's vote on a feed post (site or skill): value +1
// (up) or -1 (down) upserts the single (subject, user) row; value 0 removes it
// (un-vote). It returns the post's new net score and the caller's resulting vote.
// RLS scopes the writes to the active org.
func (s *Store) SetPostVote(ctx context.Context, t Tenant, subjectType, subjectID string, value int) (score int64, myVote int, err error) {
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if value == 0 {
			if derr := q.DeletePostVote(ctx, db.DeletePostVoteParams{
				SubjectType: subjectType,
				SubjectID:   subjectID,
				UserID:      t.UserID,
			}); derr != nil {
				return derr
			}
		} else {
			if uerr := q.UpsertPostVote(ctx, db.UpsertPostVoteParams{
				SubjectType: subjectType,
				SubjectID:   subjectID,
				OrgID:       t.OrgID,
				UserID:      t.UserID,
				Value:       int16(value),
			}); uerr != nil {
				return uerr
			}
		}
		sc, serr := q.GetPostVoteScore(ctx, db.GetPostVoteScoreParams{
			SubjectType: subjectType,
			SubjectID:   subjectID,
		})
		if serr != nil {
			return serr
		}
		score = sc
		myVote = value
		return nil
	})
	return score, myVote, err
}

// deletePostSubjectTx drops a subject's votes + comments inside an open tenant tx.
// The polymorphic tables can't FK-cascade to two parents, so a subject's delete
// path (currently only DeleteSkill — sites are never deleted) calls this to keep
// the feed-social tables from orphaning rows.
func deletePostSubjectTx(ctx context.Context, q *db.Queries, subjectType, subjectID string) error {
	if err := q.DeletePostVotesForSubject(ctx, db.DeletePostVotesForSubjectParams{
		SubjectType: subjectType,
		SubjectID:   subjectID,
	}); err != nil {
		return err
	}
	return q.DeletePostCommentsForSubject(ctx, db.DeletePostCommentsForSubjectParams{
		SubjectType: subjectType,
		SubjectID:   subjectID,
	})
}
