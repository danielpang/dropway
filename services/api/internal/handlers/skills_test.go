package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/skillseeds"
	"github.com/danielpang/dropway/internal/storage"
)

// skillsRouterFor mirrors the production /v1/skills + /v1/skill-folders route
// tree locally (avoiding the router→handlers import cycle), authenticated as
// (orgID, userID).
func skillsRouterFor(a *API, orgID, userID string) http.Handler {
	v := fakeVerifier{claims: claims(userID, orgID, "member")}
	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Route("/skills", func(r chi.Router) {
			r.Post("/", a.CreateSkill)
			r.Get("/", a.ListSkills)
			r.Get("/{id}", a.GetSkill)
			r.Delete("/{id}", a.DeleteSkill)
			r.Post("/{id}/uploads/prepare", a.PrepareSkillUpload)
			r.Post("/{id}/uploads", a.FinalizeSkillUpload)
			r.Put("/{id}/folders", a.SetSkillFolders)
			r.Get("/{id}/files", a.ListSkillFiles)
			r.Get("/{id}/download", a.DownloadSkill)
		})
		r.Route("/skill-folders", func(r chi.Router) {
			r.Get("/", a.ListSkillFolders)
			r.Post("/", a.CreateSkillFolder)
			r.Patch("/{id}", a.RenameSkillFolder)
			r.Delete("/{id}", a.DeleteSkillFolder)
			r.Post("/{id}/items", a.AddSkillFolderItem)
			r.Delete("/{id}/items/{skillID}", a.RemoveSkillFolderItem)
			r.Patch("/{id}/items/{skillID}", a.SetSkillFolderItemPreset)
			r.Get("/{id}/download", a.DownloadSkillFolder)
		})
	})
	return r
}

const testSkillMD = `---
name: pr-review
description: How to review a PR properly.
---
# PR review
Do the review.
`

// TestSkillFlow_CreateUploadDownload drives the full loop: create → prepare →
// stage blobs → finalize (frontmatter fills metadata; finalize publishes) →
// files listing → download round-trip.
func TestSkillFlow_CreateUploadDownload(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	a := NewFull(quota.Unlimited{}, fs, obj, nil)
	h := skillsRouterFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/skills", `{"slug":"pr-review"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create skill: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		Skill skillResponse `json:"skill"`
	}
	mustJSON(t, rr, &created)
	skillID := created.Skill.ID

	md := []byte(testSkillMD)
	extra := []byte("some notes")
	files := []ManifestFile{
		{Path: "SKILL.md", SHA256: sha(md), Size: int64(len(md)), ContentType: "text/markdown"},
		{Path: "notes/extra.txt", SHA256: sha(extra), Size: int64(len(extra)), ContentType: "text/plain"},
	}
	mf, _ := json.Marshal(files)

	rr = do(t, h, http.MethodPost, "/v1/skills/"+skillID+"/uploads/prepare", `{"manifest":`+string(mf)+`}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", rr.Code, rr.Body.String())
	}
	var prep prepareResponse
	mustJSON(t, rr, &prep)
	if len(prep.Missing) != 2 {
		t.Fatalf("missing = %v, want both blobs", prep.Missing)
	}

	must(t, obj.PutBlobBytes(context.Background(), "org_1", sha(md), md))
	must(t, obj.PutBlobBytes(context.Background(), "org_1", sha(extra), extra))

	digest := manifest.Digest([]manifest.File{
		{Path: "SKILL.md", SHA256: sha(md)},
		{Path: "notes/extra.txt", SHA256: sha(extra)},
	})
	rr = do(t, h, http.MethodPost, "/v1/skills/"+skillID+"/uploads",
		`{"manifest":`+string(mf)+`,"digest":"`+digest+`"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("finalize: %d %s", rr.Code, rr.Body.String())
	}
	var fin skillFinalizeResponse
	mustJSON(t, rr, &fin)
	if fin.VersionNo != 1 {
		t.Fatalf("version_no = %d, want 1", fin.VersionNo)
	}

	// Frontmatter description filled in (title comes from name only when the
	// create request set none — it did not, so name applies too).
	rr = do(t, h, http.MethodGet, "/v1/skills/"+skillID, "")
	var got struct {
		Skill skillResponse `json:"skill"`
	}
	mustJSON(t, rr, &got)
	if got.Skill.Title != "pr-review" || got.Skill.Description != "How to review a PR properly." {
		t.Fatalf("frontmatter metadata not applied: %+v", got.Skill)
	}
	if got.Skill.CurrentVersionID == nil {
		t.Fatal("finalize did not publish (current_version_id is nil)")
	}

	rr = do(t, h, http.MethodGet, "/v1/skills/"+skillID+"/download", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("download: %d %s", rr.Code, rr.Body.String())
	}
	var dl skillDownloadPayload
	mustJSON(t, rr, &dl)
	if len(dl.Files) != 2 || dl.Files[0].Path != "SKILL.md" || dl.Files[0].Content != testSkillMD || dl.Files[0].Encoding != "utf8" {
		t.Fatalf("download payload = %+v", dl)
	}
}

