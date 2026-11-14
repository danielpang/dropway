// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// PageRoomThreads lists threads this member can still read, including resolved
// and orphaned rows. A site removed from the room drops out on the next page.
func (s *Store) PageRoomThreads(ctx context.Context, t Tenant, roomID, cursor string, limit int) (ThreadPage, error) {
	page, err := s.beginReviewPage(t, "room-threads", roomID, cursor, false)
	at, id := page.At, page.ID
	if err != nil {
		return ThreadPage{}, err
	}
	limit = clampPage(limit)
	var out ThreadPage
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		rows, err := q.PageRoomThreads(ctx, db.PageRoomThreadsParams{
			UserID: t.UserID, OrgID: t.OrgID, RoomID: roomID,
			CursorAt: at, CursorID: id, Lim: int32(limit + 1), Watermark: page.Watermark,
		})
		if err != nil {
			return err
		}
		rows, more, err := boundedReviewRows(rows, limit)
		if err != nil {
			return err
		}
		out.Threads = make([]ReviewThread, len(rows))
		for i, row := range rows {
			out.Threads[i] = threadFrom(row, mem.Role)
		}
		if more {
			last := rows[len(rows)-1]
			out.NextCursor, err = s.nextReviewPage(page, last.CreatedAt, "thread", last.ID)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// PageRoomCandidates lists candidates for a room the caller still belongs to.
func (s *Store) PageRoomCandidates(ctx context.Context, t Tenant, roomID, cursor string, limit int) (CandidatePage, error) {
	page, err := s.beginReviewPage(t, "room-candidates", roomID, cursor, false)
	at, id := page.At, page.ID
	if err != nil {
		return CandidatePage{}, err
	}
	limit = clampPage(limit)
	var out CandidatePage
	err = s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		if _, err := roomMember(ctx, q, t, roomID); err != nil {
			return err
		}
		rows, err := q.PageRoomCandidates(ctx, db.PageRoomCandidatesParams{
			UserID: t.UserID, OrgID: t.OrgID, RoomID: roomID,
			CursorAt: at, CursorID: id, Lim: int32(limit + 1), Watermark: page.Watermark,
		})
		if err != nil {
			return err
		}
		rows, more, err := boundedReviewRows(rows, limit)
		if err != nil {
			return err
		}
		out.Candidates = make([]Candidate, len(rows))
		for i, row := range rows {
			out.Candidates[i] = candidateFrom(row)
			if err := loadCandidateReceipt(ctx, tx, t.OrgID, &out.Candidates[i]); err != nil {
				return err
			}
		}
		if more {
			last := rows[len(rows)-1]
			out.NextCursor, err = s.nextReviewPage(page, last.CreatedAt, "candidate", last.ID)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// GetRoomCandidate returns one candidate the caller can read.
func (s *Store) GetRoomCandidate(ctx context.Context, t Tenant, roomID, candidateID string) (Candidate, error) {
	var out Candidate
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		row, err := q.GetReviewCandidateForMember(ctx, db.GetReviewCandidateForMemberParams{
			UserID: t.UserID, ID: candidateID, OrgID: t.OrgID, RoomID: roomID,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = candidateFromMember(row)
		return loadCandidateReceipt(ctx, tx, t.OrgID, &out)
	})
	return out, err
}

// RemoveProjectRoomMember drops a non-owner member. Only the room owner may do this.
func (s *Store) RemoveProjectRoomMember(ctx context.Context, t Tenant, roomID, userID string, rec AuditRecord) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		if !projectroom.CanManageMembers(projectroom.Role(mem.Role)) {
			return ErrRoomForbidden
		}
		target, err := q.GetProjectRoomMember(ctx, db.GetProjectRoomMemberParams{OrgID: t.OrgID, RoomID: roomID, UserID: userID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if target.Role == string(projectroom.RoleOwner) {
			return ErrRoomForbidden
		}
		n, err := q.DeleteProjectRoomMember(ctx, db.DeleteProjectRoomMemberParams{OrgID: t.OrgID, RoomID: roomID, UserID: userID})
		if err != nil || n == 0 {
			if err != nil {
				return err
			}
			return ErrNotFound
		}
		if err := writeActivity(ctx, q, t, roomID, "member_removed", nil, nil, nil); err != nil {
			return err
		}
		rec.Action = audit.ActionProjectRoomMemberRemove
		rec.Target = "room:" + roomID
		rec.Metadata = map[string]any{"user_id": userID}
		_, err = writeAuditTx(ctx, q, t.OrgID, rec)
		return err
	})
}

// UpdateProjectRoomMemberRole changes a non-owner member. It does not transfer ownership.
func (s *Store) UpdateProjectRoomMemberRole(ctx context.Context, t Tenant, roomID, userID, role string, rec AuditRecord) error {
	parsed, ok := projectroom.ParseRole(role)
	if !ok || parsed == projectroom.RoleOwner {
		return ErrRoomForbidden
	}
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		if !projectroom.CanManageMembers(projectroom.Role(mem.Role)) {
			return ErrRoomForbidden
		}
		target, err := q.GetProjectRoomMember(ctx, db.GetProjectRoomMemberParams{OrgID: t.OrgID, RoomID: roomID, UserID: userID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if target.Role == string(projectroom.RoleOwner) {
			return ErrRoomForbidden
		}
		if _, err := q.UpdateProjectRoomMemberRole(ctx, db.UpdateProjectRoomMemberRoleParams{
			Role: string(parsed), OrgID: t.OrgID, RoomID: roomID, UserID: userID,
		}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if err := writeActivity(ctx, q, t, roomID, "member_role_changed", nil, nil, nil); err != nil {
			return err
		}
		rec.Action = audit.ActionProjectRoomMemberRole
		rec.Target = "room:" + roomID
		rec.Metadata = map[string]any{"user_id": userID, "role": string(parsed)}
		_, err = writeAuditTx(ctx, q, t.OrgID, rec)
		return err
	})
}

// RemoveProjectRoomSite detaches a site reference. Threads and candidates remain.
// Candidate-hash publish checks still apply. The site row is locked first.
func (s *Store) RemoveProjectRoomSite(ctx context.Context, t Tenant, roomID, siteID string, rec AuditRecord) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		if !projectroom.CanManageMembers(projectroom.Role(mem.Role)) {
			return ErrRoomForbidden
		}
		if _, err := lockOrgThenSite(ctx, q, t.OrgID, siteID); err != nil {
			return err
		}
		n, err := q.DeleteProjectRoomSite(ctx, db.DeleteProjectRoomSiteParams{OrgID: t.OrgID, RoomID: roomID, SiteID: siteID})
		if err != nil || n == 0 {
			if err != nil {
				return err
			}
			return ErrNotFound
		}
		if err := writeActivity(ctx, q, t, roomID, "site_removed", nil, &siteID, nil); err != nil {
			return err
		}
		rec.Action = audit.ActionProjectRoomSiteRemove
		rec.Target = "room:" + roomID
		rec.Metadata = map[string]any{"site_id": siteID}
		_, err = writeAuditTx(ctx, q, t.OrgID, rec)
		return err
	})
}

// ResolveReviewThread marks a thread resolved without changing its anchor history.
func (s *Store) ResolveReviewThread(ctx context.Context, t Tenant, roomID, threadID string, rec AuditRecord) (ReviewThread, error) {
	var out ReviewThread
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		th, err := s.editableThread(ctx, q, t, roomID, threadID)
		if err != nil {
			return err
		}
		row := memberThreadRow(th)
		if th.Status != "resolved" {
			updated, err := q.UpdateReviewThreadStatus(ctx, db.UpdateReviewThreadStatusParams{
				Status: "resolved", ResolvedBy: &t.UserID, ID: threadID, OrgID: t.OrgID, RoomID: roomID,
			})
			if err != nil {
				return err
			}
			row = updated
			if err := writeActivity(ctx, q, t, roomID, "thread_resolved", &threadID, &row.SiteID, &row.VersionID); err != nil {
				return err
			}
			rec.Action = audit.ActionReviewThreadResolve
			rec.Target = "thread:" + threadID
			rec.Metadata = map[string]any{"room_id": roomID}
			if _, err := writeAuditTx(ctx, q, t.OrgID, rec); err != nil {
				return err
			}
		}
		revs, err := q.ListReviewAnchorRevisions(ctx, db.ListReviewAnchorRevisionsParams{OrgID: t.OrgID, ThreadID: threadID})
		if err != nil {
			return err
		}
		out = threadWithRevisions(row, th.Role, revs)
		return nil
	})
	return out, err
}

