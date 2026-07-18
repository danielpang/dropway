// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"

	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// Collaboration toggles (migration 0014): "allow non-creators to modify".
// Dropway is collaborative by default — any org member may edit any site,
// skill, or chat log. Each setter flips ONE resource's toggle; who may flip
// it (creator-or-admin) is the handler's check, like every role gate. All
// three re-derive the row's org first (the confused-deputy guard).

// SetSiteAllowMemberEdits flips a site's collaboration toggle.
func (s *Store) SetSiteAllowMemberEdits(ctx context.Context, t Tenant, siteID string, allow bool) (Site, error) {
	var out Site
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := assertSiteInOrgTx(ctx, q, t, siteID); err != nil {
			return err
		}
		row, err := q.SetSiteAllowMemberEdits(ctx, db.SetSiteAllowMemberEditsParams{
			ID: siteID, AllowMemberEdits: allow, OrgID: t.OrgID,
		})
		if err != nil {
			return err
		}
		out = siteFromDB(row)
		return nil
	})
	return out, err
}

// SetSkillAllowMemberEdits flips a skill's collaboration toggle.
func (s *Store) SetSkillAllowMemberEdits(ctx context.Context, t Tenant, skillID string, allow bool) (Skill, error) {
	var out Skill
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		existing, err := q.GetSkill(ctx, db.GetSkillParams{ID: skillID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if existing.AppSkill.OrgID != t.OrgID {
			return ErrNotFound
		}
		row, err := q.SetSkillAllowMemberEdits(ctx, db.SetSkillAllowMemberEditsParams{
			ID: skillID, AllowMemberEdits: allow, OrgID: t.OrgID,
		})
		if err != nil {
			return err
		}
		out = skillFromDB(row)
		refs, err := foldersForSkillsTx(ctx, q, t.OrgID, []string{skillID})
		if err != nil {
			return err
		}
		out.Folders = refs[skillID]
		return nil
	})
	return out, err
}

// SetChatLogAllowMemberEdits flips a chat log's collaboration toggle.
func (s *Store) SetChatLogAllowMemberEdits(ctx context.Context, t Tenant, logID string, allow bool) (ChatLog, error) {
	var out ChatLog
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := getChatLogTx(ctx, q, t, logID); err != nil {
			return err
		}
		row, err := q.SetChatLogAllowMemberEdits(ctx, db.SetChatLogAllowMemberEditsParams{
			ID: logID, AllowMemberEdits: allow, OrgID: t.OrgID,
		})
		if err != nil {
			return err
		}
		out = chatLogFromDB(row)
		return nil
	})
	return out, err
}
