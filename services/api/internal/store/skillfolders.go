// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// ErrFolderNotFound is returned when a referenced skill folder is absent (or
// invisible under RLS — never a cross-tenant leak).
var ErrFolderNotFound = errors.New("store: skill folder not found")

// SkillFolder is one admin-curated skill folder (the org's skills taxonomy).
type SkillFolder struct {
	ID        string
	OrgID     string
	Slug      string
	Title     string
	ItemCount int64
	CreatedAt time.Time
}

// DefaultSkillFolder is one of the folders every org starts with (seeded
// lazily with the preset skills; admins may rename/delete them afterwards).
type DefaultSkillFolder struct{ Slug, Title string }

// DefaultSkillFolders are the org defaults, one per starter preset skill.
var DefaultSkillFolders = []DefaultSkillFolder{
	{Slug: "engineering", Title: "Engineering"},
	{Slug: "product", Title: "Product"},
	{Slug: "marketing", Title: "Marketing"},
}

// ListSkillFolders returns the org's folders with item counts, slug-ordered.
func (s *Store) ListSkillFolders(ctx context.Context, t Tenant) ([]SkillFolder, error) {
	var out []SkillFolder
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListSkillFolders(ctx)
		if err != nil {
			return err
		}
		out = make([]SkillFolder, len(rows))
		for i, r := range rows {
			out[i] = SkillFolder{
				ID: r.ID, OrgID: r.OrgID, Slug: r.Slug, Title: r.Title,
				ItemCount: r.ItemCount, CreatedAt: r.CreatedAt,
			}
		}
		return nil
	})
	return out, err
}

// GetSkillFolder returns one folder by id (miss → ErrFolderNotFound).
func (s *Store) GetSkillFolder(ctx context.Context, t Tenant, id string) (SkillFolder, error) {
	var out SkillFolder
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.GetSkillFolder(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return ErrFolderNotFound
			}
			return err
		}
		out = folderFromDB(r)
		return nil
	})
	return out, err
}

// GetSkillFolderBySlug returns one folder by its org-unique slug.
func (s *Store) GetSkillFolderBySlug(ctx context.Context, t Tenant, slug string) (SkillFolder, error) {
	var out SkillFolder
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.GetSkillFolderBySlug(ctx, slug)
		if err != nil {
			if isNoRows(err) {
				return ErrFolderNotFound
			}
			return err
		}
		out = folderFromDB(r)
		return nil
	})
	return out, err
}

// CreateSkillFolder creates a folder (admin-only; the handler enforces role).
func (s *Store) CreateSkillFolder(ctx context.Context, t Tenant, folderSlug, title string) (SkillFolder, error) {
	if !ValidSlug(folderSlug) {
		return SkillFolder{}, ErrInvalidSlug
	}
	if title == "" {
		title = folderSlug
	}
	var out SkillFolder
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.CreateSkillFolder(ctx, db.CreateSkillFolderParams{
			OrgID: t.OrgID, Slug: folderSlug, Title: title,
		})
		if err != nil {
			if uniqueViolation(err, "skill_folders_org_slug_key") {
				return ErrSlugTaken
			}
			return err
		}
		out = folderFromDB(r)
		return nil
	})
	return out, err
}

// RenameSkillFolder retitles a folder (the slug is immutable — it's the
// filter/URL key). Admin-only, enforced by the handler.
func (s *Store) RenameSkillFolder(ctx context.Context, t Tenant, id, title string) (SkillFolder, error) {
	var out SkillFolder
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.RenameSkillFolder(ctx, db.RenameSkillFolderParams{ID: id, Title: title})
		if err != nil {
			if isNoRows(err) {
				return ErrFolderNotFound
			}
			return err
		}
		out = folderFromDB(r)
		return nil
	})
	return out, err
}

// DeleteSkillFolder removes a folder; memberships cascade, skills survive.
func (s *Store) DeleteSkillFolder(ctx context.Context, t Tenant, id string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := q.DeleteSkillFolder(ctx, id); err != nil {
			if isNoRows(err) {
				return ErrFolderNotFound
			}
			return err
		}
		return nil
	})
}

// AddSkillToFolder adds a skill to a folder (or updates its preset flag when
// already a member). The free tier's per-folder cap applies to a genuinely-new
// membership. Role gates (admin, or owner without preset) live in the handler.
func (s *Store) AddSkillToFolder(ctx context.Context, t Tenant, folderID, skillID string, isPreset bool) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := q.GetSkill(ctx, skillID); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}
		return s.addFolderItemTx(ctx, q, t, planTier, folderID, skillID, isPreset)
	})
}

// RemoveSkillFromFolder drops a membership (admin or skill owner; handler-gated).
func (s *Store) RemoveSkillFromFolder(ctx context.Context, t Tenant, folderID, skillID string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := q.RemoveSkillFolderItem(ctx, db.RemoveSkillFolderItemParams{
			FolderID: folderID, SkillID: skillID,
		}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		return nil
	})
}

// SetSkillFolderItemPreset flips a membership's preset flag (admin-only).
func (s *Store) SetSkillFolderItemPreset(ctx context.Context, t Tenant, folderID, skillID string, isPreset bool) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := q.SetSkillFolderItemPreset(ctx, db.SetSkillFolderItemPresetParams{
			FolderID: folderID, SkillID: skillID, IsPreset: isPreset,
		}); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		return nil
	})
}

