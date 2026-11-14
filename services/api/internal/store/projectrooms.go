// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

const (
	maxProjectRooms   = 100
	maxRoomMembers    = 50
	maxRoomName       = 80
	activityPageLimit = 50
)

var (
	ErrProjectRoomsDisabled = errors.New("store: project rooms disabled")
	ErrRoomForbidden        = errors.New("store: room role does not allow this")
	ErrRoomConflict         = errors.New("store: room conflict")
	ErrCandidateState       = errors.New("store: candidate state")
	ErrApplicationDisabled  = errors.New("store: candidate application is disabled")
)

// Room is a same-org review room. It references sites; it does not own them.
type Room struct {
	ID          string
	OrgID       string
	Name        string
	OwnerUserID string
	Role        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Unread      int64
}

type RoomMember struct {
	UserID string
	Role   string
}

type RoomSite struct {
	SiteID    string
	CreatedBy string
}

type RoomDetail struct {
	Room
	Members []RoomMember
	Sites   []RoomSite
}

type ReviewThread struct {
	ID          string
	RoomID      string
	SiteID      string
	VersionID   string
	ContentHash string
	Page        string
	AnchorKind  string
	Anchor      json.RawMessage
	AnchorState string
	Status      string
	CreatedBy   string
	CreatedAt   time.Time
	ResolvedBy  *string
	ResolvedAt  *time.Time
	Role        string
	Replies     []ReviewReply
	Revisions   []AnchorRevision
}

type ReviewReply struct {
	ID        string
	AuthorID  string
	Body      string
	CreatedAt time.Time
}

type AnchorRevision struct {
	ID          string
	AnchorKind  string
	Anchor      json.RawMessage
	Page        string
	AnchorState string
	Reason      string
	ActorID     string
	CreatedAt   time.Time
}

type VersionSignoff struct {
	SiteID      string
	VersionID   string
	ContentHash string
	UserID      string
	CreatedAt   time.Time
}

type Activity struct {
	ID        string
	ActorID   string
	Kind      string
	ThreadID  *string
	SiteID    *string
	VersionID *string
	CreatedAt time.Time
}

// Candidate is an immutable proposed change. Approve-change is not version sign-off.
type Candidate struct {
	ID              string
	RoomID          string
	SiteID          string
	BaseVersionID   string
	BaseContentHash string
	ContentHash     string
	DiffHash        string
	Summary         string
	State           string
	CreatedBy       string
	CreatedAt       time.Time
	ApprovedBy      *string
	RejectedBy      *string
}

type CandidateDecision struct {
	Candidate Candidate
	Changed   bool
}

func (s *Store) ProjectRoomsEnabled(ctx context.Context, t Tenant) (bool, error) {
	var on bool
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		v, err := q.GetProjectRoomsEnabled(ctx, t.OrgID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		on = v
		return nil
	})
	return on, err
}

func (s *Store) SetProjectRoomsEnabled(ctx context.Context, t Tenant, enabled bool, rec AuditRecord) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := q.SetProjectRoomsEnabled(ctx, db.SetProjectRoomsEnabledParams{Enabled: enabled, OrgID: t.OrgID}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		rec.Action = audit.ActionProjectRoomsToggle
		rec.Target = "org:" + t.OrgID
		rec.Metadata = map[string]any{"enabled": enabled}
		_, err := writeAuditTx(ctx, q, t.OrgID, rec)
		return err
	})
}

func (s *Store) CreateProjectRoom(ctx context.Context, t Tenant, name string, rec AuditRecord) (Room, error) {
	name, err := cleanRoomName(name)
	if err != nil {
		return Room{}, err
	}
	var out Room
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		n, err := q.CountProjectRoomsForOrg(ctx, t.OrgID)
		if err != nil {
			return err
		}
		if n >= maxProjectRooms {
			return ErrRoomConflict
		}
		row, err := q.InsertProjectRoom(ctx, db.InsertProjectRoomParams{OrgID: t.OrgID, Name: name, OwnerUserID: t.UserID})
		if err != nil {
			return mapRoomErr(err)
		}
		if _, err := q.InsertProjectRoomMember(ctx, db.InsertProjectRoomMemberParams{
			OrgID: t.OrgID, RoomID: row.ID, UserID: t.UserID, Role: string(projectroom.RoleOwner),
		}); err != nil {
			return mapRoomErr(err)
		}
		if err := writeActivity(ctx, q, t, row.ID, "room_created", nil, nil, nil); err != nil {
			return err
		}
		rec.Action = audit.ActionProjectRoomCreate
		rec.Target = "room:" + row.ID
		if _, err := writeAuditTx(ctx, q, t.OrgID, rec); err != nil {
			return err
		}
		out = roomFrom(row, string(projectroom.RoleOwner), 0)
		return nil
	})
	return out, err
}

