// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"time"

	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// maxCommentsPerThread bounds how many comments a single thread read returns (the
// most recent ones), so a long-lived thread can't load an unbounded result into
// memory or over the wire. Full pagination is a future follow-up.
const maxCommentsPerThread = 200

// SiteComment is one org-internal comment on a site, optionally tagging teammates.
type SiteComment struct {
	ID               string
	OrgID            string
	SiteID           string
	AuthorUserID     string
	Body             string
	MentionedUserIDs []string
	CreatedAt        time.Time
}

// CreateSiteCommentParams is the input to CreateSiteComment.
type CreateSiteCommentParams struct {
	SiteID           string
	Body             string
	MentionedUserIDs []string
}

// CreateSiteComment appends a comment to a site's thread under the active tenant.
// RLS (the tenant WITH CHECK policy) guarantees the row lands under the caller's
// org. MentionedUserIDs is stored as-is (the handler validates they're org members
// first). Returns the persisted comment.
func (s *Store) CreateSiteComment(ctx context.Context, t Tenant, p CreateSiteCommentParams) (SiteComment, error) {
	mentions := p.MentionedUserIDs
	if mentions == nil {
		mentions = []string{}
	}
	var out SiteComment
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.CreateSiteComment(ctx, db.CreateSiteCommentParams{
			OrgID:            t.OrgID,
			SiteID:           p.SiteID,
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

// ListSiteComments returns a site's comment thread, oldest first. RLS scopes the
// read to the active org (another org's site resolves to an empty thread).
func (s *Store) ListSiteComments(ctx context.Context, t Tenant, siteID string) ([]SiteComment, error) {
	var out []SiteComment
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListSiteComments(ctx, db.ListSiteCommentsParams{
			SiteID: siteID,
			Limit:  maxCommentsPerThread,
		})
		if err != nil {
			return err
		}
		out = make([]SiteComment, len(rows))
		for i, r := range rows {
			out[i] = commentFromDB(r)
		}
		return nil
	})
	return out, err
}

func commentFromDB(r db.AppSiteComment) SiteComment {
	mentions := r.MentionedUserIds
	if mentions == nil {
		mentions = []string{}
	}
	return SiteComment{
		ID:               r.ID,
		OrgID:            r.OrgID,
		SiteID:           r.SiteID,
		AuthorUserID:     r.AuthorUserID,
		Body:             r.Body,
		MentionedUserIDs: mentions,
		CreatedAt:        r.CreatedAt,
	}
}