// SkillSeed is one embedded preset skill, ready to materialize into an org:
// content already staged in the org's blob store by the caller (idempotent,
// content-addressed), manifest digest precomputed.
type SkillSeed struct {
	Slug        string
	Title       string
	Description string
	FolderSlug  string // one of DefaultSkillFolders
	ContentHash string
	SizeBytes   int64
	Blobs       []BlobSize
}

// SeededSkill identifies one skill materialized by SeedOrgSkills, so the
// caller can write its manifest object (the version id is DB-generated).
type SeededSkill struct {
	Slug      string
	SkillID   string
	VersionID string
}

// SeedOrgSkills lazily materializes the default folders + preset skills for
// the active org, exactly once (guarded by org_meta.skills_seeded under an
// advisory lock, so concurrent first-touches serialize). The caller stages the
// seeds' blobs in object storage BEFORE calling and writes each returned
// skill's manifest object AFTER (all idempotent). seeded=false means another
// call already seeded (or is seeding) this org.
//
// Seeded skills carry the SeedOwnerUserID sentinel and land in their folder
// with is_preset=true. Admin curation afterwards is ordinary row edits; the
// seeded flag guarantees Dropway never re-seeds over them. A slug an org
// somehow already uses is skipped rather than clobbered.
func (s *Store) SeedOrgSkills(ctx context.Context, t Tenant, seeds []SkillSeed) (created []SeededSkill, seeded bool, err error) {
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.LockOrgSkillsSeed(ctx, t.OrgID); err != nil {
			return err
		}
		done, err := q.GetOrgSkillsSeeded(ctx, t.OrgID)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		folders := map[string]string{} // slug → id
		for _, def := range DefaultSkillFolders {
			r, err := q.CreateSkillFolder(ctx, db.CreateSkillFolderParams{
				OrgID: t.OrgID, Slug: def.Slug, Title: def.Title,
			})
			if err != nil {
				if uniqueViolation(err, "skill_folders_org_slug_key") {
					existing, gerr := q.GetSkillFolderBySlug(ctx, def.Slug)
					if gerr != nil {
						return gerr
					}
					folders[def.Slug] = existing.ID
					continue
				}
				return err
			}
			folders[def.Slug] = r.ID
		}

		for _, seed := range seeds {
			folderID, ok := folders[seed.FolderSlug]
			if !ok {
				f, err := q.GetSkillFolderBySlug(ctx, seed.FolderSlug)
				if err != nil {
					if isNoRows(err) {
						continue // unknown folder in seed metadata → skip the seed
					}
					return err
				}
				folderID = f.ID
			}

			row, err := q.CreateSkill(ctx, db.CreateSkillParams{
				OrgID:       t.OrgID,
				Slug:        seed.Slug,
				OwnerUserID: SeedOwnerUserID,
				Title:       textOrNull(seed.Title),
				Description: textOrNull(seed.Description),
			})
			if err != nil {
				if uniqueViolation(err, "skills_org_slug_key") {
					continue // the org already uses this slug → never clobber
				}
				return err
			}

			if err := s.accountStorage(ctx, q, t.OrgID, seed.Blobs); err != nil {
				return err // seeding must not dodge the storage meter
			}
			ver, err := q.CreateSkillVersion(ctx, db.CreateSkillVersionParams{
				OrgID:       t.OrgID,
				SkillID:     row.ID,
				VersionNo:   1,
				Status:      "ready",
				ContentHash: seed.ContentHash,
				SizeBytes:   seed.SizeBytes,
				CreatedBy:   SeedOwnerUserID,
			})
			if err != nil {
				return err
			}
			vid := ver.ID
			if err := q.SetSkillCurrentVersion(ctx, db.SetSkillCurrentVersionParams{
				ID: row.ID, CurrentVersionID: &vid,
			}); err != nil {
				return err
			}
			if err := q.UpsertSkillFolderItem(ctx, db.UpsertSkillFolderItemParams{
				OrgID: t.OrgID, FolderID: folderID, SkillID: row.ID,
				IsPreset: true, AddedBy: SeedOwnerUserID,
			}); err != nil {
				return err
			}
			created = append(created, SeededSkill{Slug: seed.Slug, SkillID: row.ID, VersionID: ver.ID})
		}

		if err := q.SetOrgSkillsSeeded(ctx, db.SetOrgSkillsSeededParams{ID: t.OrgID, SkillsSeeded: true}); err != nil {
			return err
		}
		seeded = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return created, seeded, nil
}

// SkillsSeeded reports whether the org's lazy preset seeding already ran (the
// cheap fast-path check before staging seed blobs).
func (s *Store) SkillsSeeded(ctx context.Context, t Tenant) (bool, error) {
	var done bool
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		v, err := q.GetOrgSkillsSeeded(ctx, t.OrgID)
		done = v
		return err
	})
	return done, err
}

func folderFromDB(r db.AppSkillFolder) SkillFolder {
	return SkillFolder{ID: r.ID, OrgID: r.OrgID, Slug: r.Slug, Title: r.Title, CreatedAt: r.CreatedAt}
}

// textOrNull maps "" → SQL NULL so unset metadata round-trips as null.
func textOrNull(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