func (s *Store) ListProjectRooms(ctx context.Context, t Tenant) ([]Room, error) {
	var out []Room
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		rows, err := q.ListProjectRoomsForMember(ctx, db.ListProjectRoomsForMemberParams{UserID: t.UserID, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		out = make([]Room, 0, len(rows))
		for _, row := range rows {
			n, err := q.CountUnreadReviewActivity(ctx, db.CountUnreadReviewActivityParams{OrgID: t.OrgID, RoomID: row.ID, UserID: t.UserID})
			if err != nil {
				return err
			}
			out = append(out, Room{
				ID: row.ID, OrgID: row.OrgID, Name: row.Name, OwnerUserID: row.OwnerUserID,
				Role: row.Role, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Unread: n,
			})
		}
		return nil
	})
	return out, err
}

func (s *Store) GetProjectRoom(ctx context.Context, t Tenant, roomID string) (RoomDetail, error) {
	var out RoomDetail
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		rows, err := q.ListProjectRoomsForMember(ctx, db.ListProjectRoomsForMemberParams{UserID: t.UserID, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		var found *db.ListProjectRoomsForMemberRow
		for i := range rows {
			if rows[i].ID == roomID {
				found = &rows[i]
				break
			}
		}
		if found == nil {
			return ErrNotFound
		}
		n, err := q.CountUnreadReviewActivity(ctx, db.CountUnreadReviewActivityParams{OrgID: t.OrgID, RoomID: roomID, UserID: t.UserID})
		if err != nil {
			return err
		}
		members, err := q.ListProjectRoomMembers(ctx, db.ListProjectRoomMembersParams{OrgID: t.OrgID, RoomID: roomID})
		if err != nil {
			return err
		}
		sites, err := q.ListProjectRoomSites(ctx, db.ListProjectRoomSitesParams{OrgID: t.OrgID, RoomID: roomID})
		if err != nil {
			return err
		}
		out.Room = Room{
			ID: found.ID, OrgID: found.OrgID, Name: found.Name, OwnerUserID: found.OwnerUserID,
			Role: mem.Role, CreatedAt: found.CreatedAt, UpdatedAt: found.UpdatedAt, Unread: n,
		}
		out.Members = make([]RoomMember, len(members))
		for i, m := range members {
			out.Members[i] = RoomMember{UserID: m.UserID, Role: m.Role}
		}
		out.Sites = make([]RoomSite, len(sites))
		for i, site := range sites {
			out.Sites[i] = RoomSite{SiteID: site.SiteID, CreatedBy: site.CreatedBy}
		}
		return nil
	})
	return out, err
}

