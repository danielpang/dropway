// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

const (
	commentPageDefault = 50
	commentPageMax     = 100
)

// CommentFeedItem is one authorized comment head. Body text is untrusted data.
type CommentFeedItem struct {
	Source      string
	ID          string
	CreatedAt   time.Time
	AuthorID    string
	Body        string
	RoomID      string
	SiteID      string
	VersionID   string
	ContentHash string
	Page        string
	AnchorKind  string
	Anchor      json.RawMessage
	AnchorState string
	Status      string
	Untrusted   bool
}

// CommentPage is one deterministic page across feed comments and room threads.
type CommentPage struct {
	Items      []CommentFeedItem
	NextCursor string
}

// ReplyPage is one page of replies plus the thread anchor context.
type ReplyPage struct {
	ThreadID    string
	RoomID      string
	SiteID      string
	VersionID   string
	ContentHash string
	Page        string
	AnchorKind  string
	Anchor      json.RawMessage
	AnchorState string
	Status      string
	Revisions   []AnchorRevision
	Replies     []ReviewReply
	NextCursor  string
	Untrusted   bool
}

// ThreadPage is one keyset page of review threads still attached to the room.
type ThreadPage struct {
	Threads    []ReviewThread
	NextCursor string
}

// CandidatePage is one keyset page of review candidates. Summaries are untrusted.
type CandidatePage struct {
	Candidates []Candidate
	NextCursor string
}

// PageSiteComments lists the next page of site feed comments and room-review
// threads this user can read. Room membership is evaluated on this request.
func (s *Store) PageSiteComments(ctx context.Context, t Tenant, siteID, cursor string, limit int) (CommentPage, error) {
	limit = clampPage(limit)
	at, kind, id, err := projectroom.DecodePageCursor(cursor)
	if err != nil {
		return CommentPage{}, err
	}
	if cursor != "" && kind != "feed" && kind != "room" {
		return CommentPage{}, projectroom.ErrBadCursor
	}
	var out CommentPage
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := q.GetSite(ctx, db.GetSiteParams{ID: siteID, OrgID: t.OrgID}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		feed, err := q.PageSiteFeedComments(ctx, db.PageSiteFeedCommentsParams{
			OrgID: t.OrgID, SiteID: siteID, CursorAt: at, CursorKind: kind, CursorID: id, Lim: int32(limit),
		})
		if err != nil {
			return err
		}
		var rooms []db.AppReviewThread
		enabled, err := q.GetProjectRoomsEnabled(ctx, t.OrgID)
		if err != nil && !isNoRows(err) {
			return err
		}
		if enabled {
			rooms, err = q.PageSiteRoomThreads(ctx, db.PageSiteRoomThreadsParams{
				UserID: t.UserID, OrgID: t.OrgID, SiteID: siteID,
				CursorAt: at, CursorKind: kind, CursorID: id, Lim: int32(limit),
			})
			if err != nil {
				return err
			}
		}
		out.Items = mergeCommentHeads(feed, rooms, limit)
		if len(out.Items) == limit {
			last := out.Items[len(out.Items)-1]
			out.NextCursor = projectroom.EncodePageCursor(last.CreatedAt, last.Source, last.ID)
		}
		return nil
	})
	return out, err
}