// ReanchorReviewThread appends an anchor revision. The original pin stays immutable.
func (s *Store) ReanchorReviewThread(ctx context.Context, t Tenant, roomID, threadID, kind, state, reason string, anchor json.RawMessage, rec AuditRecord) (ReviewThread, error) {
	parsed, err := projectroom.ValidateAnchor(kind, anchor)
	if err != nil {
		return ReviewThread{}, err
	}
	if state != projectroom.AnchorAnchored && state != projectroom.AnchorOrphaned {
		return ReviewThread{}, projectroom.ErrInvalidAnchor
	}
	if state == projectroom.AnchorOrphaned {
		reason = "orphaned"
	} else {
		reason = "reanchored"
	}
	var out ReviewThread
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		th, err := s.editableThread(ctx, q, t, roomID, threadID)
		if err != nil {
			return err
		}
		if _, err := q.InsertReviewAnchorRevision(ctx, db.InsertReviewAnchorRevisionParams{
			OrgID: t.OrgID, ThreadID: threadID, AnchorKind: parsed.Kind, Anchor: parsed.JSON,
			Page: th.Page, AnchorState: state, Reason: reason, ActorUserID: t.UserID,
		}); err != nil {
			return err
		}
		updated, err := q.UpdateReviewThreadAnchor(ctx, db.UpdateReviewThreadAnchorParams{
			AnchorKind: parsed.Kind, Anchor: parsed.JSON, AnchorState: state,
			ID: threadID, OrgID: t.OrgID, RoomID: roomID,
		})
		if err != nil {
			return err
		}
		if updated.Page != th.Page || updated.VersionID != th.VersionID || updated.ContentHash != th.ContentHash || updated.SiteID != th.SiteID {
			return ErrRoomConflict
		}
		activity := "anchor_reanchored"
		if state == projectroom.AnchorOrphaned {
			activity = "anchor_orphaned"
		}
		if err := writeActivity(ctx, q, t, roomID, activity, &threadID, &updated.SiteID, &updated.VersionID); err != nil {
			return err
		}
		rec.Action = audit.ActionReviewThreadReanchor
		rec.Target = "thread:" + threadID
		rec.Metadata = map[string]any{"room_id": roomID, "anchor_state": state}
		if _, err := writeAuditTx(ctx, q, t.OrgID, rec); err != nil {
			return err
		}
		revs, err := q.ListReviewAnchorRevisions(ctx, db.ListReviewAnchorRevisionsParams{OrgID: t.OrgID, ThreadID: threadID})
		if err != nil {
			return err
		}
		out = threadWithRevisions(updated, th.Role, revs)
		return nil
	})
	return out, err
}

