// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/mail"
	"strings"
	"time"

	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5"
)

type ProjectShare struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}
type SharedSite struct {
	ID                 string  `json:"id"`
	Slug               string  `json:"slug"`
	Title              string  `json:"title"`
	CurrentVersionID   *string `json:"current_version_id"`
	CurrentContentHash string  `json:"current_content_hash"`
}
type SharedProject struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Role  string       `json:"role"`
	Sites []SharedSite `json:"sites"`
}
type GuestReply struct {
	ID        string    `json:"id"`
	AuthorID  string    `json:"author_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}
type GuestThread struct {
	ID                string          `json:"id"`
	SiteID            string          `json:"site_id"`
	VersionID         string          `json:"version_id"`
	ContentHash       string          `json:"content_hash"`
	Page              string          `json:"page"`
	AnchorKind        string          `json:"anchor_kind"`
	Anchor            json.RawMessage `json:"anchor"`
	AnchorState       string          `json:"anchor_state"`
	Status            string          `json:"status"`
	CreatedAt         time.Time       `json:"created_at"`
	Replies           []GuestReply    `json:"replies"`
	RepliesNextCursor string          `json:"replies_next_cursor,omitempty"`
}
type GuestThreads struct {
	Threads    []GuestThread `json:"threads"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

const shareCols = `id::text,email,role,expires_at,accepted_at,revoked_at`

func scanShare(row pgx.Row) (g ProjectShare, err error) {
	err = row.Scan(&g.ID, &g.Email, &g.Role, &g.ExpiresAt, &g.AcceptedAt, &g.RevokedAt)
	return
}
func shareHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Share management requires live organization membership and project ownership.
func (s *Store) manageShares(ctx context.Context, tx pgx.Tx, t Tenant, roomID string) error {
	if !HumanSessionFromContext(ctx) {
		return ErrRoomForbidden
	}
	_, err := credentialMember(ctx, tx, t)
	if err != nil {
		return ErrRoomForbidden
	}
	var owner string
	if err := tx.QueryRow(ctx, `SELECT owner_user_id::text FROM app.project_rooms WHERE org_id=$1 AND id=$2 FOR SHARE`, t.OrgID, roomID).Scan(&owner); err != nil {
		return ErrNotFound
	}
	if owner != t.UserID {
		return ErrRoomForbidden
	}
	return nil
}
func (s *Store) ListProjectShares(ctx context.Context, t Tenant, roomID string) ([]ProjectShare, error) {
	out := []ProjectShare{}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		if err := s.manageShares(ctx, tx, t, roomID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+shareCols+` FROM app.project_guest_shares WHERE org_id=$1 AND room_id=$2 ORDER BY (revoked_at IS NULL AND (accepted_at IS NOT NULL OR expires_at>now())) DESC,created_at DESC LIMIT 200`, t.OrgID, roomID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			g, err := scanShare(rows)
			if err != nil {
				return err
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	return out, err
}
func (s *Store) InviteProjectGuest(ctx context.Context, t Tenant, roomID, email, role string, days int) (ProjectShare, string, error) {
	var out ProjectShare
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || (role != "viewer" && role != "reviewer") {
		return out, "", projectroom.ErrInvalidAnchor
	}
	if days == 0 {
		days = 7
	}
	if days < 1 || days > 30 {
		return out, "", projectroom.ErrInvalidAnchor
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return out, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	err = s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		if s.collaborationAllowed == nil || !s.collaborationAllowed(ctx, t.UserID, t.OrgID) {
			return ErrRoomForbidden
		}
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT project_rooms_enabled AND allow_external_sharing FROM app.org_meta WHERE id=$1 FOR SHARE`, t.OrgID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return ErrRoomForbidden
		}
		if err := s.manageShares(ctx, tx, t, roomID); err != nil {
			return err
		}
		// Bound invitations and serialize replacement for the same room.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, t.OrgID+":guest-shares"); err != nil {
			return err
		}
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.audit_log WHERE org_id=$1 AND action='project.guest.invite' AND created_at>now()-interval '1 hour'`, t.OrgID).Scan(&recent); err != nil {
			return err
		}
		if recent >= 50 {
			return ErrRoomConflict
		}
		var tooSoon bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app.project_guest_shares WHERE org_id=$1 AND room_id=$2 AND email=$3 AND last_invited_at>now()-interval '60 seconds')`, t.OrgID, roomID, email).Scan(&tooSoon); err != nil {
			return err
		}
		if tooSoon {
			return ErrRoomConflict
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.project_guest_shares WHERE org_id=$1 AND room_id=$2 AND email<>$3 AND revoked_at IS NULL AND (accepted_at IS NOT NULL OR expires_at>now())`, t.OrgID, roomID, email).Scan(&count); err != nil {
			return err
		}
		if count >= 200 {
			return ErrRoomConflict
		}
		out, err = scanShare(tx.QueryRow(ctx, `INSERT INTO app.project_guest_shares(org_id,room_id,email,role,token_hash,invited_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,now()+($7::int*interval '1 day')) ON CONFLICT(org_id,room_id,email) DO UPDATE SET last_invited_at=now(),role=excluded.role,token_hash=excluded.token_hash,invited_by=excluded.invited_by,expires_at=excluded.expires_at,accepted_at=CASE WHEN project_guest_shares.revoked_at IS NOT NULL THEN NULL ELSE project_guest_shares.accepted_at END,accepted_user_id=CASE WHEN project_guest_shares.revoked_at IS NOT NULL THEN NULL ELSE project_guest_shares.accepted_user_id END,revoked_at=NULL RETURNING `+shareCols, t.OrgID, roomID, email, role, shareHash(token), t.UserID, days))
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "project.guest.invite", "room:"+roomID)
	})
	if err != nil {
		return out, "", err
	}
	return out, token, nil
}
func (s *Store) RevokeProjectGuest(ctx context.Context, t Tenant, roomID, id string) error {
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		if err := s.manageShares(ctx, tx, t, roomID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE app.project_guest_shares SET revoked_at=COALESCE(revoked_at,now()) WHERE org_id=$1 AND room_id=$2 AND id=$3`, t.OrgID, roomID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "project.guest.revoke", "share:"+id)
	})
}
func (s *Store) discoverGuest(ctx context.Context, user, room, token string) ([][2]string, error) {
	var rp, tp any
	if room != "" {
		rp = room
	}
	if token != "" {
		tp = shareHash(token)
	}
	rows, err := s.pool.Query(ctx, `SELECT org_id::text,room_id::text FROM app.discover_guest_projects($1,$2,$3)`, user, tp, rp)
	if err != nil {
		return nil, ErrRoomForbidden
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var x [2]string
		if err := rows.Scan(&x[0], &x[1]); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// withGuest derives the host tenant from a live invitation. It never trusts the
// JWT's org claim or manufactures organization/project-room membership.
func (s *Store) withGuest(ctx context.Context, user, room, token string, write bool, fn func(pgx.Tx, *db.Queries, Tenant, string) error) error {
	if !HumanSessionFromContext(ctx) {
		return ErrRoomForbidden
	}
	scopes, err := s.discoverGuest(ctx, user, room, token)
	if err != nil || len(scopes) != 1 {
		return ErrRoomForbidden
	}
	t := Tenant{OrgID: scopes[0][0], UserID: user}
	room = scopes[0][1]
	if s.collaborationAllowed == nil || !s.collaborationAllowed(ctx, user, t.OrgID) {
		return ErrRoomForbidden
	}
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		// Order matches owner policy mutations. SHARE locks prevent revocation or
		// policy/email changes from committing between authorization and writes.
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT project_rooms_enabled AND allow_external_sharing FROM app.org_meta WHERE id=$1 FOR SHARE`, t.OrgID).Scan(&allowed); err != nil || !allowed {
			return ErrRoomForbidden
		}
		var email string
		if err := tx.QueryRow(ctx, `SELECT lower(email) FROM identity."user" WHERE id=$1 AND "emailVerified"=true AND COALESCE((to_jsonb("user")->>'banned')::boolean,false)=false FOR SHARE`, user).Scan(&email); err != nil {
			return ErrRoomForbidden
		}
		var role string
		if token != "" {
			err = tx.QueryRow(ctx, `UPDATE app.project_guest_shares SET accepted_user_id=$1,accepted_at=COALESCE(accepted_at,now()) WHERE org_id=$2 AND room_id=$3 AND email=$4 AND token_hash=$5 AND revoked_at IS NULL AND expires_at>now() AND (accepted_user_id IS NULL OR accepted_user_id=$1) RETURNING role`, user, t.OrgID, room, email, shareHash(token)).Scan(&role)
		} else {
			err = tx.QueryRow(ctx, `SELECT role FROM app.project_guest_shares WHERE org_id=$1 AND room_id=$2 AND email=$3 AND accepted_user_id=$4 AND accepted_at IS NOT NULL AND revoked_at IS NULL FOR SHARE`, t.OrgID, room, email, user).Scan(&role)
		}
		if err != nil || (write && role != "reviewer") {
			return ErrRoomForbidden
		}
		return fn(tx, q, t, role)
	})
}
func guestProject(ctx context.Context, tx pgx.Tx, t Tenant, room, role string) (SharedProject, error) {
	out := SharedProject{ID: room, Role: role, Sites: []SharedSite{}}
	if err := tx.QueryRow(ctx, `SELECT name FROM app.project_rooms WHERE org_id=$1 AND id=$2`, t.OrgID, room).Scan(&out.Name); err != nil {
		return out, ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT s.id::text,s.slug,COALESCE(s.title,''),s.current_version_id::text,COALESCE(v.content_hash,'') FROM app.project_room_sites rs JOIN app.sites s ON s.id=rs.site_id AND s.org_id=rs.org_id LEFT JOIN app.site_versions v ON v.id=s.current_version_id AND v.org_id=s.org_id WHERE rs.org_id=$1 AND rs.room_id=$2 ORDER BY s.created_at,s.id LIMIT 200`, t.OrgID, room)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var site SharedSite
		if err := rows.Scan(&site.ID, &site.Slug, &site.Title, &site.CurrentVersionID, &site.CurrentContentHash); err != nil {
			return out, err
		}
		out.Sites = append(out.Sites, site)
	}
	return out, rows.Err()
}
func (s *Store) GuestProject(ctx context.Context, user, room, token string) (SharedProject, error) {
	var out SharedProject
	err := s.withGuest(ctx, user, room, token, false, func(tx pgx.Tx, _ *db.Queries, t Tenant, role string) error {
		if room == "" {
			if err := tx.QueryRow(ctx, `SELECT room_id::text FROM app.project_guest_shares WHERE org_id=$1 AND token_hash=$2`, t.OrgID, shareHash(token)).Scan(&room); err != nil {
				return err
			}
		}
		var err error
		out, err = guestProject(ctx, tx, t, room, role)
		return err
	})
	return out, err
}
func (s *Store) ListGuestProjects(ctx context.Context, user string) ([]SharedProject, error) {
	if !HumanSessionFromContext(ctx) {
		return nil, ErrRoomForbidden
	}
	ids, err := s.discoverGuest(ctx, user, "", "")
	if err != nil {
		return nil, err
	}
	out := []SharedProject{}
	for _, id := range ids {
		p, err := s.GuestProject(ctx, user, id[1], "")
		if err == ErrRoomForbidden || err == ErrNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func guestVersion(ctx context.Context, tx pgx.Tx, t Tenant, room, site, version string) (SiteVersion, error) {
	var v SiteVersion
	// Historical reads are limited to versions referenced by this room's review
	// threads; attaching a site does not expose its unrelated deployment history.
	err := tx.QueryRow(ctx, `SELECT v.id::text,v.site_id::text,v.content_hash FROM app.project_room_sites rs JOIN app.sites s ON s.id=rs.site_id AND s.org_id=rs.org_id JOIN app.site_versions v ON v.site_id=s.id AND v.org_id=s.org_id WHERE rs.org_id=$1 AND rs.room_id=$2 AND s.id=$3 AND v.id=$4 AND (s.current_version_id=v.id OR EXISTS(SELECT 1 FROM app.review_threads th WHERE th.org_id=rs.org_id AND th.room_id=rs.room_id AND th.site_id=s.id AND th.version_id=v.id)) FOR SHARE OF rs`, t.OrgID, room, site, version).Scan(&v.ID, &v.SiteID, &v.ContentHash)
	v.OrgID = t.OrgID
	if err != nil {
		return v, ErrNotFound
	}
	return v, nil
}
func (s *Store) GuestVersion(ctx context.Context, user, room, site, version string) (SiteVersion, error) {
	var out SiteVersion
	err := s.withGuest(ctx, user, room, "", false, func(tx pgx.Tx, _ *db.Queries, t Tenant, _ string) error {
		var err error
		out, err = guestVersion(ctx, tx, t, room, site, version)
		return err
	})
	return out, err
}

const guestThreadCols = `th.id::text,th.site_id::text,th.version_id::text,th.content_hash,th.page,th.anchor_kind,th.anchor,th.anchor_state,th.status,th.created_at`

func scanGuestThread(row pgx.Row) (v GuestThread, err error) {
	v.Replies = []GuestReply{}
	err = row.Scan(&v.ID, &v.SiteID, &v.VersionID, &v.ContentHash, &v.Page, &v.AnchorKind, &v.Anchor, &v.AnchorState, &v.Status, &v.CreatedAt)
	return
}
func (s *Store) guestReplies(ctx context.Context, tx pgx.Tx, t Tenant, room string, thread *GuestThread, cursor string, limit int) error {
	binding := strings.Join([]string{"guest_replies", t.OrgID, t.UserID, room, thread.ID}, "\x00")
	page, err := s.reviewCursors.Begin(cursor, binding, time.Now(), false)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,author_user_id::text,body,created_at FROM app.review_replies WHERE org_id=$1 AND thread_id=$2 AND (created_at,id::text)>($3,$4) AND created_at<=$5 ORDER BY created_at,id LIMIT $6`, t.OrgID, thread.ID, page.At, page.ID, page.Watermark, limit+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r GuestReply
		if err := rows.Scan(&r.ID, &r.AuthorID, &r.Body, &r.CreatedAt); err != nil {
			return err
		}
		thread.Replies = append(thread.Replies, r)
	}
	if len(thread.Replies) > limit {
		thread.Replies = thread.Replies[:limit]
		last := thread.Replies[limit-1]
		thread.RepliesNextCursor, err = s.reviewCursors.Next(page, binding, last.CreatedAt, "reply", last.ID)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}
func (s *Store) GuestThreads(ctx context.Context, user, room, site, threadID, cursor string) (GuestThreads, error) {
	out := GuestThreads{Threads: []GuestThread{}}
	err := s.withGuest(ctx, user, room, "", false, func(tx pgx.Tx, _ *db.Queries, t Tenant, _ string) error {
		binding := strings.Join([]string{"guest_threads", t.OrgID, t.UserID, room, site}, "\x00")
		threadCursor := ""
		if threadID == "" {
			threadCursor = cursor
		}
		page, err := s.reviewCursors.Begin(threadCursor, binding, time.Now(), false)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+guestThreadCols+` FROM app.review_threads th JOIN app.project_room_sites rs ON rs.org_id=th.org_id AND rs.room_id=th.room_id AND rs.site_id=th.site_id WHERE th.org_id=$1 AND th.room_id=$2 AND ($3::text='' OR th.site_id::text=$3) AND ($4::text='' OR th.id::text=$4) AND (th.created_at,th.id::text)>($5,$6) AND th.created_at<=$7 ORDER BY th.created_at,th.id LIMIT 21 FOR SHARE OF rs`, t.OrgID, room, site, threadID, page.At, page.ID, page.Watermark)
		if err != nil {
			return err
		}
		for rows.Next() {
			th, err := scanGuestThread(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out.Threads = append(out.Threads, th)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if threadID != "" && len(out.Threads) == 0 {
			return ErrNotFound
		}
		if len(out.Threads) > 20 {
			out.Threads = out.Threads[:20]
			last := out.Threads[19]
			out.NextCursor, err = s.reviewCursors.Next(page, binding, last.CreatedAt, "thread", last.ID)
			if err != nil {
				return err
			}
		}
		for i := range out.Threads {
			replyCursor := ""
			limit := 5
			if threadID != "" {
				replyCursor = cursor
				limit = 50
			}
			if err := s.guestReplies(ctx, tx, t, room, &out.Threads[i], replyCursor, limit); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if len(raw) > 1<<20 {
			return ErrReviewPageTooLarge
		}
		return nil
	})
	return out, err
}
func validGuestBody(body string) bool {
	return len(strings.TrimSpace(body)) > 0 && len(body) <= 4000 && !hasControl(strings.ReplaceAll(strings.ReplaceAll(body, "\n", ""), "\t", ""))
}
func (s *Store) GuestCreateThread(ctx context.Context, user, room, site, version, hash, page, kind string, anchor json.RawMessage, body string) (GuestThread, error) {
	var out GuestThread
	parsed, err := projectroom.ValidateAnchor(kind, anchor)
	if err != nil {
		return out, err
	}
	page, err = projectroom.ValidatePage(page)
	if err != nil {
		return out, err
	}
	if !validGuestBody(body) {
		return out, projectroom.ErrInvalidAnchor
	}
	err = s.withGuest(ctx, user, room, "", true, func(tx pgx.Tx, q *db.Queries, t Tenant, _ string) error {
		ver, err := guestVersion(ctx, tx, t, room, site, version)
		if err != nil {
			return err
		}
		if ver.ContentHash != hash {
			return ErrVersionMismatch
		}
		row, err := q.InsertReviewThread(ctx, db.InsertReviewThreadParams{OrgID: t.OrgID, RoomID: room, SiteID: site, VersionID: version, ContentHash: hash, Page: page, AnchorKind: parsed.Kind, Anchor: parsed.JSON, CreatedBy: user})
		if err != nil {
			return err
		}
		if _, err := q.InsertReviewAnchorRevision(ctx, db.InsertReviewAnchorRevisionParams{OrgID: t.OrgID, ThreadID: row.ID, AnchorKind: parsed.Kind, Anchor: parsed.JSON, Page: page, AnchorState: projectroom.AnchorAnchored, Reason: "created", ActorUserID: user}); err != nil {
			return err
		}
		reply, err := q.InsertReviewReply(ctx, db.InsertReviewReplyParams{OrgID: t.OrgID, ThreadID: row.ID, AuthorUserID: user, Body: strings.TrimSpace(body)})
		if err != nil {
			return err
		}
		out = GuestThread{ID: row.ID, SiteID: site, VersionID: version, ContentHash: hash, Page: page, AnchorKind: parsed.Kind, Anchor: parsed.JSON, AnchorState: row.AnchorState, Status: row.Status, CreatedAt: row.CreatedAt, Replies: []GuestReply{{ID: reply.ID, AuthorID: user, Body: reply.Body, CreatedAt: reply.CreatedAt}}}
		return writeActivity(ctx, q, t, room, "thread_created", &row.ID, &site, &version)
	})
	return out, err
}
func (s *Store) GuestAddReply(ctx context.Context, user, room, thread, body string) (GuestReply, error) {
	var out GuestReply
	if !validGuestBody(body) {
		return out, projectroom.ErrInvalidAnchor
	}
	err := s.withGuest(ctx, user, room, "", true, func(tx pgx.Tx, q *db.Queries, t Tenant, _ string) error {
		var site, version string
		if err := tx.QueryRow(ctx, `SELECT th.site_id::text,th.version_id::text FROM app.review_threads th JOIN app.project_room_sites rs ON rs.org_id=th.org_id AND rs.room_id=th.room_id AND rs.site_id=th.site_id WHERE th.org_id=$1 AND th.room_id=$2 AND th.id=$3 FOR SHARE OF rs`, t.OrgID, room, thread).Scan(&site, &version); err != nil {
			return ErrNotFound
		}
		reply, err := q.InsertReviewReply(ctx, db.InsertReviewReplyParams{OrgID: t.OrgID, ThreadID: thread, AuthorUserID: user, Body: strings.TrimSpace(body)})
		if err != nil {
			return err
		}
		out = GuestReply{ID: reply.ID, AuthorID: user, Body: reply.Body, CreatedAt: reply.CreatedAt}
		return writeActivity(ctx, q, t, room, "reply_created", &thread, &site, &version)
	})
	return out, err
}
func (s *Store) GuestSignoff(ctx context.Context, user, room, site, version, hash string) error {
	return s.withGuest(ctx, user, room, "", true, func(tx pgx.Tx, q *db.Queries, t Tenant, _ string) error {
		ver, err := guestVersion(ctx, tx, t, room, site, version)
		if err != nil {
			return err
		}
		if ver.ContentHash != hash {
			return ErrVersionMismatch
		}
		_, err = q.InsertReviewApproval(ctx, db.InsertReviewApprovalParams{OrgID: t.OrgID, RoomID: room, SiteID: site, VersionID: version, ContentHash: hash, UserID: user})
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return writeActivity(ctx, q, t, room, "version_signoff", nil, &site, &version)
	})
}