func (s *Store) AddProjectRoomSite(ctx context.Context, t Tenant, roomID, siteID string, rec AuditRecord) error {
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
		site, err := q.GetSite(ctx, db.GetSiteParams{ID: siteID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		ownerBefore := site.OwnerUserID
		if _, err := lockOrgThenSite(ctx, q, t.OrgID, siteID); err != nil {
			return err
		}
		if _, err := q.InsertProjectRoomSite(ctx, db.InsertProjectRoomSiteParams{
			OrgID: t.OrgID, RoomID: roomID, SiteID: siteID, CreatedBy: t.UserID,
		}); err != nil {
			return mapRoomErr(err)
		}
		siteAfter, err := q.GetSite(ctx, db.GetSiteParams{ID: siteID, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		if siteAfter.OwnerUserID != ownerBefore {
			return fmt.Errorf("project room changed site ownership")
		}
		if err := writeActivity(ctx, q, t, roomID, "site_added", nil, &siteID, nil); err != nil {
			return err
		}
		rec.Action = audit.ActionProjectRoomSiteAdd
		rec.Target = "room:" + roomID
		rec.Metadata = map[string]any{"site_id": siteID}
		_, err = writeAuditTx(ctx, q, t.OrgID, rec)
		return err
	})
}

func (s *Store) AddProjectRoomMember(ctx context.Context, t Tenant, roomID, userID, role string, rec AuditRecord) error {
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
		if _, err := s.MemberRole(ctx, t.OrgID, userID); err != nil {
			if errors.Is(err, ErrNoMembership) || errors.Is(err, ErrAuthSchemaUnavailable) {
				return ErrNotOrgMember
			}
			return err
		}
		n, err := q.CountProjectRoomMembers(ctx, db.CountProjectRoomMembersParams{OrgID: t.OrgID, RoomID: roomID})
		if err != nil {
			return err
		}
		if n >= maxRoomMembers {
			return ErrRoomConflict
		}
		if _, err := q.InsertProjectRoomMember(ctx, db.InsertProjectRoomMemberParams{
			OrgID: t.OrgID, RoomID: roomID, UserID: userID, Role: string(parsed),
		}); err != nil {
			return mapRoomErr(err)
		}
		if err := writeActivity(ctx, q, t, roomID, "member_added", nil, nil, nil); err != nil {
			return err
		}
		rec.Action = audit.ActionProjectRoomMemberAdd
		rec.Target = "room:" + roomID
		rec.Metadata = map[string]any{"user_id": userID, "role": string(parsed)}
		_, err = writeAuditTx(ctx, q, t.OrgID, rec)
		return err
	})
}

func (s *Store) CreateReviewThread(ctx context.Context, t Tenant, roomID, siteID, versionID, page, kind string, anchor json.RawMessage) (ReviewThread, error) {
	parsed, err := projectroom.ValidateAnchor(kind, anchor)
	if err != nil {
		return ReviewThread{}, err
	}
	page, err = projectroom.ValidatePage(page)
	if err != nil {
		return ReviewThread{}, err
	}
	var out ReviewThread
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		rights, _, err := s.siteRights(ctx, q, t, siteID)
		if err != nil {
			return err
		}
		if !projectroom.CanComment(projectroom.Role(mem.Role), rights) {
			return ErrRoomForbidden
		}
		if _, err := q.GetProjectRoomSite(ctx, db.GetProjectRoomSiteParams{OrgID: t.OrgID, RoomID: roomID, SiteID: siteID}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: versionID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if ver.SiteID != siteID {
			return ErrVersionMismatch
		}
		row, err := q.InsertReviewThread(ctx, db.InsertReviewThreadParams{
			OrgID: t.OrgID, RoomID: roomID, SiteID: siteID, VersionID: ver.ID,
			ContentHash: ver.ContentHash, Page: page, AnchorKind: parsed.Kind, Anchor: parsed.JSON, CreatedBy: t.UserID,
		})
		if err != nil {
			return mapRoomErr(err)
		}
		if _, err := q.InsertReviewAnchorRevision(ctx, db.InsertReviewAnchorRevisionParams{
			OrgID: t.OrgID, ThreadID: row.ID, AnchorKind: parsed.Kind, Anchor: parsed.JSON,
			Page: page, AnchorState: projectroom.AnchorAnchored, Reason: "created", ActorUserID: t.UserID,
		}); err != nil {
			return err
		}
		if err := writeActivity(ctx, q, t, roomID, "thread_created", &row.ID, &siteID, &ver.ID); err != nil {
			return err
		}
		out = threadFrom(row, mem.Role)
		return nil
	})
	return out, err
}

func (s *Store) AddReviewReply(ctx context.Context, t Tenant, roomID, threadID, body string) (ReviewReply, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 4000 || hasControl(body) {
		return ReviewReply{}, projectroom.ErrInvalidAnchor
	}
	var out ReviewReply
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		th, err := q.GetReviewThreadForMember(ctx, db.GetReviewThreadForMemberParams{
			UserID: t.UserID, ID: threadID, OrgID: t.OrgID, RoomID: roomID,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		rights, _, err := s.siteRights(ctx, q, t, th.SiteID)
		if err != nil {
			return err
		}
		if !projectroom.CanComment(projectroom.Role(th.Role), rights) {
			return ErrRoomForbidden
		}
		row, err := q.InsertReviewReply(ctx, db.InsertReviewReplyParams{
			OrgID: t.OrgID, ThreadID: threadID, AuthorUserID: t.UserID, Body: body,
		})
		if err != nil {
			return err
		}
		if err := writeActivity(ctx, q, t, roomID, "reply_created", &threadID, &th.SiteID, &th.VersionID); err != nil {
			return err
		}
		out = ReviewReply{ID: row.ID, AuthorID: row.AuthorUserID, Body: row.Body, CreatedAt: row.CreatedAt}
		return nil
	})
	return out, err
}

