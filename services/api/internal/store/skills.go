// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// SeedOwnerUserID is the sentinel owner_user_id for skills materialized from
// the embedded preset seeds ("seeded by Dropway"). UIs render it as "Dropway";
// only org admins manage these rows (no real user matches the sentinel).
const SeedOwnerUserID = "00000000-0000-0000-0000-000000000000"

// Skill is a shareable Claude skill (SKILL.md + supporting files).
type Skill struct {
	ID          string
	OrgID       string
	Slug        string
	OwnerUserID string
	// Title / Description come from the create request or the uploaded
	// SKILL.md frontmatter (empty when unset; clients fall back to the slug).
	Title            string
	Description      string
	CurrentVersionID *string
	// SizeBytes is the current version's total size (0 until first upload).
	SizeBytes int64
	// Folders are the skill's folder memberships (with preset flags), populated
	// by the read paths so listings render chips without an N+1.
	Folders   []SkillFolderRef
	CreatedAt time.Time
}

// SkillFolderRef is one folder membership as seen from a skill.
type SkillFolderRef struct {
	FolderID string
	Slug     string
	Title    string
	IsPreset bool
}

// SkillVersion is an immutable, content-addressed skill upload. The latest-only
// v1 model exposes only the current one (finalize flips the pointer).
type SkillVersion struct {
	ID          string
	OrgID       string
	SkillID     string
	VersionNo   int32
	Status      string
	ContentHash string
	SizeBytes   int64
	CreatedBy   string
	CreatedAt   time.Time
}

// CreateSkill inserts a skill for the active tenant and attaches it to
// folderIDs in the same tx. The org skill count and each folder's item count
// run through the quota provider under advisory locks (the same race-safe
// COUNT → policy → INSERT critical section CreateSite uses); the free tier's
// 10-skills-per-folder cap surfaces as a *quota.ExceededError (→ 402).
func (s *Store) CreateSkill(ctx context.Context, t Tenant, skillSlug, title string, folderIDs []string) (Skill, error) {
	if !ValidSlug(skillSlug) {
		return Skill{}, ErrInvalidSlug
	}

	var out Skill
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.LockOrgSkillQuota(ctx, t.OrgID); err != nil {
			return err
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}
		current, err := q.CountSkillsForOrg(ctx, t.OrgID)
		if err != nil {
			return err
		}
		if err := s.quota.Allow(planTier, quota.ResourceSkillPerOrg, current); err != nil {
			return err // *quota.ExceededError → handler renders HTTP 402
		}

		row, err := q.CreateSkill(ctx, db.CreateSkillParams{
			OrgID:       t.OrgID,
			Slug:        skillSlug,
			OwnerUserID: t.UserID,
			Title:       pgtype.Text{String: title, Valid: title != ""},
		})
		if err != nil {
			if uniqueViolation(err, "skills_org_slug_key") {
				return ErrSlugTaken
			}
			return err
		}
		out = skillFromDB(row)

		for _, folderID := range folderIDs {
			if err := s.addFolderItemTx(ctx, q, t, planTier, folderID, row.ID, false); err != nil {
				return err
			}
		}
		refs, err := foldersForSkillsTx(ctx, q, []string{row.ID})
		if err != nil {
			return err
		}
		out.Folders = refs[row.ID]
		return nil
	})
	return out, err
}

