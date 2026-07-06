package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// This file extends the unit-test fakeStore with the skills surface (skills,
// folders, preset flags, lazy seeding), using the same sidecar-registry
// pattern as the Phase-2 state. folderCap emulates the cloud free tier's
// 10-skills-per-folder policy so the 402 path is testable without the cloud
// provider.

type skillsState struct {
	skills   map[string]store.Skill        // skillID → skill
	versions map[string]store.SkillVersion // versionID → version
	folders  map[string]store.SkillFolder  // folderID → folder
	items    map[string]map[string]bool    // folderID → skillID → isPreset
	seeded   bool
	nextID   int
	// folderCap: 0 = unlimited; N → a NEW membership 402s when the folder
	// already holds N skills (mirrors quota.ResourceSkillPerFolder).
	folderCap int64
}

var skRegistry = map[*fakeStore]*skillsState{}

func (f *fakeStore) sk() *skillsState {
	s, ok := skRegistry[f]
	if !ok {
		s = &skillsState{
			skills:   map[string]store.Skill{},
			versions: map[string]store.SkillVersion{},
			folders:  map[string]store.SkillFolder{},
			items:    map[string]map[string]bool{},
		}
		skRegistry[f] = s
	}
	return s
}

func (s *skillsState) id(prefix string) string {
	s.nextID++
	return fmt.Sprintf("%s_%d", prefix, s.nextID)
}

