package handlers

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// collabRouterFor mounts the deploy + collab routes authenticated as
// (orgID, userID).
func collabRouterFor(a *API, orgID, userID string) http.Handler {
	v := fakeVerifier{claims: claims(userID, orgID, "member")}
	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Route("/sites", func(r chi.Router) {
			r.Post("/{id}/deployments/prepare", a.PrepareDeployment)
			r.Put("/{id}/collab", a.SetSiteCollab)
		})
		r.Route("/skills", func(r chi.Router) {
			r.Post("/{id}/uploads/prepare", a.PrepareSkillUpload)
			r.Put("/{id}/collab", a.SetSkillCollab)
		})
	})
	return r
}

// TestSiteCollabGate: any member may deploy to any site by default; the
// creator can flip the toggle off to restrict deploys to creator-or-admin.
func TestSiteCollabGate(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["creator_1"] = "member"
	fs.p2().members["teammate_1"] = "member"
	fs.p2().members["admin_1"] = "admin"
	fs.sites["s1"] = store.Site{
		ID: "s1", OrgID: "org_1", Slug: "s", OwnerUserID: "creator_1",
		AllowMemberEdits: true, // DB default
	}
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	creator := collabRouterFor(a, "org_1", "creator_1")
	teammate := collabRouterFor(a, "org_1", "teammate_1")
	admin := collabRouterFor(a, "org_1", "admin_1")

	prepBody := `{"manifest":[{"path":"index.html","sha256":"` + hex64('a') + `","size":3}]}`

	// Default: a teammate may deploy to the creator's site.
	rr := do(t, teammate, http.MethodPost, "/v1/sites/s1/deployments/prepare", prepBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("teammate prepare with collab on = %d: %s", rr.Code, rr.Body.String())
	}

	// Only the creator (or admin) may flip the toggle.
	rr = do(t, teammate, http.MethodPut, "/v1/sites/s1/collab", `{"allow_member_edits":false}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("teammate toggle = %d, want 403", rr.Code)
	}
	rr = do(t, creator, http.MethodPut, "/v1/sites/s1/collab", `{"allow_member_edits":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("creator toggle: %d %s", rr.Code, rr.Body.String())
	}
	var resp siteResponse
	mustJSON(t, rr, &resp)
	if resp.AllowMemberEdits {
		t.Fatal("toggle should be off in the response")
	}

	// Off: the teammate is locked out; creator and admin still deploy.
	rr = do(t, teammate, http.MethodPost, "/v1/sites/s1/deployments/prepare", prepBody)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("teammate prepare with collab off = %d, want 403", rr.Code)
	}
	rr = do(t, creator, http.MethodPost, "/v1/sites/s1/deployments/prepare", prepBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("creator prepare with collab off = %d", rr.Code)
	}
	rr = do(t, admin, http.MethodPost, "/v1/sites/s1/deployments/prepare", prepBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin prepare with collab off = %d", rr.Code)
	}
}

// TestSkillCollabGate: any member may start an upload to any skill by
// default; toggle off restricts to creator-or-admin.
func TestSkillCollabGate(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["creator_1"] = "member"
	fs.p2().members["teammate_1"] = "member"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	creatorAPI := chatsRouterFor(a, "org_1", "creator_1") // for skill create we need skills router
	_ = creatorAPI

	// Create the skill as creator_1 via the skills router.
	skillsCreator := skillsRouterFor(a, "org_1", "creator_1")
	rr := do(t, skillsCreator, http.MethodPost, "/v1/skills", `{"slug":"shared-skill"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create skill: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		Skill skillResponse `json:"skill"`
	}
	mustJSON(t, rr, &created)
	if !created.Skill.AllowMemberEdits {
		t.Fatal("skill collaboration must default ON")
	}
	id := created.Skill.ID

	prepBody := `{"manifest":[{"path":"SKILL.md","sha256":"` + hex64('b') + `","size":10}]}`

	// Default: a teammate may upload a new version.
	teammate := collabRouterFor(a, "org_1", "teammate_1")
	rr = do(t, teammate, http.MethodPost, "/v1/skills/"+id+"/uploads/prepare", prepBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("teammate skill prepare with collab on = %d: %s", rr.Code, rr.Body.String())
	}

	// Creator flips the toggle off → teammate locked out.
	creator := collabRouterFor(a, "org_1", "creator_1")
	rr = do(t, creator, http.MethodPut, "/v1/skills/"+id+"/collab", `{"allow_member_edits":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("creator toggle: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(t, teammate, http.MethodPost, "/v1/skills/"+id+"/uploads/prepare", prepBody)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("teammate skill prepare with collab off = %d, want 403", rr.Code)
	}
	rr = do(t, creator, http.MethodPost, "/v1/skills/"+id+"/uploads/prepare", prepBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("creator skill prepare with collab off = %d", rr.Code)
	}
}
