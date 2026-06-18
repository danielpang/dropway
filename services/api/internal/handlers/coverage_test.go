// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/customdomains"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// mountAccessWithSites is mountAccess plus the GET /v1/sites/{id} route so the
// GetSite tests resolve chi.URLParam("id") the production way.
func mountAccessWithSites(a *API, c *auth.Claims) http.Handler {
	r := chi.NewRouter()
	v := fakeVerifier{claims: c}
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Get("/v1/sites/{id}", a.GetSite)
		r.Get("/v1/sites/{id}/versions", a.ListVersions)
	})
	return r
}

// This file adds handler unit tests for the previously-untested read/list handlers
// (ListSites, GetSite, ListAllowlist, RemoveAllowlistEntry, ListDomains,
// GetDomainStatus) and the pure mapping helpers (writeStoreError status mapping,
// mapDomainStatus, domainMatches, looksLikeHostname/looksLikeEmail/looksLikeID/
// normalizeHost, parseIntDefault). It reuses the in-memory fakeStore +
// mountAccess router from the existing Phase-2 test files.

// ---------------------------------------------------------------------------
// ListSites / GetSite (sites.go) — the read path, including the no-store 503,
// the no-claims 401, and the GetSite 404 for an absent/other-tenant site.
// ---------------------------------------------------------------------------

func TestListSites_OK(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "alpha", OwnerUserID: "user_1", AccessMode: "public"}
	fs.sites["s2"] = store.Site{ID: "s2", OrgID: "org_1", Slug: "beta", OwnerUserID: "user_1", AccessMode: "org_only"}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := authed(a.ListSites, claims("user_1", "org_1", "member"))

	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Sites []siteResponse `json:"sites"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sites) != 2 {
		t.Fatalf("want 2 sites, got %d", len(body.Sites))
	}
	// The live_url is the org-namespaced canonical host (fake org slug "org").
	for _, s := range body.Sites {
		if s.LiveURL != "https://"+projection.HostForSite("org", s.Slug) {
			t.Errorf("live_url = %q for slug %q", s.LiveURL, s.Slug)
		}
	}
	// RLS tenant derived from the verified claims.
	if fs.lastTenant.OrgID != "org_1" {
		t.Errorf("tenant = %+v", fs.lastTenant)
	}
}

func TestListSites_NoStore_503(t *testing.T) {
	a := New(quota.Unlimited{})
	h := authed(a.ListSites, claims("user_1", "org_1", "member"))
	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestListSites_NoClaims_401(t *testing.T) {
	a := NewFull(quota.Unlimited{}, newFakeStore(), nil, nil)
	rr := httptest.NewRecorder()
	a.ListSites(rr, httptest.NewRequest(http.MethodGet, "/v1/sites", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no claims)", rr.Code)
	}
}

func TestGetSite_OK(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "alpha", OwnerUserID: "user_1", AccessMode: "public"}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	// Mount via the Phase-2 router so chi.URLParam("id") resolves.
	h := mountAccessWithSites(a, claims("user_1", "org_1", "member"))

	rr := getReq(h, "/v1/sites/s1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body siteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "s1" || body.Slug != "alpha" {
		t.Errorf("site = %+v", body)
	}
}

func TestGetSite_NotFound_404(t *testing.T) {
	fs := newFakeStore() // empty store
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccessWithSites(a, claims("user_1", "org_1", "member"))
	rr := getReq(h, "/v1/sites/ghost")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for absent site", rr.Code)
	}
}

// GetSite surfaces the site's LOGICAL storage (its current version's size); a site
// with no live version reports 0.
func TestGetSite_StorageBytes(t *testing.T) {
	fs := newFakeStore()
	ver := "v1"
	fs.versions[ver] = store.SiteVersion{ID: ver, SiteID: "s1", SizeBytes: 4096}
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "alpha", OwnerUserID: "user_1", AccessMode: "public", CurrentVersionID: &ver}
	fs.sites["s2"] = store.Site{ID: "s2", OrgID: "org_1", Slug: "beta", OwnerUserID: "user_1", AccessMode: "public"} // no live version
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccessWithSites(a, claims("user_1", "org_1", "member"))

	var live siteResponse
	if err := json.Unmarshal(getReq(h, "/v1/sites/s1").Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if live.StorageBytes != 4096 {
		t.Errorf("live site storage_bytes = %d, want 4096", live.StorageBytes)
	}
	var empty siteResponse
	if err := json.Unmarshal(getReq(h, "/v1/sites/s2").Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.StorageBytes != 0 {
		t.Errorf("site with no live version storage_bytes = %d, want 0", empty.StorageBytes)
	}
}

// ListVersions returns the site's deploy history newest-first, with the live
// version flagged is_current.
func TestListVersions_OK(t *testing.T) {
	fs := newFakeStore()
	v2 := "v2"
	fs.versions["v1"] = store.SiteVersion{ID: "v1", SiteID: "s1", VersionNo: 1, Status: "ready", SizeBytes: 100}
	fs.versions["v2"] = store.SiteVersion{ID: "v2", SiteID: "s1", VersionNo: 2, Status: "ready", SizeBytes: 200}
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "alpha", OwnerUserID: "user_1", CurrentVersionID: &v2}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccessWithSites(a, claims("user_1", "org_1", "member"))

	rr := getReq(h, "/v1/sites/s1/versions")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Versions []versionResponse `json:"versions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Versions) != 2 {
		t.Fatalf("want 2 versions, got %d", len(body.Versions))
	}
	// Newest first: v2 (current) then v1.
	if body.Versions[0].VersionNo != 2 || !body.Versions[0].IsCurrent {
		t.Errorf("first row = %+v, want version_no 2 + is_current", body.Versions[0])
	}
	if body.Versions[1].VersionNo != 1 || body.Versions[1].IsCurrent {
		t.Errorf("second row = %+v, want version_no 1 + not current", body.Versions[1])
	}
}

