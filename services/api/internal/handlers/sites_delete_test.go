// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func deleteRouter(a *API, userID, role string) http.Handler {
	v := fakeVerifier{claims: claims(userID, "org_1", role)}
	r := chi.NewRouter()
	r.Route("/v1/sites", func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Delete("/{id}", a.DeleteSite)
	})
	return r
}

// A member who OWNS the site can delete it (the requireSiteOwnerOrAdmin owner
// path drops to requireOrgMember) — this is the same path an API key acting as
// its creator takes, so a key can clean up the sites it made. Delete removes the
// row and de-projects the edge route.
func TestDeleteSite_OwnerMember(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleMember
	const id = "11111111-1111-1111-1111-111111111111"
	fs.sites[id] = store.Site{ID: id, OrgID: "org_1", Slug: "doomed", OwnerUserID: "user_1", AccessMode: projection.AccessPublic}

	proj := projection.NewLocal()
	host := projection.HostForSite("org", "doomed")
	_ = proj.PutRoute(nil, host, projection.RouteValue{
		OrgID: "org_1", SiteID: id, AccessMode: projection.AccessPublic, SchemaVersion: projection.SchemaVersion,
	})
	a := NewFull(quota.Unlimited{}, fs, nil, proj)

	rr := do(t, deleteRouter(a, "user_1", "member"), http.MethodDelete, "/v1/sites/"+id, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rr.Code, rr.Body.String())
	}
	if _, ok := fs.sites[id]; ok {
		t.Fatal("site not removed from store")
	}
	if _, ok := proj.Get(host); ok {
		t.Fatal("edge route not de-projected")
	}
}

// A member who does NOT own the site needs org admin (requireSiteOwnerOrAdmin
// falls through to requireAdmin) — so a plain member gets 403 and the site
// survives.
func TestDeleteSite_NonOwnerMemberForbidden(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleMember
	const id = "22222222-2222-2222-2222-222222222222"
	fs.sites[id] = store.Site{ID: id, OrgID: "org_1", Slug: "notmine", OwnerUserID: "user_2", AccessMode: projection.AccessPublic}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())

	rr := do(t, deleteRouter(a, "user_1", "member"), http.MethodDelete, "/v1/sites/"+id, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if _, ok := fs.sites[id]; !ok {
		t.Fatal("site was deleted despite 403")
	}
}

// The collaboration toggle governs CONTENT edits only: destructive deletes stay
// creator-or-admin regardless (collab.go's contract note, migration 0014). This
// pins the case the other tests miss — allow_member_edits is true by DEFAULT in
// the DB, while a zero-valued store.Site leaves it false, so a gate that used
// requireSiteEditor here would pass every other test and still let any org
// member delete any site in production.
func TestDeleteSite_NonOwnerMemberForbiddenWhenMemberEditsAllowed(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleMember
	const id = "55555555-5555-5555-5555-555555555555"
	fs.sites[id] = store.Site{
		ID: id, OrgID: "org_1", Slug: "collab", OwnerUserID: "user_2",
		AllowMemberEdits: true, AccessMode: projection.AccessPublic,
	}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())

	rr := do(t, deleteRouter(a, "user_1", "member"), http.MethodDelete, "/v1/sites/"+id, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if _, ok := fs.sites[id]; !ok {
		t.Fatal("site was deleted despite 403")
	}
}

// An org admin can delete a site they don't own.
func TestDeleteSite_AdminNonOwner(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleAdmin
	const id = "33333333-3333-3333-3333-333333333333"
	fs.sites[id] = store.Site{ID: id, OrgID: "org_1", Slug: "theirs", OwnerUserID: "user_2", AccessMode: projection.AccessPublic}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())

	rr := do(t, deleteRouter(a, "user_1", "admin"), http.MethodDelete, "/v1/sites/"+id, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteSite_NotFound(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())

	rr := do(t, deleteRouter(a, "user_1", "member"), http.MethodDelete,
		"/v1/sites/44444444-4444-4444-4444-444444444444", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}