func (f *fakeStore) skillFolders(skillID string) []store.SkillFolderRef {
	sk := f.sk()
	var out []store.SkillFolderRef
	for folderID, members := range sk.items {
		preset, ok := members[skillID]
		if !ok {
			continue
		}
		fol := sk.folders[folderID]
		out = append(out, store.SkillFolderRef{FolderID: folderID, Slug: fol.Slug, Title: fol.Title, IsPreset: preset})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

func (f *fakeStore) addItem(t store.Tenant, folderID, skillID string, isPreset bool) error {
	sk := f.sk()
	if _, ok := sk.folders[folderID]; !ok {
		return store.ErrFolderNotFound
	}
	members := sk.items[folderID]
	if members == nil {
		members = map[string]bool{}
		sk.items[folderID] = members
	}
	if _, already := members[skillID]; !already && sk.folderCap > 0 && int64(len(members)) >= sk.folderCap {
		return &quota.ExceededError{
			Limit: quota.ResourceSkillPerFolder, Current: int64(len(members)),
			Max: sk.folderCap, PlanTier: "free", NextTier: "pro",
		}
	}
	members[skillID] = isPreset
	return nil
}

func (f *fakeStore) CreateSkill(_ context.Context, t store.Tenant, slug, title string, folderIDs []string) (store.Skill, error) {
	f.lastTenant = t
	sk := f.sk()
	for _, s := range sk.skills {
		if s.Slug == slug {
			return store.Skill{}, store.ErrSlugTaken
		}
	}
	s := store.Skill{
		ID: sk.id("skill"), OrgID: t.OrgID, Slug: slug, OwnerUserID: t.UserID, Title: title,
	}
	sk.skills[s.ID] = s
	for _, folderID := range folderIDs {
		if err := f.addItem(t, folderID, s.ID, false); err != nil {
			delete(sk.skills, s.ID) // mirror the tx rollback
			return store.Skill{}, err
		}
	}
	s.Folders = f.skillFolders(s.ID)
	return s, nil
}

func (f *fakeStore) decorated(s store.Skill) store.Skill {
	s.Folders = f.skillFolders(s.ID)
	if s.CurrentVersionID != nil {
		if v, ok := f.sk().versions[*s.CurrentVersionID]; ok {
			s.SizeBytes = v.SizeBytes
		}
	}
	return s
}

func (f *fakeStore) ListSkills(_ context.Context, t store.Tenant, q, folderSlug string, presetsOnly bool) ([]store.Skill, error) {
	f.lastTenant = t
	sk := f.sk()
	var out []store.Skill
	for _, s := range sk.skills {
		if s.CurrentVersionID == nil && s.OwnerUserID != t.UserID {
			continue
		}
		if q != "" && !strings.Contains(s.Slug+" "+s.Title+" "+s.Description, q) {
			continue
		}
		s = f.decorated(s)
		if folderSlug != "" || presetsOnly {
			match := false
			for _, ref := range s.Folders {
				if folderSlug != "" && ref.Slug != folderSlug {
					continue
				}
				if presetsOnly && !ref.IsPreset {
					continue
				}
				match = true
			}
			if !match {
				continue
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) GetSkill(_ context.Context, t store.Tenant, id string) (store.Skill, error) {
	f.lastTenant = t
	s, ok := f.sk().skills[id]
	if !ok {
		return store.Skill{}, store.ErrNotFound
	}
	return f.decorated(s), nil
}

func (f *fakeStore) DeleteSkill(_ context.Context, t store.Tenant, id string) error {
	f.lastTenant = t
	sk := f.sk()
	if _, ok := sk.skills[id]; !ok {
		return store.ErrNotFound
	}
	delete(sk.skills, id)
	for _, members := range sk.items {
		delete(members, id)
	}
	return nil
}

func (f *fakeStore) SetSkillMeta(_ context.Context, t store.Tenant, id, title, description string) (store.Skill, error) {
	sk := f.sk()
	s, ok := sk.skills[id]
	if !ok {
		return store.Skill{}, store.ErrNotFound
	}
	s.Title, s.Description = title, description
	sk.skills[id] = s
	return f.decorated(s), nil
}

func (f *fakeStore) SetSkillFolders(_ context.Context, t store.Tenant, skillID string, folderIDs []string) (store.Skill, error) {
	sk := f.sk()
	s, ok := sk.skills[skillID]
	if !ok {
		return store.Skill{}, store.ErrNotFound
	}
	// Preserve preset flags for kept folders (mirrors the real store).
	preset := map[string]bool{}
	for folderID, members := range sk.items {
		if p, ok := members[skillID]; ok {
			preset[folderID] = p
			delete(members, skillID)
		}
	}
	for _, folderID := range folderIDs {
		if err := f.addItem(t, folderID, skillID, preset[folderID]); err != nil {
			return store.Skill{}, err
		}
	}
	return f.decorated(s), nil
}

func (f *fakeStore) CreateSkillVersion(_ context.Context, t store.Tenant, p store.CreateSkillVersionParams) (store.SkillVersion, error) {
	f.lastTenant = t
	sk := f.sk()
	s, ok := sk.skills[p.SkillID]
	if !ok {
		return store.SkillVersion{}, store.ErrNotFound
	}
	_ = s
	// Idempotent on content hash. Mirrors the real store: does NOT flip the live
	// pointer — the handler publishes via PublishSkillVersion after the manifest
	// is written.
	for _, v := range sk.versions {
		if v.SkillID == p.SkillID && v.ContentHash == p.ContentHash {
			return v, nil
		}
	}
	var maxNo int32
	for _, v := range sk.versions {
		if v.SkillID == p.SkillID && v.VersionNo > maxNo {
			maxNo = v.VersionNo
		}
	}
	v := store.SkillVersion{
		ID: sk.id("skillver"), OrgID: t.OrgID, SkillID: p.SkillID, VersionNo: maxNo + 1,
		Status: "ready", ContentHash: p.ContentHash, SizeBytes: p.SizeBytes, CreatedBy: t.UserID,
	}
	sk.versions[v.ID] = v
	return v, nil
}

func (f *fakeStore) PublishSkillVersion(_ context.Context, t store.Tenant, skillID, versionID string) error {
	sk := f.sk()
	s, ok := sk.skills[skillID]
	if !ok {
		return store.ErrNotFound
	}
	v, ok := sk.versions[versionID]
	if !ok || v.SkillID != skillID {
		return store.ErrNotFound
	}
	vid := versionID
	s.CurrentVersionID = &vid
	sk.skills[skillID] = s
	return nil
}

func (f *fakeStore) ListSkillFolders(_ context.Context, t store.Tenant) ([]store.SkillFolder, error) {
	f.lastTenant = t
	sk := f.sk()
	out := make([]store.SkillFolder, 0, len(sk.folders))
	for id, fol := range sk.folders {
		fol.ItemCount = int64(len(sk.items[id]))
		out = append(out, fol)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (f *fakeStore) GetSkillFolder(_ context.Context, t store.Tenant, id string) (store.SkillFolder, error) {
	fol, ok := f.sk().folders[id]
	if !ok {
		return store.SkillFolder{}, store.ErrFolderNotFound
	}
	fol.ItemCount = int64(len(f.sk().items[id]))
	return fol, nil
}

func (f *fakeStore) CreateSkillFolder(_ context.Context, t store.Tenant, slug, title string) (store.SkillFolder, error) {
	f.lastTenant = t
	sk := f.sk()
	for _, fol := range sk.folders {
		if fol.Slug == slug {
			return store.SkillFolder{}, store.ErrSlugTaken
		}
	}
	if title == "" {
		title = slug
	}
	fol := store.SkillFolder{ID: sk.id("folder"), OrgID: t.OrgID, Slug: slug, Title: title}
	sk.folders[fol.ID] = fol
	return fol, nil
}

func (f *fakeStore) RenameSkillFolder(_ context.Context, t store.Tenant, id, title string) (store.SkillFolder, error) {
	sk := f.sk()
	fol, ok := sk.folders[id]
	if !ok {
		return store.SkillFolder{}, store.ErrFolderNotFound
	}
	fol.Title = title
	sk.folders[id] = fol
	return fol, nil
}

func (f *fakeStore) DeleteSkillFolder(_ context.Context, t store.Tenant, id string) error {
	sk := f.sk()
	if _, ok := sk.folders[id]; !ok {
		return store.ErrFolderNotFound
	}
	delete(sk.folders, id)
	delete(sk.items, id)
	return nil
}

func (f *fakeStore) AddSkillToFolder(_ context.Context, t store.Tenant, folderID, skillID string, isPreset bool) error {
	if _, ok := f.sk().skills[skillID]; !ok {
		return store.ErrNotFound
	}
	return f.addItem(t, folderID, skillID, isPreset)
}

func (f *fakeStore) RemoveSkillFromFolder(_ context.Context, t store.Tenant, folderID, skillID string) error {
	members, ok := f.sk().items[folderID]
	if !ok {
		return store.ErrNotFound
	}
	if _, ok := members[skillID]; !ok {
		return store.ErrNotFound
	}
	delete(members, skillID)
	return nil
}

func (f *fakeStore) SetSkillFolderItemPreset(_ context.Context, t store.Tenant, folderID, skillID string, isPreset bool) error {
	members, ok := f.sk().items[folderID]
	if !ok {
		return store.ErrNotFound
	}
	if _, ok := members[skillID]; !ok {
		return store.ErrNotFound
	}
	members[skillID] = isPreset
	return nil
}

func (f *fakeStore) ListFolderSkills(_ context.Context, t store.Tenant, folderID string) ([]store.Skill, error) {
	sk := f.sk()
	if _, ok := sk.folders[folderID]; !ok {
		return nil, store.ErrFolderNotFound
	}
	var out []store.Skill
	for skillID := range sk.items[folderID] {
		s, ok := sk.skills[skillID]
		if !ok || s.CurrentVersionID == nil {
			continue
		}
		out = append(out, f.decorated(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (f *fakeStore) SkillsSeeded(_ context.Context, t store.Tenant) (bool, error) {
	return f.sk().seeded, nil
}

// SeedOrgSkillsStage mirrors the real store: idempotently materializes folders +
// seed skills + versions (NOT current) + preset memberships, without setting the
// seeded flag. Re-running is safe (get-or-create semantics).
func (f *fakeStore) SeedOrgSkillsStage(ctx context.Context, t store.Tenant, seeds []store.SkillSeed) ([]store.SeededSkill, bool, error) {
	sk := f.sk()
	if sk.seeded {
		return nil, false, nil
	}
	for _, def := range store.DefaultSkillFolders {
		if _, err := f.CreateSkillFolder(ctx, t, def.Slug, def.Title); err != nil && err != store.ErrSlugTaken {
			return nil, false, err
		}
	}
	folderBySlug := map[string]string{}
	for id, fol := range sk.folders {
		folderBySlug[fol.Slug] = id
	}
	seedTenant := store.Tenant{OrgID: t.OrgID, UserID: store.SeedOwnerUserID}
	var created []store.SeededSkill
	for _, seed := range seeds {
		// Get-or-create the seed skill by slug (reuse only our own seed).
		var skillID string
		if existing, ok := f.skillBySlug(seed.Slug); ok {
			if existing.OwnerUserID != store.SeedOwnerUserID {
				continue
			}
			skillID = existing.ID
		} else {
			s, err := f.CreateSkill(ctx, seedTenant, seed.Slug, seed.Title, nil)
			if err != nil {
				return nil, false, err
			}
			skillID = s.ID
		}
		v, err := f.CreateSkillVersion(ctx, seedTenant, store.CreateSkillVersionParams{
			SkillID: skillID, ContentHash: seed.ContentHash, SizeBytes: seed.SizeBytes,
		})
		if err != nil {
			return nil, false, err
		}
		if folderID, ok := folderBySlug[seed.FolderSlug]; ok {
			if err := f.addItem(t, folderID, skillID, true); err != nil {
				return nil, false, err
			}
		}
		created = append(created, store.SeededSkill{Slug: seed.Slug, SkillID: skillID, VersionID: v.ID})
	}
	return created, true, nil
}

// SeedOrgSkillsPublish flips each staged seed skill to its version + marks seeded.
func (f *fakeStore) SeedOrgSkillsPublish(ctx context.Context, t store.Tenant, created []store.SeededSkill) error {
	sk := f.sk()
	if sk.seeded {
		return nil
	}
	for _, c := range created {
		if err := f.PublishSkillVersion(ctx, t, c.SkillID, c.VersionID); err != nil {
			return err
		}
	}
	sk.seeded = true
	return nil
}

func (f *fakeStore) skillBySlug(slug string) (store.Skill, bool) {
	for _, s := range f.sk().skills {
		if s.Slug == slug {
			return s, true
		}
	}
	return store.Skill{}, false
}
