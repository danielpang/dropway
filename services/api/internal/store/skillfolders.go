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
		rows, err := q.ListSkillFolders(ctx, t.OrgID)
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
		r, err := q.GetSkillFolder(ctx, db.GetSkillFolderParams{ID: id, OrgID: t.OrgID})
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
		r, err := q.GetSkillFolderBySlug(ctx, db.GetSkillFolderBySlugParams{Slug: slug, OrgID: t.OrgID})
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
		r, err := q.RenameSkillFolder(ctx, db.RenameSkillFolderParams{ID: id, Title: title, OrgID: t.OrgID})
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
		if _, err := q.DeleteSkillFolder(ctx, db.DeleteSkillFolderParams{ID: id, OrgID: t.OrgID}); err != nil {
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
		if _, err := q.GetSkill(ctx, db.GetSkillParams{ID: skillID, OrgID: t.OrgID}); err != nil {
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
			FolderID: folderID, SkillID: skillID, OrgID: t.OrgID,
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
			FolderID: folderID, SkillID: skillID, IsPreset: isPreset, OrgID: t.OrgID,
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

// SeedOrgSkillsStage idempotently materializes the default folders + preset
// skills for the active org — folders, skills, immutable versions, and preset
// folder memberships — but deliberately does NOT set current_version_id or
// org_meta.skills_seeded. The caller writes each returned skill's manifest to
// object storage and THEN calls SeedOrgSkillsPublish, so a preset skill only
// becomes live (and only trips skills_seeded) once its manifest durably exists;
// this closes the window where the GC could delete a current version's
// just-staged, not-yet-manifested blobs.
//
// Every step is conflict-safe (get-or-create folder, insert-or-skip seed skill,
// upsert version, upsert membership), so it can be re-run after a partial
// failure without ever raising 23505 and aborting the transaction. staged=false
// means the org is already fully seeded (nothing to do). Seed content is
// metered but not capped, so an at-cap org can still receive its presets.
//
// Seeded skills carry the SeedOwnerUserID sentinel; a slug already used by a
// real user is skipped rather than clobbered.
func (s *Store) SeedOrgSkillsStage(ctx context.Context, t Tenant, seeds []SkillSeed) (created []SeededSkill, staged bool, err error) {
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.LockOrgSkillsSeed(ctx, t.OrgID); err != nil {
			return err
		}
		done, err := q.GetOrgSkillsSeeded(ctx, t.OrgID)
		if err != nil {
			return err
		}
		if done {
			return nil // already seeded → staged stays false
		}
		staged = true

		folders := map[string]string{} // slug → id
		for _, def := range DefaultSkillFolders {
			r, err := q.GetOrCreateSkillFolder(ctx, db.GetOrCreateSkillFolderParams{
				OrgID: t.OrgID, Slug: def.Slug, Title: def.Title,
			})
			if err != nil {
				return err
			}
			folders[def.Slug] = r.ID
		}

		for _, seed := range seeds {
			folderID, ok := folders[seed.FolderSlug]
			if !ok {
				f, err := q.GetOrCreateSkillFolder(ctx, db.GetOrCreateSkillFolderParams{
					OrgID: t.OrgID, Slug: seed.FolderSlug, Title: seed.FolderSlug,
				})
				if err != nil {
					return err
				}
				folderID = f.ID
				folders[seed.FolderSlug] = folderID
			}

			// Insert the seed skill, or reuse it only if the existing slug is our
			// own seed (never clobber a real user's skill).
			var skillID string
			row, err := q.CreateSeedSkill(ctx, db.CreateSeedSkillParams{
				OrgID:       t.OrgID,
				Slug:        seed.Slug,
				OwnerUserID: SeedOwnerUserID,
				Title:       textOrNull(seed.Title),
				Description: textOrNull(seed.Description),
			})
			switch {
			case err == nil:
				skillID = row.ID
			case isNoRows(err):
				existing, gerr := q.GetSkillBySlug(ctx, db.GetSkillBySlugParams{Slug: seed.Slug, OrgID: t.OrgID})
				if gerr != nil {
					return gerr
				}
				if existing.AppSkill.OwnerUserID != SeedOwnerUserID {
					continue // a real user owns this slug → skip
				}
				skillID = existing.AppSkill.ID
			default:
				return err
			}

			if err := s.meterStorageNoCap(ctx, q, t.OrgID, seed.Blobs); err != nil {
				return err
			}
			ver, err := q.UpsertSkillVersion(ctx, db.UpsertSkillVersionParams{
				OrgID:       t.OrgID,
				SkillID:     skillID,
				VersionNo:   1,
				Status:      "ready",
				ContentHash: seed.ContentHash,
				SizeBytes:   seed.SizeBytes,
				CreatedBy:   SeedOwnerUserID,
			})
			if err != nil {
				return err
			}
			if err := q.UpsertSkillFolderItem(ctx, db.UpsertSkillFolderItemParams{
				OrgID: t.OrgID, FolderID: folderID, SkillID: skillID,
				IsPreset: true, AddedBy: SeedOwnerUserID,
			}); err != nil {
				return err
			}
			created = append(created, SeededSkill{Slug: seed.Slug, SkillID: skillID, VersionID: ver.ID})
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return created, staged, nil
}

// SeedOrgSkillsPublish flips every staged preset skill to its version and marks
// the org seeded, in one tx under the seed advisory lock (idempotent: a no-op
// if another request already published). The caller runs this ONLY after the
// staged versions' manifests are durably written to object storage.
func (s *Store) SeedOrgSkillsPublish(ctx context.Context, t Tenant, created []SeededSkill) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
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
		for _, c := range created {
			vid := c.VersionID
			if err := q.SetSkillCurrentVersion(ctx, db.SetSkillCurrentVersionParams{
				ID: c.SkillID, CurrentVersionID: &vid, OrgID: t.OrgID,
			}); err != nil {
				return err
			}
		}
		return q.SetOrgSkillsSeeded(ctx, db.SetOrgSkillsSeededParams{ID: t.OrgID, SkillsSeeded: true})
	})
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
