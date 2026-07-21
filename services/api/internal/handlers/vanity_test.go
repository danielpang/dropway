// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
)

func vanityRouter(a *API, userID, role string) http.Handler {
	v := fakeVerifier{claims: claims(userID, "org_1", role)}
	r := chi.NewRouter()
	r.Route("/v1/sites", func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Post("/{id}/vanity", a.RegisterVanity)
		r.Delete("/{id}/vanity", a.ReleaseVanity)
		r.Get("/{id}", a.GetSite)
	})
	return r
}

const vanitySiteID = "66666666-6666-6666-6666-666666666666"

func vanityFixture() (*fakeStore, *projection.Local) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleOwner
	ver := "77777777-7777-7777-7777-777777777777"
	fs.sites[vanitySiteID] = store.Site{
		ID: vanitySiteID, OrgID: "org_1", Slug: "docs", OwnerUserID: "user_1",
		AccessMode: projection.AccessPublic, CurrentVersionID: &ver,
	}
	return fs, projection.NewLocal()
}

// An admin claims a free label for a live site: 201 with the vanity host and a
// live URL built from it, and the route is projected so the host serves now.
func TestRegisterVanity_ClaimsAndProjects(t *testing.T) {
	fs, proj := vanityFixture()
	a := NewFull(quota.Unlimited{}, fs, nil, proj)

	rr := do(t, vanityRouter(a, "user_1", "owner"), http.MethodPost,
		"/v1/sites/"+vanitySiteID+"/vanity", `{"slug":"readme"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		VanityHost string `json:"vanity_host"`
		LiveURL    string `json:"live_url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	wantHost := "readme." + projection.ContentDomain
	if resp.VanityHost != wantHost {
		t.Errorf("vanity_host = %q, want %q", resp.VanityHost, wantHost)
	}
	if resp.LiveURL != "https://"+wantHost {
		t.Errorf("live_url = %q, want https://%s", resp.LiveURL, wantHost)
	}
	if _, ok := proj.Get(wantHost); !ok {
		t.Error("vanity route not projected for a live site")
	}
}

// A plain member is not allowed to claim (admin/owner only, like custom domains).
func TestRegisterVanity_MemberForbidden(t *testing.T) {
	fs, proj := vanityFixture()
	fs.p2().members["user_2"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, proj)

	rr := do(t, vanityRouter(a, "user_2", "member"), http.MethodPost,
		"/v1/sites/"+vanitySiteID+"/vanity", `{"slug":"readme"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

// A label someone already holds (any org, any site) is a 409, not an overwrite.
func TestRegisterVanity_TakenLabel409(t *testing.T) {
	fs, proj := vanityFixture()
	fs.vanityTaken = true
	a := NewFull(quota.Unlimited{}, fs, nil, proj)

	rr := do(t, vanityRouter(a, "user_1", "owner"), http.MethodPost,
		"/v1/sites/"+vanitySiteID+"/vanity", `{"slug":"readme"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body.String())
	}
}

// Reserved and malformed labels are rejected before touching the registry.
func TestRegisterVanity_ReservedAndInvalid400(t *testing.T) {
	fs, proj := vanityFixture()
	a := NewFull(quota.Unlimited{}, fs, nil, proj)
	r := vanityRouter(a, "user_1", "owner")

	for _, body := range []string{`{"slug":"www"}`, `{"slug":"Bad Slug!"}`, `{"slug":"a--b"}`, `{"slug":""}`} {
		rr := do(t, r, http.MethodPost, "/v1/sites/"+vanitySiteID+"/vanity", body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400: %s", body, rr.Code, rr.Body.String())
		}
	}
}

// One vanity host per site: a second claim conflicts until the first is released.
func TestRegisterVanity_SecondClaim409(t *testing.T) {
	fs, proj := vanityFixture()
	a := NewFull(quota.Unlimited{}, fs, nil, proj)
	r := vanityRouter(a, "user_1", "owner")

	if rr := do(t, r, http.MethodPost, "/v1/sites/"+vanitySiteID+"/vanity", `{"slug":"readme"}`); rr.Code != http.StatusCreated {
		t.Fatalf("first claim: status = %d: %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, r, http.MethodPost, "/v1/sites/"+vanitySiteID+"/vanity", `{"slug":"other"}`); rr.Code != http.StatusConflict {
		t.Fatalf("second claim: status = %d, want 409: %s", rr.Code, rr.Body.String())
	}
}

// Release removes the row and the edge route, and frees the label for re-claim.
func TestReleaseVanity_FreesLabel(t *testing.T) {
	fs, proj := vanityFixture()
	a := NewFull(quota.Unlimited{}, fs, nil, proj)
	r := vanityRouter(a, "user_1", "owner")
	host := "readme." + projection.ContentDomain

	if rr := do(t, r, http.MethodPost, "/v1/sites/"+vanitySiteID+"/vanity", `{"slug":"readme"}`); rr.Code != http.StatusCreated {
		t.Fatalf("claim: status = %d: %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, r, http.MethodDelete, "/v1/sites/"+vanitySiteID+"/vanity", ""); rr.Code != http.StatusNoContent {
		t.Fatalf("release: status = %d, want 204: %s", rr.Code, rr.Body.String())
	}
	if _, ok := proj.Get(host); ok {
		t.Error("vanity route not de-projected on release")
	}
	// Releasing again is a 404 (nothing to release)…
	if rr := do(t, r, http.MethodDelete, "/v1/sites/"+vanitySiteID+"/vanity", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("second release: status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
	// …and the label is claimable again.
	if rr := do(t, r, http.MethodPost, "/v1/sites/"+vanitySiteID+"/vanity", `{"slug":"readme"}`); rr.Code != http.StatusCreated {
		t.Fatalf("re-claim: status = %d: %s", rr.Code, rr.Body.String())
	}
}

// A site holding a vanity host reports it in the site response and its live_url
// is built from it (the canonical host keeps serving; display prefers vanity).
func TestGetSite_LiveURLPrefersVanity(t *testing.T) {
	fs, proj := vanityFixture()
	a := NewFull(quota.Unlimited{}, fs, nil, proj)
	r := vanityRouter(a, "user_1", "owner")

	if rr := do(t, r, http.MethodPost, "/v1/sites/"+vanitySiteID+"/vanity", `{"slug":"readme"}`); rr.Code != http.StatusCreated {
		t.Fatalf("claim: status = %d: %s", rr.Code, rr.Body.String())
	}
	rr := do(t, r, http.MethodGet, "/v1/sites/"+vanitySiteID, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status = %d: %s", rr.Code, rr.Body.String())
	}
	var site struct {
		LiveURL    string `json:"live_url"`
		VanityHost string `json:"vanity_host"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &site); err != nil {
		t.Fatal(err)
	}
	wantHost := "readme." + projection.ContentDomain
	if site.VanityHost != wantHost {
		t.Errorf("vanity_host = %q, want %q", site.VanityHost, wantHost)
	}
	if site.LiveURL != "https://"+wantHost {
		t.Errorf("live_url = %q, want https://%s", site.LiveURL, wantHost)
	}
}