func (s *Store) SignoffVersion(ctx context.Context, t Tenant, roomID, siteID, versionID string) (VersionSignoff, error) {
	var out VersionSignoff
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		rights, _, err := s.siteRights(ctx, q, t, siteID)
		if err != nil {
			return err
		}
		if !projectroom.CanApprove(projectroom.Role(mem.Role), rights) {
			return ErrRoomForbidden
		}
		if _, err := q.GetProjectRoomSite(ctx, db.GetProjectRoomSiteParams{OrgID: t.OrgID, RoomID: roomID, SiteID: siteID}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: versionID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if ver.SiteID != siteID {
			return ErrVersionMismatch
		}
		row, err := q.InsertReviewApproval(ctx, db.InsertReviewApprovalParams{
			OrgID: t.OrgID, RoomID: roomID, SiteID: siteID, VersionID: ver.ID, ContentHash: ver.ContentHash, UserID: t.UserID,
		})
		if err != nil {
			if isNoRows(err) {
				existing, gerr := q.GetReviewApproval(ctx, db.GetReviewApprovalParams{
					OrgID: t.OrgID, RoomID: roomID, VersionID: ver.ID, UserID: t.UserID,
				})
				if gerr != nil {
					return gerr
				}
				row = existing
			} else {
				return mapRoomErr(err)
			}
		} else if err := writeActivity(ctx, q, t, roomID, "version_signoff", nil, &siteID, &ver.ID); err != nil {
			return err
		}
		out = VersionSignoff{SiteID: row.SiteID, VersionID: row.VersionID, ContentHash: row.ContentHash, UserID: row.UserID, CreatedAt: row.CreatedAt}
		return nil
	})
	return out, err
}

func (s *Store) SubmitCandidate(ctx context.Context, t Tenant, roomID, siteID, baseVersionID, contentHash, diffHash, summary string) (Candidate, error) {
	if !hex64(contentHash) || !hex64(diffHash) {
		return Candidate{}, projectroom.ErrCandidateMismatch
	}
	summary = strings.TrimSpace(summary)
	if summary == "" || len(summary) > 2000 || hasControl(summary) {
		return Candidate{}, projectroom.ErrInvalidAnchor
	}
	var out Candidate
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		rights, _, err := s.siteRights(ctx, q, t, siteID)
		if err != nil {
			return err
		}
		// Submitting a candidate is a proposal, not approval and not publish.
		if !projectroom.CanRun(projectroom.Role(mem.Role), rights) {
			return ErrRoomForbidden
		}
		if _, err := q.GetProjectRoomSite(ctx, db.GetProjectRoomSiteParams{OrgID: t.OrgID, RoomID: roomID, SiteID: siteID}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: baseVersionID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if ver.SiteID != siteID {
			return ErrVersionMismatch
		}
		row, err := q.InsertReviewCandidate(ctx, db.InsertReviewCandidateParams{
			OrgID: t.OrgID, RoomID: roomID, SiteID: siteID, BaseVersionID: ver.ID,
			BaseContentHash: ver.ContentHash, ContentHash: contentHash, DiffHash: diffHash,
			Summary: summary, CreatedBy: t.UserID,
		})
		if err != nil {
			return mapRoomErr(err)
		}
		if err := writeActivity(ctx, q, t, roomID, "candidate_submitted", nil, &siteID, &ver.ID); err != nil {
			return err
		}
		out = candidateFrom(row)
		return nil
	})
	return out, err
}

// ApproveCandidate records a human approve-change decision. It does not publish
// and it does not move the live pointer. A repeated call for the same artifact
// is idempotent. A different candidate or version does not match.
func (s *Store) ApproveCandidate(ctx context.Context, t Tenant, roomID string, req projectroom.ApprovalRequest, rec AuditRecord) (CandidateDecision, error) {
	return s.decideCandidate(ctx, t, roomID, req, true, rec)
}

