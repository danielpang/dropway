//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/skillseeds"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// TestIntegration_SkillsSeedingCollision reproduces the aborted-transaction bug
// against real Postgres: when an org already holds a folder slug or a skill slug
// that a preset would use, seeding must still succeed (get-or-create folder;
// insert-or-skip skill) rather than raising 23505 and aborting the whole tx.
func TestIntegration_SkillsSeedingCollision(t *testing.T) {
	ctx := context.Background()
	repoRoot := repoRoot(t)

	startPostgres(t)
	startMinio(t)
	applyMigrations(t, repoRoot)

	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})
	obj := newMinioStore(t, ctx)
	if err := obj.EnsureBucket(ctx); err != nil {
		t.Fatalf("ensure bucket: %v", err)
	}

	org := "55555555-5555-5555-5555-555555555555"
	user := "e0000000-0000-0000-0000-000000000001"
	tn := store.Tenant{OrgID: org, UserID: user}
	mustExec(t, "INSERT INTO app.org_meta (id) VALUES ($1)", org)
	seedAuthOrg(t, org, "orge")
	must(t, st.EnsureOrgProvisioned(ctx, tn))

	seeds, err := skillseeds.Load()
	if err != nil {
		t.Fatalf("load seeds: %v", err)
	}
	if len(seeds) == 0 {
		t.Fatal("no embedded seeds")
	}

	// Pre-create the collision state: an admin already made the "engineering"
	// folder, and a user already owns a skill with the first seed's slug.
	if _, err := st.CreateSkillFolder(ctx, tn, "engineering", "Engineering"); err != nil {
		t.Fatalf("pre-create folder: %v", err)
	}
	userSkillSlug := seeds[0].Slug
	userSkill, err := st.CreateSkill(ctx, tn, userSkillSlug, "My own", nil)
	if err != nil {
		t.Fatalf("pre-create user skill: %v", err)
	}

	// Stage seed blobs, then run the split stage/publish exactly as the handler does.
	staged := make([]store.SkillSeed, 0, len(seeds))
	for _, s := range seeds {
		blobs := make([]store.BlobSize, 0, len(s.Files))
		for _, f := range s.Files {
			if err := obj.PutBlob(ctx, org, f.SHA256, bytes.NewReader(f.Content), f.Size, f.ContentType); err != nil {
				t.Fatalf("stage blob: %v", err)
			}
			blobs = append(blobs, store.BlobSize{SHA: f.SHA256, Size: f.Size})
		}
		staged = append(staged, store.SkillSeed{
			Slug: s.Slug, Title: s.Title, Description: s.Description, FolderSlug: s.FolderSlug,
			ContentHash: s.Digest, SizeBytes: s.TotalSize, Blobs: blobs,
		})
	}

	created, ok, err := st.SeedOrgSkillsStage(ctx, tn, staged)
	if err != nil {
		t.Fatalf("stage seeding (must not abort on collision): %v", err)
	}
	if !ok {
		t.Fatal("expected staged=true on a fresh org")
	}
	// The user's slug must be skipped (never clobbered), so created excludes it.
	for _, c := range created {
		if c.Slug == userSkillSlug {
			t.Fatalf("seeding clobbered the user's skill slug %q", userSkillSlug)
		}
	}
	for _, c := range created {
		if err := obj.PutSkillManifest(ctx, org, c.SkillID, c.VersionID, manifestForSlug(seeds, c.Slug)); err != nil {
			t.Fatalf("write seed manifest: %v", err)
		}
	}
	if err := st.SeedOrgSkillsPublish(ctx, tn, created); err != nil {
		t.Fatalf("publish seeding: %v", err)
	}

	// Seeding is marked done, and the user's skill is untouched (still theirs).
	done, err := st.SkillsSeeded(ctx, tn)
	if err != nil || !done {
		t.Fatalf("skills_seeded = %v (err %v), want true", done, err)
	}
	got, err := st.GetSkill(ctx, tn, userSkill.ID)
	if err != nil {
		t.Fatalf("get user skill: %v", err)
	}
	if got.OwnerUserID != user {
		t.Fatalf("user's skill was clobbered: owner = %q", got.OwnerUserID)
	}

	// Re-running staging is idempotent (already seeded → staged=false, no error).
	if _, ok, err := st.SeedOrgSkillsStage(ctx, tn, staged); err != nil || ok {
		t.Fatalf("re-stage: staged=%v err=%v, want false/nil", ok, err)
	}
}

func manifestForSlug(seeds []skillseeds.Seed, slug string) []byte {
	for _, s := range seeds {
		if s.Slug != slug {
			continue
		}
		type target struct {
			SHA256      string `json:"sha256"`
			ContentType string `json:"content_type,omitempty"`
			Size        int64  `json:"size"`
		}
		files := map[string]target{}
		for _, f := range s.Files {
			files[f.Path] = target{SHA256: f.SHA256, ContentType: f.ContentType, Size: f.Size}
		}
		body := struct {
			SchemaVersion int               `json:"schema_version"`
			Files         map[string]target `json:"files"`
		}{SchemaVersion: 1, Files: files}
		b, _ := json.Marshal(body)
		return b
	}
	return []byte(`{"schema_version":1,"files":{}}`)
}