// TestSkillPrepare_RequiresSkillMD rejects a manifest without a root SKILL.md
// before any bytes move.
func TestSkillPrepare_RequiresSkillMD(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	h := skillsRouterFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/skills", `{"slug":"no-md"}`)
	var created struct {
		Skill skillResponse `json:"skill"`
	}
	mustJSON(t, rr, &created)

	body := []byte("just a file")
	mf, _ := json.Marshal([]ManifestFile{{Path: "readme.txt", SHA256: sha(body), Size: int64(len(body))}})
	rr = do(t, h, http.MethodPost, "/v1/skills/"+created.Skill.ID+"/uploads/prepare", `{"manifest":`+string(mf)+`}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("prepare without SKILL.md: %d, want 400 (%s)", rr.Code, rr.Body.String())
	}
}

// TestSkillFolderCap_402 exercises the free-tier folder cap: a genuinely-new
// membership past the cap returns the quota 402; re-adding an existing member
// never does.
func TestSkillFolderCap_402(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["admin_1"] = "admin"
	fs.sk().folderCap = 1
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	h := skillsRouterFor(a, "org_1", "admin_1")

	rr := do(t, h, http.MethodPost, "/v1/skill-folders", `{"slug":"engineering"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create folder: %d %s", rr.Code, rr.Body.String())
	}
	var folder struct {
		Folder skillFolderResponse `json:"folder"`
	}
	mustJSON(t, rr, &folder)

	var ids []string
	for _, slug := range []string{"one", "two"} {
		rr = do(t, h, http.MethodPost, "/v1/skills", `{"slug":"`+slug+`"}`)
		var created struct {
			Skill skillResponse `json:"skill"`
		}
		mustJSON(t, rr, &created)
		ids = append(ids, created.Skill.ID)
	}

	rr = do(t, h, http.MethodPost, "/v1/skill-folders/"+folder.Folder.ID+"/items", `{"skill_id":"`+ids[0]+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("first add: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodPost, "/v1/skill-folders/"+folder.Folder.ID+"/items", `{"skill_id":"`+ids[1]+`"}`)
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("over-cap add: %d, want 402 (%s)", rr.Code, rr.Body.String())
	}
	// Re-adding the existing member is not a new slot → never 402.
	rr = do(t, h, http.MethodPost, "/v1/skill-folders/"+folder.Folder.ID+"/items", `{"skill_id":"`+ids[0]+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-add existing member: %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
}

// TestSkillFolderRoleGates: folder creation and preset flags are admin-only; a
// skill's owner may add their own skill (non-preset) to a folder.
func TestSkillFolderRoleGates(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["member_1"] = "member"
	fs.p2().members["admin_1"] = "admin"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	memberH := skillsRouterFor(a, "org_1", "member_1")
	adminH := skillsRouterFor(a, "org_1", "admin_1")

	// Non-admin cannot create folders.
	rr := do(t, memberH, http.MethodPost, "/v1/skill-folders", `{"slug":"engineering"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member folder create: %d, want 403", rr.Code)
	}
	rr = do(t, adminH, http.MethodPost, "/v1/skill-folders", `{"slug":"engineering"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("admin folder create: %d %s", rr.Code, rr.Body.String())
	}
	var folder struct {
		Folder skillFolderResponse `json:"folder"`
	}
	mustJSON(t, rr, &folder)

	// The member uploads a skill and may add it to the folder themselves…
	rr = do(t, memberH, http.MethodPost, "/v1/skills", `{"slug":"mine"}`)
	var created struct {
		Skill skillResponse `json:"skill"`
	}
	mustJSON(t, rr, &created)
	rr = do(t, memberH, http.MethodPost, "/v1/skill-folders/"+folder.Folder.ID+"/items",
		`{"skill_id":"`+created.Skill.ID+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("owner add own skill: %d %s", rr.Code, rr.Body.String())
	}
	// …but cannot flag it as a preset (admin-only).
	rr = do(t, memberH, http.MethodPost, "/v1/skill-folders/"+folder.Folder.ID+"/items",
		`{"skill_id":"`+created.Skill.ID+`","is_preset":true}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member preset flag: %d, want 403", rr.Code)
	}
	rr = do(t, adminH, http.MethodPatch, "/v1/skill-folders/"+folder.Folder.ID+"/items/"+created.Skill.ID,
		`{"is_preset":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin preset flag: %d %s", rr.Code, rr.Body.String())
	}
}

// TestSkillsSeeding_LazyOnFirstTouch: with the real embedded seeds wired, the
// first skills listing materializes the default folders + preset skills and
// writes their manifest objects so the bulk folder download works end to end.
func TestSkillsSeeding_LazyOnFirstTouch(t *testing.T) {
	seeds, err := skillseeds.Load()
	if err != nil {
		t.Fatalf("load seeds: %v", err)
	}
	if len(seeds) != 3 {
		t.Fatalf("embedded seeds = %d, want 3", len(seeds))
	}

	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	a := NewFull(quota.Unlimited{}, fs, obj, nil)
	a.SkillSeeds = seeds
	h := skillsRouterFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodGet, "/v1/skills?presets=true", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var listed struct {
		Skills []skillResponse `json:"skills"`
	}
	mustJSON(t, rr, &listed)
	if len(listed.Skills) != 3 {
		t.Fatalf("seeded presets = %d, want 3 (%s)", len(listed.Skills), rr.Body.String())
	}

	rr = do(t, h, http.MethodGet, "/v1/skill-folders", "")
	var folders struct {
		Folders []skillFolderResponse `json:"folders"`
	}
	mustJSON(t, rr, &folders)
	if len(folders.Folders) != 3 {
		t.Fatalf("seeded folders = %d, want 3", len(folders.Folders))
	}

	// Bulk-download the engineering folder: the seeded preset's files come back.
	var engID string
	for _, f := range folders.Folders {
		if f.Slug == "engineering" {
			engID = f.ID
		}
	}
	rr = do(t, h, http.MethodGet, "/v1/skill-folders/"+engID+"/download", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("folder download: %d %s", rr.Code, rr.Body.String())
	}
	var dl skillFolderDownloadResponse
	mustJSON(t, rr, &dl)
	if len(dl.Skills) != 1 || dl.Skills[0].Slug != "pr-review-checklist" || dl.Skills[0].Truncated {
		t.Fatalf("folder download skills = %+v", dl.Skills)
	}
	foundMD := false
	for _, f := range dl.Skills[0].Files {
		if f.Path == "SKILL.md" && f.Encoding == "utf8" && len(f.Content) > 0 {
			foundMD = true
		}
	}
	if !foundMD {
		t.Fatalf("seeded skill download missing SKILL.md: %+v", dl.Skills[0].Files)
	}

	// Seeding is once-only: a second touch doesn't duplicate.
	rr = do(t, h, http.MethodGet, "/v1/skills?presets=true", "")
	mustJSON(t, rr, &listed)
	if len(listed.Skills) != 3 {
		t.Fatalf("re-list after second touch = %d, want 3", len(listed.Skills))
	}
}