// RejectCandidate terminally blocks application of this candidate.
func (s *Store) RejectCandidate(ctx context.Context, t Tenant, roomID string, req projectroom.ApprovalRequest, rec AuditRecord) (CandidateDecision, error) {
	return s.decideCandidate(ctx, t, roomID, req, false, rec)
}

func (s *Store) decideCandidate(ctx context.Context, t Tenant, roomID string, req projectroom.ApprovalRequest, approve bool, rec AuditRecord) (CandidateDecision, error) {
	var out CandidateDecision
	conflict := false
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		peek, err := q.GetReviewCandidateForMember(ctx, db.GetReviewCandidateForMemberParams{
			UserID: t.UserID, ID: req.CandidateID, OrgID: t.OrgID, RoomID: roomID,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if _, err := lockOrgThenSite(ctx, q, t.OrgID, peek.SiteID); err != nil {
			return err
		}
		row, err := q.LockReviewCandidateForMember(ctx, db.LockReviewCandidateForMemberParams{
			UserID: t.UserID, ID: req.CandidateID, OrgID: t.OrgID, RoomID: roomID,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		rights, _, err := s.siteRights(ctx, q, t, row.SiteID)
		if err != nil {
			return err
		}
		if !projectroom.CanAccept(projectroom.Role(row.Role), rights) {
			return ErrRoomForbidden
		}
		lockedSite, err := q.GetSite(ctx, db.GetSiteParams{ID: row.SiteID, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		req.LiveVersionID = ""
		req.LiveContentHash = ""
		if lockedSite.CurrentVersionID != nil {
			ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: *lockedSite.CurrentVersionID, OrgID: t.OrgID})
			if err != nil {
				return err
			}
			req.LiveVersionID = ver.ID
			req.LiveContentHash = ver.ContentHash
		}
		c := projectroom.Candidate{
			ID: row.ID, State: row.State, ContentHash: row.ContentHash,
			BaseVersionID: row.BaseVersionID, BaseContentHash: row.BaseContentHash, DiffHash: row.DiffHash,
		}
		var decision projectroom.ApprovalResult
		var derr error
		if approve {
			decision, derr = projectroom.DecideApproval(c, req)
		} else {
			decision, derr = projectroom.DecideReject(c, req)
		}
		if decision.Idempotent {
			out = CandidateDecision{Candidate: candidateFromLock(row), Changed: false}
			return nil
		}
		if derr != nil && !errors.Is(derr, projectroom.ErrCandidateConflict) {
			return derr
		}
		next := decision.State
		var approvedBy, rejectedBy *string
		if next == projectroom.CandidateApproved {
			approvedBy = &t.UserID
		}
		if next == projectroom.CandidateRejected {
			rejectedBy = &t.UserID
		}
		updated, uerr := q.DecideReviewCandidate(ctx, db.DecideReviewCandidateParams{
			State: next, ApprovedBy: approvedBy, RejectedBy: rejectedBy,
			ID: row.ID, OrgID: t.OrgID, RoomID: roomID,
			ContentHash: req.ContentHash, BaseVersionID: req.BaseVersionID, BaseContentHash: req.BaseContentHash,
		})
		if uerr != nil {
			if !isNoRows(uerr) {
				return mapRoomErr(uerr)
			}
			cur, gerr := q.GetReviewCandidate(ctx, db.GetReviewCandidateParams{ID: row.ID, OrgID: t.OrgID, RoomID: roomID})
			if gerr != nil {
				return gerr
			}
			out = CandidateDecision{Candidate: candidateFrom(cur), Changed: false}
			if approve && cur.State == projectroom.CandidateApproved && cur.ContentHash == req.ContentHash && cur.BaseVersionID == req.BaseVersionID {
				return nil
			}
			if !approve && cur.State == projectroom.CandidateRejected && cur.ContentHash == req.ContentHash {
				return nil
			}
			if cur.State == projectroom.CandidateConflict {
				conflict = true
				return nil
			}
			return ErrCandidateState
		}
		kind := "candidate_rejected"
		action := audit.ActionCandidateReject
		if next == projectroom.CandidateApproved {
			kind = "candidate_approved"
			action = audit.ActionCandidateApprove
		} else if next == projectroom.CandidateConflict {
			kind = "candidate_conflict"
			action = audit.ActionCandidateConflict
			conflict = true
		}
		if err := writeActivity(ctx, q, t, roomID, kind, nil, &row.SiteID, &row.BaseVersionID); err != nil {
			return err
		}
		rec.Action = action
		rec.Target = "candidate:" + row.ID
		rec.Metadata = map[string]any{
			"human_user_id":     t.UserID,
			"candidate_id":      row.ID,
			"content_hash":      row.ContentHash,
			"base_version_id":   row.BaseVersionID,
			"base_content_hash": row.BaseContentHash,
			"diff_hash":         row.DiffHash,
			"state":             next,
		}
		if _, err := writeAuditTx(ctx, q, t.OrgID, rec); err != nil {
			return err
		}
		out = CandidateDecision{Candidate: candidateFrom(updated), Changed: true}
		return nil
	})
	if err != nil {
		return out, err
	}
	if conflict {
		return out, projectroom.ErrCandidateConflict
	}
	return out, nil
}

func (s *Store) RoomAccess(ctx context.Context, t Tenant, roomID, siteID string) (string, projectroom.SiteRights, error) {
	var role string
	var rights projectroom.SiteRights
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		mem, err := roomMember(ctx, q, t, roomID)
		if err != nil {
			return err
		}
		role = mem.Role
		if siteID == "" {
			rights.CanRead = true
			return nil
		}
		got, _, err := s.siteRights(ctx, q, t, siteID)
		if err != nil {
			return err
		}
		if _, err := q.GetProjectRoomSite(ctx, db.GetProjectRoomSiteParams{OrgID: t.OrgID, RoomID: roomID, SiteID: siteID}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		rights = got
		return nil
	})
	return role, rights, err
}

func (s *Store) ApplyCandidate(ctx context.Context, t Tenant, roomID string, req projectroom.ApprovalRequest) (Candidate, error) {
	var out Candidate
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		row, err := q.GetReviewCandidateForMember(ctx, db.GetReviewCandidateForMemberParams{
			UserID: t.UserID, ID: req.CandidateID, OrgID: t.OrgID, RoomID: roomID,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		locked, err := lockOrgThenSite(ctx, q, t.OrgID, row.SiteID)
		if err != nil {
			return err
		}
		rights, _, err := s.siteRights(ctx, q, t, row.SiteID)
		if err != nil {
			return err
		}
		if !projectroom.CanAccept(projectroom.Role(row.Role), rights) {
			return ErrRoomForbidden
		}
		c := projectroom.Candidate{
			ID: row.ID, State: row.State, ContentHash: row.ContentHash,
			BaseVersionID: row.BaseVersionID, BaseContentHash: row.BaseContentHash,
		}
		if err := projectroom.CanApply(c, req); err != nil {
			return err
		}
		if locked.CurrentVersionID == nil || *locked.CurrentVersionID != row.BaseVersionID {
			return projectroom.ErrCandidateConflict
		}
		ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: *locked.CurrentVersionID, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		if ver.ContentHash != row.BaseContentHash {
			return projectroom.ErrCandidateConflict
		}
		out = candidateFromMember(row)
		return ErrApplicationDisabled
	})
	return out, err
}

func (s *Store) ProjectReviewControlled(ctx context.Context, t Tenant, siteID string) (bool, error) {
	var controlled bool
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		v, err := q.SiteInEnabledProjectRoom(ctx, db.SiteInEnabledProjectRoomParams{OrgID: t.OrgID, SiteID: siteID})
		if err != nil {
			return err
		}
		controlled = v
		return nil
	})
	return controlled, err
}