func (s *Store) editableThread(ctx context.Context, q *db.Queries, t Tenant, roomID, threadID string) (db.GetReviewThreadForMemberRow, error) {
	if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
		return db.GetReviewThreadForMemberRow{}, err
	}
	th, err := q.GetReviewThreadForMember(ctx, db.GetReviewThreadForMemberParams{
		UserID: t.UserID, ID: threadID, OrgID: t.OrgID, RoomID: roomID,
	})
	if err != nil {
		if isNoRows(err) {
			return db.GetReviewThreadForMemberRow{}, ErrNotFound
		}
		return db.GetReviewThreadForMemberRow{}, err
	}
	if _, err := q.GetProjectRoomSite(ctx, db.GetProjectRoomSiteParams{OrgID: t.OrgID, RoomID: roomID, SiteID: th.SiteID}); err != nil {
		if isNoRows(err) {
			return db.GetReviewThreadForMemberRow{}, ErrNotFound
		}
		return db.GetReviewThreadForMemberRow{}, err
	}
	rights, _, err := s.siteRights(ctx, q, t, th.SiteID)
	if err != nil {
		return db.GetReviewThreadForMemberRow{}, err
	}
	if !projectroom.CanComment(projectroom.Role(th.Role), rights) {
		return db.GetReviewThreadForMemberRow{}, ErrRoomForbidden
	}
	return th, nil
}

func memberThreadRow(row db.GetReviewThreadForMemberRow) db.AppReviewThread {
	return db.AppReviewThread{
		ID: row.ID, OrgID: row.OrgID, RoomID: row.RoomID, SiteID: row.SiteID,
		VersionID: row.VersionID, ContentHash: row.ContentHash, Page: row.Page,
		AnchorKind: row.AnchorKind, Anchor: row.Anchor, AnchorState: row.AnchorState,
		Status: row.Status, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
		ResolvedBy: row.ResolvedBy, ResolvedAt: row.ResolvedAt,
	}
}

func threadWithRevisions(row db.AppReviewThread, role string, revs []db.AppReviewAnchorRevision) ReviewThread {
	out := threadFrom(row, role)
	out.Revisions = make([]AnchorRevision, len(revs))
	for i, rev := range revs {
		out.Revisions[i] = AnchorRevision{
			ID: rev.ID, AnchorKind: rev.AnchorKind, Anchor: rev.Anchor, Page: rev.Page,
			AnchorState: rev.AnchorState, Reason: rev.Reason, ActorID: rev.ActorUserID, CreatedAt: rev.CreatedAt,
		}
	}
	return out
}