func TestListVersions_SiteNotFound_404(t *testing.T) {
	a := NewFull(quota.Unlimited{}, newFakeStore(), nil, nil)
	h := mountAccessWithSites(a, claims("user_1", "org_1", "member"))
	if rr := getReq(h, "/v1/sites/ghost/versions"); rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for absent site", rr.Code)
	}
}

// StorageUsage sums each user's sites' current-version sizes (logical) and returns a
// per-user breakdown sorted by user id.
func TestStorageUsage_PerUser(t *testing.T) {
	fs := newFakeStore()
	v1, v2, v3 := "v1", "v2", "v3"
	fs.versions[v1] = store.SiteVersion{ID: v1, SiteID: "s1", SizeBytes: 1000}
	fs.versions[v2] = store.SiteVersion{ID: v2, SiteID: "s2", SizeBytes: 2000}
	fs.versions[v3] = store.SiteVersion{ID: v3, SiteID: "s3", SizeBytes: 500}
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", OwnerUserID: "alice", CurrentVersionID: &v1}
	fs.sites["s2"] = store.Site{ID: "s2", OrgID: "org_1", OwnerUserID: "alice", CurrentVersionID: &v2}
	fs.sites["s3"] = store.Site{ID: "s3", OrgID: "org_1", OwnerUserID: "bob", CurrentVersionID: &v3}
	fs.sites["s4"] = store.Site{ID: "s4", OrgID: "org_1", OwnerUserID: "bob"} // no live version → 0
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := authed(a.StorageUsage, claims("alice", "org_1", "member"))

	req := httptest.NewRequest(http.MethodGet, "/v1/storage", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Users []userStorageResponse `json:"users"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// Sorted by user id: alice (1000+2000) then bob (500).
	want := []userStorageResponse{{UserID: "alice", Bytes: 3000}, {UserID: "bob", Bytes: 500}}
	if len(body.Users) != 2 || body.Users[0] != want[0] || body.Users[1] != want[1] {
		t.Errorf("per-user storage = %+v, want %+v", body.Users, want)
	}
}

// ---------------------------------------------------------------------------
// ListAllowlist / RemoveAllowlistEntry (access.go) — the untested allowlist read
// + delete paths.
// ---------------------------------------------------------------------------

func TestListAllowlist_OK(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().allowlist["site_1"] = []store.AllowlistEntry{
		{Email: "a@x.com", IsExternal: false},
		{Email: "b@ext.com", IsExternal: true},
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "member")) // any member may read

	rr := getReq(h, "/v1/sites/site_1/allowlist")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Allowlist []allowlistEntryResponse `json:"allowlist"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Allowlist) != 2 {
		t.Fatalf("want 2 allowlist entries, got %d", len(body.Allowlist))
	}
}

