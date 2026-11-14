// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5"
)

type CandidateApplication struct {
	Candidate Candidate
	Changed   bool
	VersionID string
	Publish   PublishResult
}

// ApplyCandidate commits the exact approved artifact and its immutable receipt
// together. Repeated requests may repair projection, never replay an old approval
// after a newer publication. The handler verifies object bytes before calling.
func (s *Store) ApplyCandidate(ctx context.Context, t Tenant, roomID string, req projectroom.ApprovalRequest) (CandidateApplication, error) {
	var out CandidateApplication
	if !req.HumanSession || !HumanSessionFromContext(ctx) {
		return out, projectroom.ErrHumanRequired
	}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		peek, err := q.GetReviewCandidateForMember(ctx, db.GetReviewCandidateForMemberParams{UserID: t.UserID, ID: req.CandidateID, OrgID: t.OrgID, RoomID: roomID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		site, err := lockOrgThenSite(ctx, q, t.OrgID, peek.SiteID)
		if err != nil {
			return err
		}
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		// Re-read membership and candidate state after obtaining the mutation locks.
		row, err := q.LockReviewCandidateForMember(ctx, db.LockReviewCandidateForMemberParams{UserID: t.UserID, ID: req.CandidateID, OrgID: t.OrgID, RoomID: roomID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if _, err := q.GetProjectRoomSite(ctx, db.GetProjectRoomSiteParams{OrgID: t.OrgID, RoomID: roomID, SiteID: row.SiteID}); err != nil {
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
		c := projectroom.Candidate{ID: row.ID, State: row.State, ContentHash: row.ContentHash, BaseVersionID: row.BaseVersionID, BaseContentHash: row.BaseContentHash}
		if err := projectroom.CanApply(c, req); err != nil {
			return err
		}
		if req.VerifiedVersionID == "" {
			return ErrCandidateArtifact
		}
		ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: req.VerifiedVersionID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrCandidateArtifact
			}
			return err
		}
		if ver.SiteID != site.ID || ver.ContentHash != row.ContentHash || ver.Status != "ready" {
			return ErrCandidateArtifact
		}
		var appliedVersion string
		err = tx.QueryRow(ctx, `SELECT version_id::text FROM app.candidate_applications WHERE candidate_id=$1 AND org_id=$2`, row.ID, t.OrgID).Scan(&appliedVersion)
		already := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if already {
			if appliedVersion != ver.ID || site.CurrentVersionID == nil || *site.CurrentVersionID != ver.ID {
				return projectroom.ErrCandidateConflict
			}
		} else {
			if site.CurrentVersionID == nil || *site.CurrentVersionID != row.BaseVersionID {
				return projectroom.ErrCandidateConflict
			}
			base, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: row.BaseVersionID, OrgID: t.OrgID})
			if err != nil {
				return err
			}
			if base.SiteID != site.ID || base.ContentHash != row.BaseContentHash {
				return projectroom.ErrCandidateConflict
			}
		}
		out.Publish, err = s.publishLocked(ctx, tx, q, t, site, ver, !already)
		if err != nil {
			return err
		}
		if !already {
			_, err = tx.Exec(ctx, `INSERT INTO app.candidate_applications(candidate_id,org_id,version_id,applied_by) VALUES($1,$2,$3,$4)`, row.ID, t.OrgID, ver.ID, t.UserID)
			if err != nil {
				return err
			}
			previewHosts, err := json.Marshal(append([]string{}, out.Publish.DeletedPreviewHosts...))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO app.candidate_projection_outbox(candidate_id,org_id,site_id,deleted_preview_hosts) VALUES($1,$2,$3,$4) ON CONFLICT(org_id,site_id) DO UPDATE SET candidate_id=EXCLUDED.candidate_id, deleted_preview_hosts=app.candidate_projection_outbox.deleted_preview_hosts || EXCLUDED.deleted_preview_hosts, delivered_at=NULL`, row.ID, t.OrgID, row.SiteID, previewHosts); err != nil {
				return err
			}
			if err := writeActivity(ctx, q, t, roomID, "candidate_applied", nil, &row.SiteID, &ver.ID); err != nil {
				return err
			}
			_, err = writeAuditTx(ctx, q, t.OrgID, AuditRecord{Ctx: audit.Context{ActorUser: t.UserID}, Action: audit.Action("candidate.apply"), Target: "candidate:" + row.ID, Metadata: map[string]any{"candidate_id": row.ID, "version_id": ver.ID, "content_hash": row.ContentHash, "base_version_id": row.BaseVersionID, "base_content_hash": row.BaseContentHash}})
			if err != nil {
				return err
			}
		}
		out.Candidate = candidateFromLock(row)
		out.Candidate.AppliedVersionID = &ver.ID
		out.VersionID, out.Changed = ver.ID, !already
		return nil
	})
	return out, err
}

func loadCandidateReceipt(ctx context.Context, tx pgx.Tx, orgID string, c *Candidate) error {
	var version string
	err := tx.QueryRow(ctx, `SELECT version_id::text FROM app.candidate_applications WHERE candidate_id=$1 AND org_id=$2`, c.ID, orgID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err == nil {
		c.AppliedVersionID = &version
	}
	return err
}