// addFolderItemTx adds (or re-flags) a folder membership inside an open tenant
// tx, enforcing the per-folder cap under the folder's advisory lock. The folder
// is re-read first so an absent / other-tenant folder surfaces as
// ErrFolderNotFound instead of an FK error. An UPDATE of an existing
// membership (upsert hit) can't overshoot the cap: the pre-count includes the
// existing row, and count+1 is only checked when the skill isn't a member yet.
func (s *Store) addFolderItemTx(ctx context.Context, q *db.Queries, t Tenant, planTier, folderID, skillID string, isPreset bool) error {
	if _, err := q.GetSkillFolder(ctx, folderID); err != nil {
		if isNoRows(err) {
			return ErrFolderNotFound
		}
		return err
	}
	if err := q.LockSkillFolderQuota(ctx, folderID); err != nil {
		return err
	}
	current, err := q.CountFolderItems(ctx, folderID)
	if err != nil {
		return err
	}
	// Only a genuinely-new membership consumes a slot; re-flagging an existing
	// one must never 402. Cheap existence probe via the upsert's conflict target.
	member, err := isFolderMember(ctx, q, folderID, skillID)
	if err != nil {
		return err
	}
	if !member {
		if err := s.quota.Allow(planTier, quota.ResourceSkillPerFolder, current); err != nil {
			return err // *quota.ExceededError → 402
		}
	}
	return q.UpsertSkillFolderItem(ctx, db.UpsertSkillFolderItemParams{
		OrgID:    t.OrgID,
		FolderID: folderID,
		SkillID:  skillID,
		IsPreset: isPreset,
		AddedBy:  t.UserID,
	})
}

// isFolderMember reports whether the skill is already in the folder (RLS-scoped).
func isFolderMember(ctx context.Context, q *db.Queries, folderID, skillID string) (bool, error) {
	rows, err := q.ListFoldersForSkills(ctx, []string{skillID})
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.FolderID == folderID {
			return true, nil
		}
	}
	return false, nil
}

// ListSkills returns the active org's skills matching the filters, each with
// its folder memberships and current-version size. q (” = all) matches
// slug/title/description; folderSlug (” = any) restricts to a folder;
// presetsOnly additionally requires the preset flag on the membership.
func (s *Store) ListSkills(ctx context.Context, t Tenant, q, folderSlug string, presetsOnly bool) ([]Skill, error) {
	var out []Skill
	err := s.withTx(ctx, t, func(qq *db.Queries) error {
		rows, err := qq.ListSkills(ctx, db.ListSkillsParams{
			CallerID:    t.UserID,
			Q:           q,
			FolderSlug:  folderSlug,
			PresetsOnly: presetsOnly,
		})
		if err != nil {
			return err
		}
		out = make([]Skill, len(rows))
		ids := make([]string, len(rows))
		for i, r := range rows {
			out[i] = skillFromDB(r)
			ids[i] = r.ID
		}
		if err := s.decorateSkills(ctx, qq, out, ids); err != nil {
			return err
		}
		return nil
	})
	return out, err
}

// ListFolderSkills returns every finalized skill in a folder (the bulk-download
// set), decorated like ListSkills. A missing folder is ErrFolderNotFound.
func (s *Store) ListFolderSkills(ctx context.Context, t Tenant, folderID string) ([]Skill, error) {
	var out []Skill
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := q.GetSkillFolder(ctx, folderID); err != nil {
			if isNoRows(err) {
				return ErrFolderNotFound
			}
			return err
		}
		rows, err := q.ListFolderSkills(ctx, folderID)
		if err != nil {
			return err
		}
		out = make([]Skill, len(rows))
		ids := make([]string, len(rows))
		for i, r := range rows {
			out[i] = skillFromDB(r)
			ids[i] = r.ID
		}
		return s.decorateSkills(ctx, q, out, ids)
	})
	return out, err
}

// decorateSkills fills folder memberships + current-version sizes for a slice
// of skills in two round-trips (no N+1).
func (s *Store) decorateSkills(ctx context.Context, q *db.Queries, skills []Skill, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	refs, err := foldersForSkillsTx(ctx, q, ids)
	if err != nil {
		return err
	}
	for i := range skills {
		skills[i].Folders = refs[skills[i].ID]
		if skills[i].CurrentVersionID != nil {
			v, err := q.GetSkillVersion(ctx, *skills[i].CurrentVersionID)
			if err != nil {
				if isNoRows(err) {
					continue
				}
				return err
			}
			skills[i].SizeBytes = v.SizeBytes
		}
	}
	return nil
}