func (s *Store) requireRooms(ctx context.Context, q *db.Queries, orgID string) error {
	on, err := q.GetProjectRoomsEnabled(ctx, orgID)
	if err != nil {
		if isNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	if !on {
		return ErrProjectRoomsDisabled
	}
	return nil
}

func roomMember(ctx context.Context, q *db.Queries, t Tenant, roomID string) (db.AppProjectRoomMember, error) {
	mem, err := q.GetProjectRoomMember(ctx, db.GetProjectRoomMemberParams{OrgID: t.OrgID, RoomID: roomID, UserID: t.UserID})
	if err != nil {
		if isNoRows(err) {
			return db.AppProjectRoomMember{}, ErrNotFound
		}
		return db.AppProjectRoomMember{}, err
	}
	return mem, nil
}

func (s *Store) siteRights(ctx context.Context, q *db.Queries, t Tenant, siteID string) (projectroom.SiteRights, db.AppSite, error) {
	site, err := q.GetSite(ctx, db.GetSiteParams{ID: siteID, OrgID: t.OrgID})
	if err != nil {
		if isNoRows(err) {
			return projectroom.SiteRights{}, db.AppSite{}, ErrNotFound
		}
		return projectroom.SiteRights{}, db.AppSite{}, err
	}
	rights := projectroom.SiteRights{CanRead: true, CanEditContent: site.AllowMemberEdits || site.OwnerUserID == t.UserID}
	if !rights.CanEditContent {
		role, err := s.MemberRole(ctx, t.OrgID, t.UserID)
		if err == nil && IsAdminRole(role) {
			rights.CanEditContent = true
		}
	}
	return rights, site, nil
}

func writeActivity(ctx context.Context, q *db.Queries, t Tenant, roomID, kind string, threadID, siteID, versionID *string) error {
	_, err := q.InsertReviewActivity(ctx, db.InsertReviewActivityParams{
		OrgID: t.OrgID, RoomID: roomID, ActorUserID: t.UserID, Kind: kind,
		ThreadID: threadID, SiteID: siteID, VersionID: versionID,
	})
	return err
}

func roomFrom(row db.AppProjectRoom, role string, unread int64) Room {
	return Room{ID: row.ID, OrgID: row.OrgID, Name: row.Name, OwnerUserID: row.OwnerUserID, Role: role, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Unread: unread}
}

func threadFrom(row db.AppReviewThread, role string) ReviewThread {
	return ReviewThread{
		ID: row.ID, RoomID: row.RoomID, SiteID: row.SiteID, VersionID: row.VersionID,
		ContentHash: row.ContentHash, Page: row.Page, AnchorKind: row.AnchorKind,
		Anchor: row.Anchor, AnchorState: row.AnchorState, Status: row.Status,
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, ResolvedBy: row.ResolvedBy,
		ResolvedAt: ptrFromTimestamptz(row.ResolvedAt), Role: role,
	}
}

func candidateFrom(row db.AppReviewCandidate) Candidate {
	return Candidate{
		ID: row.ID, RoomID: row.RoomID, SiteID: row.SiteID, BaseVersionID: row.BaseVersionID,
		BaseContentHash: row.BaseContentHash, ContentHash: row.ContentHash, DiffHash: row.DiffHash,
		Summary: row.Summary, State: row.State, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
		ApprovedBy: row.ApprovedBy, RejectedBy: row.RejectedBy,
	}
}

func candidateFromMember(row db.GetReviewCandidateForMemberRow) Candidate {
	return candidateFromLock(db.LockReviewCandidateForMemberRow{
		ID: row.ID, OrgID: row.OrgID, RoomID: row.RoomID, SiteID: row.SiteID,
		BaseVersionID: row.BaseVersionID, BaseContentHash: row.BaseContentHash,
		ContentHash: row.ContentHash, DiffHash: row.DiffHash, Summary: row.Summary,
		State: row.State, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
		ApprovedBy: row.ApprovedBy, ApprovedAt: row.ApprovedAt,
		RejectedBy: row.RejectedBy, RejectedAt: row.RejectedAt, Role: row.Role,
	})
}

func candidateFromLock(row db.LockReviewCandidateForMemberRow) Candidate {
	return Candidate{
		ID: row.ID, RoomID: row.RoomID, SiteID: row.SiteID, BaseVersionID: row.BaseVersionID,
		BaseContentHash: row.BaseContentHash, ContentHash: row.ContentHash, DiffHash: row.DiffHash,
		Summary: row.Summary, State: row.State, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
		ApprovedBy: row.ApprovedBy, RejectedBy: row.RejectedBy,
	}
}

func cleanRoomName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxRoomName || hasControl(name) {
		return "", projectroom.ErrInvalidAnchor
	}
	return name, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func hex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func mapRoomErr(err error) error {
	if err == nil {
		return nil
	}
	if uniqueViolation(err, "") {
		return ErrRoomConflict
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" && strings.Contains(pgErr.Message, "project room") {
		return fmt.Errorf("%w: %s", ErrRoomConflict, pgErr.Message)
	}
	if isNoRows(err) {
		return ErrNotFound
	}
	return err
}