func TestListAllowlist_SiteNotFound_404(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "member"))
	rr := getReq(h, "/v1/sites/ghost/allowlist")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestRemoveAllowlist_Admin_OK(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleAdmin
	fs.p2().allowlist["site_1"] = []store.AllowlistEntry{{Email: "a@x.com"}, {Email: "b@x.com"}}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "admin"))

	rr := doReq(h, http.MethodDelete, "/v1/sites/site_1/allowlist", `{"email":"a@x.com"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	// The grant was removed; only b@x.com remains.
	rest := fs.p2().allowlist["site_1"]
	if len(rest) != 1 || rest[0].Email != "b@x.com" {
		t.Errorf("after remove: %+v", rest)
	}
}

func TestRemoveAllowlist_MemberForbidden_403(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "member"))
	rr := doReq(h, http.MethodDelete, "/v1/sites/site_1/allowlist", `{"email":"a@x.com"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (member can't remove)", rr.Code)
	}
}

func TestRemoveAllowlist_EmptyEmail_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleOwner
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "owner"))
	rr := doReq(h, http.MethodDelete, "/v1/sites/site_1/allowlist", `{"email":"   "}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (email required)", rr.Code)
	}
}

func TestAddAllowlist_InvalidEmail_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleAdmin
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "admin"))
	rr := postJSON(h, "/v1/sites/site_1/allowlist", `{"email":"not-an-email"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (looksLikeEmail rejects)", rr.Code)
	}
}

func TestAddAllowlist_ExternalDisabled_403(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleAdmin
	fs.p2().orgPolicy = false // external sharing disabled
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "admin"))
	// An external-domain email under a false org policy → the store returns
	// ErrExternalSharingDisabled, which maps to 403.
	rr := postJSON(h, "/v1/sites/site_1/allowlist", `{"email":"a@external.com"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (external sharing disabled): %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// ListDomains / GetDomainStatus (domains.go) — the untested domain read + the
// status-poll state machine that registers the route on verified+TLS.
// ---------------------------------------------------------------------------

func TestListDomains_OK(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().domains["dom_1"] = store.Domain{ID: "dom_1", SiteID: "site_1", Hostname: "docs.acme.com", VerifyStatus: store.DomainPending}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "member"))

	rr := getReq(h, "/v1/sites/site_1/domains")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Domains []domainResponse `json:"domains"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Domains) != 1 || body.Domains[0].Hostname != "docs.acme.com" {
		t.Fatalf("domains = %+v", body.Domains)
	}
}

func TestGetDomainStatus_VerifiesAndWritesRoute(t *testing.T) {
	fs := newFakeStore()
	ver := "33333333-3333-3333-3333-333333333333"
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s", AccessMode: "public", CurrentVersionID: &ver}
	// Seed a pending domain with a CF hostname id so the handler polls the provider.
	dom := customdomains.NewFake()
	created, err := dom.CreateCustomHostname(context.Background(), "docs.acme.com")
	if err != nil {
		t.Fatal(err)
	}
	fs.p2().domains["dom_1"] = store.Domain{
		ID: "dom_1", OrgID: "org_1", SiteID: "site_1", Hostname: "docs.acme.com",
		VerifyStatus: store.DomainPending, TLSStatus: store.TLSPending, CFHostnameID: created.ID,
	}
	// Advance the CF hostname to active (verified + cert issued).
	if err := dom.AdvanceTo(created.ID, customdomains.StateActive); err != nil {
		t.Fatal(err)
	}

	proj := projection.NewLocal()
	a := NewFull(quota.Unlimited{}, fs, nil, proj)
	a.Domains = dom
	h := mountAccess(a, claims("user_1", "org_1", "member"))

	rr := getReq(h, "/v1/domains/dom_1/status")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body domainResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.VerifyStatus != store.DomainVerified || body.TLSStatus != store.TLSIssued {
		t.Fatalf("domain not advanced to verified+issued: %+v", body)
	}
	// The custom host's route was projected so it serves at the live version.
	rv, ok := proj.Get("docs.acme.com")
	if !ok || rv.VersionID != ver || rv.AccessMode != "public" {
		t.Fatalf("custom host route not written: %+v ok=%v", rv, ok)
	}
}