func foldersForSkillsTx(ctx context.Context, q *db.Queries, ids []string) (map[string][]SkillFolderRef, error) {
	rows, err := q.ListFoldersForSkills(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]SkillFolderRef, len(ids))
	for _, r := range rows {
		out[r.SkillID] = append(out[r.SkillID], SkillFolderRef{
			FolderID: r.FolderID,
			Slug:     r.Slug,
			Title:    r.Title,
			IsPreset: r.IsPreset,
		})
	}
	return out, nil
}

// GetSkill returns one skill by id, decorated with folders + size (RLS makes
// other orgs' skills invisible → ErrNotFound, never a cross-tenant leak).
func (s *Store) GetSkill(ctx context.Context, t Tenant, id string) (Skill, error) {
	var out Skill
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetSkill(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = skillFromDB(row)
		skills := []Skill{out}
		if err := s.decorateSkills(ctx, q, skills, []string{row.ID}); err != nil {
			return err
		}
		out = skills[0]
		return nil
	})
	return out, err
}

// GetSkillBySlug is GetSkill keyed by the org-unique slug.
func (s *Store) GetSkillBySlug(ctx context.Context, t Tenant, slug string) (Skill, error) {
	var out Skill
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetSkillBySlug(ctx, slug)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = skillFromDB(row)
		skills := []Skill{out}
		if err := s.decorateSkills(ctx, q, skills, []string{row.ID}); err != nil {
			return err
		}
		out = skills[0]
		return nil
	})
	return out, err
}

// DeleteSkill removes a skill (versions + memberships cascade). Ownership
// (owner-or-admin) is enforced by the handler; RLS scopes the delete to the org.
func (s *Store) DeleteSkill(ctx context.Context, t Tenant, id string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := q.DeleteSkill(ctx, id); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		return nil
	})
}

// SetSkillMeta sets a skill's human metadata (finalize fills these from
// SKILL.md frontmatter when unset). Empty strings store as NULL.
func (s *Store) SetSkillMeta(ctx context.Context, t Tenant, id, title, description string) (Skill, error) {
	var out Skill
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.SetSkillMeta(ctx, db.SetSkillMetaParams{
			ID:          id,
			Title:       pgtype.Text{String: title, Valid: title != ""},
			Description: pgtype.Text{String: description, Valid: description != ""},
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = skillFromDB(row)
		return nil
	})
	return out, err
}

// SetSkillFolders replaces a skill's folder memberships with folderIDs
// (PUT semantics). Existing preset flags on kept folders are preserved; the
// per-folder cap applies to each newly-gained membership. Owner-or-admin is
// enforced by the handler.
func (s *Store) SetSkillFolders(ctx context.Context, t Tenant, skillID string, folderIDs []string) (Skill, error) {
	var out Skill
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetSkill(ctx, skillID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}
		// Preserve preset flags for memberships that survive the replace.
		existing, err := foldersForSkillsTx(ctx, q, []string{skillID})
		if err != nil {
			return err
		}
		preset := map[string]bool{}
		for _, ref := range existing[skillID] {
			preset[ref.FolderID] = ref.IsPreset
		}
		if err := q.DeleteSkillFolderItemsForSkill(ctx, skillID); err != nil {
			return err
		}
		for _, folderID := range folderIDs {
			if err := s.addFolderItemTx(ctx, q, t, planTier, folderID, skillID, preset[folderID]); err != nil {
				return err
			}
		}
		out = skillFromDB(row)
		skills := []Skill{out}
		if err := s.decorateSkills(ctx, q, skills, []string{skillID}); err != nil {
			return err
		}
		out = skills[0]
		return nil
	})
	return out, err
}