// PageReviewReplies returns the thread and one page of replies. A caller who
// lost room membership, or a thread whose site left the room, gets not-found.
func (s *Store) PageReviewReplies(ctx context.Context, t Tenant, threadID, cursor string, limit int) (ReplyPage, error) {
	limit = clampPage(limit)
	at, kind, id, err := projectroom.DecodePageCursor(cursor)
	if err != nil {
		return ReplyPage{}, err
	}
	if cursor != "" && kind != "reply" {
		return ReplyPage{}, projectroom.ErrBadCursor
	}
	var out ReplyPage
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		enabled, err := q.GetProjectRoomsEnabled(ctx, t.OrgID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if !enabled {
			return ErrProjectRoomsDisabled
		}
		th, err := q.GetReadableReviewThread(ctx, db.GetReadableReviewThreadParams{
			UserID: t.UserID, ID: threadID, OrgID: t.OrgID,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		revs, err := q.ListReviewAnchorRevisions(ctx, db.ListReviewAnchorRevisionsParams{OrgID: t.OrgID, ThreadID: threadID})
		if err != nil {
			return err
		}
		replies, err := q.PageReviewReplies(ctx, db.PageReviewRepliesParams{
			OrgID: t.OrgID, ThreadID: threadID, CursorAt: at, CursorID: id, Lim: int32(limit),
		})
		if err != nil {
			return err
		}
		out = ReplyPage{
			ThreadID: th.ID, RoomID: th.RoomID, SiteID: th.SiteID, VersionID: th.VersionID,
			ContentHash: th.ContentHash, Page: th.Page, AnchorKind: th.AnchorKind,
			Anchor: th.Anchor, AnchorState: th.AnchorState, Status: th.Status, Untrusted: true,
		}
		out.Revisions = make([]AnchorRevision, len(revs))
		for i, rev := range revs {
			out.Revisions[i] = AnchorRevision{
				ID: rev.ID, AnchorKind: rev.AnchorKind, Anchor: rev.Anchor, Page: rev.Page,
				AnchorState: rev.AnchorState, Reason: rev.Reason, ActorID: rev.ActorUserID, CreatedAt: rev.CreatedAt,
			}
		}
		out.Replies = make([]ReviewReply, len(replies))
		for i, rp := range replies {
			out.Replies[i] = ReviewReply{ID: rp.ID, AuthorID: rp.AuthorUserID, Body: rp.Body, CreatedAt: rp.CreatedAt}
		}
		if len(replies) == limit {
			last := replies[len(replies)-1]
			out.NextCursor = projectroom.EncodePageCursor(last.CreatedAt, "reply", last.ID)
		}
		return nil
	})
	return out, err
}

func authorizeLockedCandidatePublish(ctx context.Context, q *db.Queries, orgID string, site db.AppSite, ver db.AppSiteVersion) error {
	cands, err := q.ListCandidatesForArtifact(ctx, db.ListCandidatesForArtifactParams{
		OrgID: orgID, SiteID: site.ID, ContentHash: ver.ContentHash,
	})
	if err != nil {
		return err
	}
	if len(cands) == 0 {
		return nil
	}
	if site.CurrentVersionID == nil {
		return ErrCandidatePublishDenied
	}
	live, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: *site.CurrentVersionID, OrgID: orgID})
	if err != nil {
		return err
	}
	matches := 0
	for _, c := range cands {
		if c.State == projectroom.CandidateApproved && c.ContentHash == ver.ContentHash &&
			c.BaseVersionID == live.ID && c.BaseContentHash == live.ContentHash {
			matches++
		}
	}
	if matches != 1 {
		return ErrCandidatePublishDenied
	}
	return nil
}

func mergeCommentHeads(feed []db.AppPostComment, rooms []db.AppReviewThread, limit int) []CommentFeedItem {
	items := make([]CommentFeedItem, 0, len(feed)+len(rooms))
	for _, c := range feed {
		items = append(items, CommentFeedItem{
			Source: "feed", ID: c.ID, CreatedAt: c.CreatedAt, AuthorID: c.AuthorUserID,
			Body: c.Body, SiteID: c.SubjectID, Untrusted: true,
		})
	}
	for _, th := range rooms {
		items = append(items, CommentFeedItem{
			Source: "room", ID: th.ID, CreatedAt: th.CreatedAt, AuthorID: th.CreatedBy,
			RoomID: th.RoomID, SiteID: th.SiteID, VersionID: th.VersionID, ContentHash: th.ContentHash,
			Page: th.Page, AnchorKind: th.AnchorKind, Anchor: th.Anchor, AnchorState: th.AnchorState,
			Status: th.Status, Untrusted: true,
		})
	}
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 && commentLess(items[j], items[j-1]) {
			items[j], items[j-1] = items[j-1], items[j]
			j--
		}
	}
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func commentLess(a, b CommentFeedItem) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.ID < b.ID
}

func clampPage(limit int) int {
	if limit <= 0 {
		return commentPageDefault
	}
	if limit > commentPageMax {
		return commentPageMax
	}
	return limit
}