func TestGetDomainStatus_NoCFHostname_ReturnsCurrent(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	// A domain with no CF hostname id recorded → nothing to poll, return current.
	fs.p2().domains["dom_1"] = store.Domain{ID: "dom_1", OrgID: "org_1", SiteID: "site_1", Hostname: "x.io", VerifyStatus: store.DomainPending}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.Domains = customdomains.NewFake()
	h := mountAccess(a, claims("user_1", "org_1", "member"))

	rr := getReq(h, "/v1/domains/dom_1/status")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body domainResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.VerifyStatus != store.DomainPending {
		t.Errorf("expected unchanged pending status, got %q", body.VerifyStatus)
	}
}

func TestGetDomainStatus_NotFound_404(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.Domains = customdomains.NewFake()
	h := mountAccess(a, claims("user_1", "org_1", "member"))
	rr := getReq(h, "/v1/domains/ghost/status")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestAddDomain_InvalidHostname_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleAdmin
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.Domains = customdomains.NewFake()
	h := mountAccess(a, claims("user_1", "org_1", "admin"))
	// No dot → not a valid hostname.
	rr := postJSON(h, "/v1/sites/site_1/domains", `{"hostname":"localhost"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid hostname)", rr.Code)
	}
}

func TestAddDomain_NoDomainsProvider_503(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleAdmin
	a := NewFull(quota.Unlimited{}, fs, nil, nil) // no Domains provider
	h := mountAccess(a, claims("user_1", "org_1", "admin"))
	rr := postJSON(h, "/v1/sites/site_1/domains", `{"hostname":"docs.acme.com"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no custom domains provider)", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// ListMembers degradation: identity.member unavailable → empty list, not an error.
// ---------------------------------------------------------------------------

func TestListMembers_AuthSchemaUnavailable_EmptyList(t *testing.T) {
	fs := newFakeStore()
	fs.p2().memberErr = store.ErrAuthSchemaUnavailable
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "owner"))
	rr := getReq(h, "/v1/members")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degrade to empty)", rr.Code)
	}
	var body struct {
		Members []memberResponse `json:"members"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Members) != 0 {
		t.Errorf("want empty members list, got %+v", body.Members)
	}
}

// ---------------------------------------------------------------------------
// writeStoreError — the sentinel → HTTP status mapping (table-driven). This is the
// single point that turns store sentinels into the right status + quota 402.
// ---------------------------------------------------------------------------

func TestWriteStoreError_StatusMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"reserved slug → 400", store.ErrReservedSlug, http.StatusBadRequest},
		{"slug taken → 400", store.ErrSlugTaken, http.StatusBadRequest},
		{"not found → 404", store.ErrNotFound, http.StatusNotFound},
		{"version mismatch → 400", store.ErrVersionMismatch, http.StatusBadRequest},
		{"host taken → 409", store.ErrHostTaken, http.StatusConflict},
		{"external sharing disabled → 403", store.ErrExternalSharingDisabled, http.StatusForbidden},
		{"invalid mode → 400", store.ErrInvalidMode, http.StatusBadRequest},
		{"invalid domain status → 400", store.ErrInvalidDomainStatus, http.StatusBadRequest},
		{"bad email → 400", store.ErrBadEmail, http.StatusBadRequest},
		{"bad hostname → 400", store.ErrBadHostname, http.StatusBadRequest},
		{"no policy → 404", store.ErrNoPolicy, http.StatusNotFound},
		{"policy expired → 403", store.ErrPolicyExpired, http.StatusForbidden},
		{"host not found → 404", store.ErrHostNotFound, http.StatusNotFound},
		{"not org member → 403", store.ErrNotOrgMember, http.StatusForbidden},
		{"not allowlisted → 403", store.ErrNotAllowlisted, http.StatusForbidden},
		{"not gated → 400", store.ErrNotGated, http.StatusBadRequest},
		{"unknown → 500", errors.New("boom"), http.StatusInternalServerError},
		{"wrapped not-found → 404", fmt.Errorf("ctx: %w", store.ErrNotFound), http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeStoreError(rr, c.err)
			if rr.Code != c.want {
				t.Errorf("writeStoreError(%v) → %d, want %d", c.err, rr.Code, c.want)
			}
		})
	}
}

func TestWriteStoreError_QuotaExceeded_402(t *testing.T) {
	ex := &quota.ExceededError{
		Limit: quota.ResourceSitePerOrg, Current: 10, Max: 10,
		PlanTier: "free", NextTier: "business",
		UpgradeURL: "https://app.dropway.dev/billing/upgrade?tier=business",
	}
	rr := httptest.NewRecorder()
	writeStoreError(rr, ex)
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rr.Code)
	}
	var body quota.ExceededError
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.NextTier != "business" {
		t.Errorf("402 body = %+v", body)
	}
}

// ---------------------------------------------------------------------------
// mapDomainStatus — the provider VerifyState → (verify, tls) state-machine pair.
// ---------------------------------------------------------------------------

func TestMapDomainStatus(t *testing.T) {
	cases := []struct {
		name       string
		in         customdomains.StatusResult
		wantVerify string
		wantTLS    string
	}{
		{"active + cert → verified/issued", customdomains.StatusResult{State: customdomains.StateActive, TLSIssued: true}, store.DomainVerified, store.TLSIssued},
		{"active, no cert yet → verified/pending", customdomains.StatusResult{State: customdomains.StateActive, TLSIssued: false}, store.DomainVerified, store.TLSPending},
		{"verifying → verifying/pending", customdomains.StatusResult{State: customdomains.StateVerifying}, store.DomainVerifying, store.TLSPending},
		{"failed → failed/failed", customdomains.StatusResult{State: customdomains.StateFailed}, store.DomainFailed, store.TLSFailed},
		{"pending → pending/pending", customdomains.StatusResult{State: customdomains.StatePending}, store.DomainPending, store.TLSPending},
		{"unknown state → pending/pending", customdomains.StatusResult{State: customdomains.VerifyState("weird")}, store.DomainPending, store.TLSPending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, tls := mapDomainStatus(c.in)
			if v != c.wantVerify || tls != c.wantTLS {
				t.Errorf("mapDomainStatus(%+v) = (%q,%q), want (%q,%q)", c.in, v, tls, c.wantVerify, c.wantTLS)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Pure string helpers: domainMatches, looksLikeHostname, looksLikeEmail,
// looksLikeID, normalizeHost, parseIntDefault.
// ---------------------------------------------------------------------------

func TestDomainMatches(t *testing.T) {
	cases := []struct {
		hostname, emailDomain string
		want                  bool
	}{
		{"acme.com", "acme.com", true},       // exact
		{"docs.acme.com", "acme.com", true},  // email domain is a parent of the host
		{"ACME.com", "acme.COM", true},       // case-insensitive
		{"acme.com", "other.com", false},     // unrelated
		{"notacme.com", "acme.com", false},   // suffix without the dot boundary
		{"acme.com", "docs.acme.com", false}, // child can't match a parent host
	}
	for _, c := range cases {
		if got := domainMatches(c.hostname, c.emailDomain); got != c.want {
			t.Errorf("domainMatches(%q,%q) = %v, want %v", c.hostname, c.emailDomain, got, c.want)
		}
	}
}

func TestLooksLikeHostname(t *testing.T) {
	good := []string{"docs.acme.com", "a.b", "x.io"}
	for _, h := range good {
		if !looksLikeHostname(h) {
			t.Errorf("looksLikeHostname(%q) = false, want true", h)
		}
	}
	bad := []string{"", "localhost", "no dot", "has space.com", "a/b.com", "host:port.com", ".leadingdot"}
	for _, h := range bad {
		if looksLikeHostname(h) {
			t.Errorf("looksLikeHostname(%q) = true, want false", h)
		}
	}
}

func TestLooksLikeEmail(t *testing.T) {
	good := []string{"a@b.com", "user.name@sub.example.org"}
	for _, e := range good {
		if !looksLikeEmail(e) {
			t.Errorf("looksLikeEmail(%q) = false, want true", e)
		}
	}
	bad := []string{"", "no-at-sign", "@nolocal.com", "trailing@", "noTLD@host", "a@b"}
	for _, e := range bad {
		if looksLikeEmail(e) {
			t.Errorf("looksLikeEmail(%q) = true, want false", e)
		}
	}
}

func TestLooksLikeID(t *testing.T) {
	if !looksLikeID("user_123") || !looksLikeID("33333333-3333-3333-3333-333333333333") {
		t.Error("a plausible id should pass")
	}
	bad := []string{"", "has/slash", "has space", "has\ttab", "has\nnewline"}
	for _, s := range bad {
		if looksLikeID(s) {
			t.Errorf("looksLikeID(%q) = true, want false", s)
		}
	}
	// Over the 200-char bound.
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	if looksLikeID(string(long)) {
		t.Error("an over-long id should be rejected")
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"  ACME.DropwayContent.COM ": "acme.dropwaycontent.com",
		"a\tb\nc\rd":                     "abcd", // whitespace runes are stripped
		"already.lower.com":              "already.lower.com",
		"":                               "",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseIntDefault(t *testing.T) {
	cases := []struct {
		in  string
		def int
		out int
	}{
		{"", 50, 50},    // empty → default
		{"10", 50, 10},  // valid
		{"0", 50, 0},    // zero is valid (non-negative)
		{"-5", 50, 50},  // negative → default
		{"abc", 50, 50}, // non-numeric → default
		{"999", 0, 999}, // large valid value
	}
	for _, c := range cases {
		if got := parseIntDefault(c.in, c.def); got != c.out {
			t.Errorf("parseIntDefault(%q,%d) = %d, want %d", c.in, c.def, got, c.out)
		}
	}
}

// ---------------------------------------------------------------------------
// recordAudit best-effort: an audit-write failure must NOT fail the request the
// audit row describes (the action already committed and is authoritative).
// ---------------------------------------------------------------------------

func TestRecordAudit_WriteFailureDoesNotFailRequest(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org-1", Slug: "s"}
	fs.p2().members["u-admin"] = store.RoleAdmin
	fs.p2().orgPolicy = true
	// Make every audit write fail; the create itself must still return 201.
	fs.p2().auditErr = errors.New("audit db down")
	a := New(quota.Unlimited{})
	a.Store = fs

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/sites", `{"slug":"resilient"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create site with failing audit: %d %s, want 201 (audit is best-effort)", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// EnsureOrgProvisioned middleware: with no store it passes through; a provision
// error surfaces; success calls through to the next handler.
// ---------------------------------------------------------------------------

func TestEnsureOrgProvisioned_NoStore_PassesThrough(t *testing.T) {
	a := New(quota.Unlimited{}) // no store
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := authed(func(w http.ResponseWriter, r *http.Request) {
		a.EnsureOrgProvisioned(next).ServeHTTP(w, r)
	}, claims("user_1", "org_1", "member"))

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !called {
		t.Error("with no store, EnsureOrgProvisioned must pass through to next")
	}
}

func TestEnsureOrgProvisioned_Error_Surfaces(t *testing.T) {
	fs := &provisionFailStore{fakeStore: newFakeStore(), err: errors.New("provision failed")}
	a := New(quota.Unlimited{})
	a.Store = fs
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := authed(func(w http.ResponseWriter, r *http.Request) {
		a.EnsureOrgProvisioned(next).ServeHTTP(w, r)
	}, claims("user_1", "org_1", "member"))

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		t.Error("a provision error should surface (not 200)")
	}
}

// provisionFailStore wraps the fakeStore to inject an EnsureOrgProvisioned error.
type provisionFailStore struct {
	*fakeStore
	err error
}

func (s *provisionFailStore) EnsureOrgProvisioned(_ context.Context, _ store.Tenant) error {
	return s.err
}

// keep httpx import meaningful (the 401 path is via httpx sentinels).
var _ = httpx.ErrUnauthorized