// CreateSkillVersionParams carries the inputs for an immutable skill upload.
type CreateSkillVersionParams struct {
	SkillID     string
	ContentHash string // sha256 of the upload manifest (internal/manifest.Digest)
	SizeBytes   int64
	Status      string // "ready" once blobs are verified + manifest written
	// Blobs are the upload's DISTINCT content-addressed blobs (+ server-observed
	// sizes) for the dedup-aware storage meter, exactly like deploys.
	Blobs []BlobSize
}

// CreateSkillVersion inserts the next immutable version for a skill AND flips
// the skill's current_version_id in the same tx — in the latest-only v1 model,
// finalize IS publish. Idempotent on the per-skill content_hash (a re-upload of
// identical content returns the existing version and still ensures the pointer
// is set). Storage is metered under the same per-org advisory lock deploys use.
func (s *Store) CreateSkillVersion(ctx context.Context, t Tenant, p CreateSkillVersionParams) (SkillVersion, error) {
	status := p.Status
	if status == "" {
		status = "ready"
	}

	var out SkillVersion
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		skill, err := q.GetSkill(ctx, p.SkillID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if skill.OrgID != t.OrgID {
			return ErrNotFound // confused-deputy guard (RLS already scopes this)
		}

		// Idempotency: identical content for this skill → reuse the version, but
		// still make sure it is the live one (a crash between insert and flip
		// must be recoverable by re-finalizing).
		if existing, err := q.GetSkillVersionByContentHash(ctx, db.GetSkillVersionByContentHashParams{
			SkillID:     p.SkillID,
			ContentHash: p.ContentHash,
		}); err == nil {
			out = skillVersionFromDB(existing)
			vid := existing.ID
			return q.SetSkillCurrentVersion(ctx, db.SetSkillCurrentVersionParams{
				ID:               p.SkillID,
				CurrentVersionID: &vid,
			})
		} else if !isNoRows(err) {
			return err
		}

		if err := s.accountStorage(ctx, q, t.OrgID, p.Blobs); err != nil {
			return err // *quota.ExceededError → 402; rolls back the whole tx
		}

		nextNo, err := q.NextSkillVersionNo(ctx, p.SkillID)
		if err != nil {
			return err
		}
		row, err := q.CreateSkillVersion(ctx, db.CreateSkillVersionParams{
			OrgID:       t.OrgID,
			SkillID:     p.SkillID,
			VersionNo:   nextNo,
			Status:      status,
			ContentHash: p.ContentHash,
			SizeBytes:   p.SizeBytes,
			CreatedBy:   t.UserID,
		})
		if err != nil {
			return err
		}
		vid := row.ID
		if err := q.SetSkillCurrentVersion(ctx, db.SetSkillCurrentVersionParams{
			ID:               p.SkillID,
			CurrentVersionID: &vid,
		}); err != nil {
			return err
		}
		out = skillVersionFromDB(row)
		return nil
	})
	return out, err
}

// GetSkillVersion returns one skill version by id (RLS-scoped; miss → ErrNotFound).
func (s *Store) GetSkillVersion(ctx context.Context, t Tenant, id string) (SkillVersion, error) {
	var out SkillVersion
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetSkillVersion(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = skillVersionFromDB(row)
		return nil
	})
	return out, err
}

func skillFromDB(r db.AppSkill) Skill {
	s := Skill{
		ID:               r.ID,
		OrgID:            r.OrgID,
		Slug:             r.Slug,
		OwnerUserID:      r.OwnerUserID,
		CurrentVersionID: r.CurrentVersionID,
		CreatedAt:        r.CreatedAt,
	}
	if r.Title.Valid {
		s.Title = r.Title.String
	}
	if r.Description.Valid {
		s.Description = r.Description.String
	}
	return s
}

func skillVersionFromDB(r db.AppSkillVersion) SkillVersion {
	return SkillVersion{
		ID:          r.ID,
		OrgID:       r.OrgID,
		SkillID:     r.SkillID,
		VersionNo:   r.VersionNo,
		Status:      r.Status,
		ContentHash: r.ContentHash,
		SizeBytes:   r.SizeBytes,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
	}
}
