package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpang/shipped/internal/auth"
	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// authed wraps a handler with the real Auth middleware in front (a fake verifier
// injects the claims), so the handler reads claims the production way.
func authed(handler http.HandlerFunc, c *auth.Claims) http.Handler {
	v := fakeVerifier{claims: c}
	return middleware.Auth(v)(handler)
}

type fakeVerifier struct{ claims *auth.Claims }

func (f fakeVerifier) Verify(context.Context, string) (*auth.Claims, error) {
	return f.claims, nil
}

func claims(user, org, role string) *auth.Claims {
	c := &auth.Claims{OrgID: org, Role: role}
	c.Subject = user
	return c
}

// fakeStore is an in-memory SiteStore for handler unit tests (no live DB). It
// records the tenant it was called with so tests can assert RLS scoping inputs.
type fakeStore struct {
	sites     map[string]store.Site
	versions  map[string]store.SiteVersion
	createErr error

	// orgSlug is the slug OrgSlug returns; defaults to "org" so the canonical host
	// is "org--<slug>.shippedusercontent.com" in tests that don't override it.
	orgSlug    string
	orgSlugErr error

	lastTenant  store.Tenant
	provisioned bool
}

// OrgSlug returns the configured fake org slug (default "org"), mirroring the real
// store's auth.organization read used to build the org-namespaced content host.
func (f *fakeStore) OrgSlug(_ context.Context, t store.Tenant) (string, error) {
	if f.orgSlugErr != nil {
		return "", f.orgSlugErr
	}
	if f.orgSlug != "" {
		return f.orgSlug, nil
	}
	return "org", nil
}

func newFakeStore() *fakeStore {
	return &fakeStore{sites: map[string]store.Site{}, versions: map[string]store.SiteVersion{}}
}

func (f *fakeStore) EnsureOrgProvisioned(_ context.Context, t store.Tenant) error {
	f.lastTenant, f.provisioned = t, true
	return nil
}

func (f *fakeStore) CreateSite(_ context.Context, t store.Tenant, slug, mode string) (store.Site, error) {
	f.lastTenant = t
	if f.createErr != nil {
		return store.Site{}, f.createErr
	}
	if store.IsReservedSlug(slug) {
		return store.Site{}, store.ErrReservedSlug
	}
	// Mirror the real store: an empty access_mode inherits the org default
	// (org_only for a fresh org), never "" — so the publish projection is valid.
	if mode == "" {
		mode = "org_only"
	}
	s := store.Site{ID: "site_" + slug, OrgID: t.OrgID, Slug: slug, OwnerUserID: t.UserID, AccessMode: mode}
	f.sites[s.ID] = s
	return s, nil
}

func (f *fakeStore) ListSites(_ context.Context, t store.Tenant) ([]store.Site, error) {
	f.lastTenant = t
	out := make([]store.Site, 0, len(f.sites))
	for _, s := range f.sites {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeStore) GetSite(_ context.Context, t store.Tenant, id string) (store.Site, error) {
	f.lastTenant = t
	s, ok := f.sites[id]
	if !ok {
		return store.Site{}, store.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) CreateSiteVersion(_ context.Context, t store.Tenant, p store.CreateSiteVersionParams) (store.SiteVersion, error) {
	f.lastTenant = t
	v := store.SiteVersion{
		ID: "ver_" + p.ContentHash[:8], OrgID: t.OrgID, SiteID: p.SiteID,
		VersionNo: int32(len(f.versions) + 1), Status: p.Status, ContentHash: p.ContentHash, SizeBytes: p.SizeBytes,
	}
	f.versions[v.ID] = v
	return v, nil
}

func (f *fakeStore) GetSiteVersion(_ context.Context, t store.Tenant, id string) (store.SiteVersion, error) {
	v, ok := f.versions[id]
	if !ok {
		return store.SiteVersion{}, store.ErrNotFound
	}
	return v, nil
}

func (f *fakeStore) Publish(_ context.Context, t store.Tenant, siteID, versionID string) (store.PublishResult, error) {
	f.lastTenant = t
	s, ok := f.sites[siteID]
	if !ok {
		return store.PublishResult{}, store.ErrNotFound
	}
	v, ok := f.versions[versionID]
	if !ok || v.SiteID != siteID {
		return store.PublishResult{}, store.ErrVersionMismatch
	}
	s.CurrentVersionID = &versionID
	f.sites[siteID] = s
	host := projection.HostForSite("org", s.Slug)
	return store.PublishResult{
		Host:  host,
		Site:  s,
		Route: routeValueFor(t.OrgID, siteID, versionID, s.AccessMode),
	}, nil
}

func TestHealthz(t *testing.T) {
	a := New(quota.Unlimited{})
	rr := httptest.NewRecorder()
	a.Healthz(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestMe_EchoesClaims(t *testing.T) {
	a := New(quota.Unlimited{})
	h := authed(a.Me, claims("user_1", "org_1", "owner"))

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body meResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.UserID != "user_1" || body.OrgID != "org_1" || body.Role != "owner" {
		t.Errorf("me = %+v", body)
	}
}

func TestCreateSite_Unlimited_201(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := authed(a.CreateSite, claims("user_1", "org_1", "member"))

	req := jsonReq(http.MethodPost, "/v1/sites", `{"slug":"my-site"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body.String())
	}
	var body siteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OrgID != "org_1" || body.OwnerID != "user_1" || body.Slug != "my-site" {
		t.Errorf("site = %+v", body)
	}
	if body.LiveURL != "https://org--my-site.shippedusercontent.com" {
		t.Errorf("live_url = %q", body.LiveURL)
	}
	// RLS tenant was derived from the verified claims.
	if fs.lastTenant.OrgID != "org_1" || fs.lastTenant.UserID != "user_1" {
		t.Errorf("tenant = %+v", fs.lastTenant)
	}
}

func TestCreateSite_ReservedSlug_400(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := authed(a.CreateSite, claims("user_1", "org_1", "member"))

	req := jsonReq(http.MethodPost, "/v1/sites", `{"slug":"admin"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for reserved slug", rr.Code)
	}
}

func TestCreateSite_QuotaExceeded_402(t *testing.T) {
	// The hard cap is now enforced INSIDE store.CreateSite (advisory lock + COUNT →
	// quota.Provider.Allow → INSERT). The handler's job is to render the store's
	// *quota.ExceededError as a 402 with the upgrade body — drive that via the fake
	// store returning the error the cloud provider would produce.
	ex := &quota.ExceededError{
		Limit:      quota.ResourceSitePerUser,
		Current:    10,
		Max:        10,
		PlanTier:   "free",
		NextTier:   "business",
		UpgradeURL: "https://app.shipped.app/billing/upgrade?tier=business",
	}
	fs := newFakeStore()
	fs.createErr = ex
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := authed(a.CreateSite, claims("user_1", "org_1", "member"))

	req := jsonReq(http.MethodPost, "/v1/sites", `{"slug":"ok-slug"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rr.Code)
	}
	var body quota.ExceededError
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.NextTier != "business" || body.UpgradeURL == "" {
		t.Errorf("402 body = %+v", body)
	}
}

// The DB-backed path returns 503 when no Store is configured (clean degradation).
func TestCreateSite_NoStore_503(t *testing.T) {
	a := New(quota.Unlimited{})
	h := authed(a.CreateSite, claims("user_1", "org_1", "member"))
	req := jsonReq(http.MethodPost, "/v1/sites", `{"slug":"x"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

// Ensure the package's unauthorized helper maps via httpx (defensive branch).
func TestCreateSite_NoClaims_401(t *testing.T) {
	a := New(quota.Unlimited{})
	req := jsonReq(http.MethodPost, "/v1/sites", `{"slug":"x"}`)
	rr := httptest.NewRecorder()
	a.CreateSite(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	_ = httpx.ErrUnauthorized // keep import meaningful/documented
}

// H8: the members preflight gate renders the store's *quota.ExceededError as 402
// (the upgrade body) when the org is at/over its member cap, and 200 otherwise.
func TestMembersPreflight_AtCap_402(t *testing.T) {
	fs := newFakeStore()
	fs.p2().preflightErr = &quota.ExceededError{
		Limit: quota.ResourceMemberPerOrg, Current: 5, Max: 5,
		PlanTier: "free", NextTier: "business",
		UpgradeURL: "https://app.shipped.app/billing/upgrade?tier=business",
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := authed(a.MembersPreflight, claims("u", "org_1", "admin"))
	req := httptest.NewRequest(http.MethodGet, "/v1/members/preflight", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (at member cap): %s", rr.Code, rr.Body.String())
	}
	var body quota.ExceededError
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.NextTier != "business" {
		t.Errorf("402 body next_tier = %q, want business", body.NextTier)
	}
}

func TestMembersPreflight_UnderCap_200(t *testing.T) {
	fs := newFakeStore() // preflightErr nil → allowed
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := authed(a.MembersPreflight, claims("u", "org_1", "admin"))
	req := httptest.NewRequest(http.MethodGet, "/v1/members/preflight", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (under cap): %s", rr.Code, rr.Body.String())
	}
}

// H10: GET /v1/orgs/policy returns the org's LIVE allow_external_sharing value so
// the dashboard can render the toggle truthfully instead of a hardcoded default.
func TestGetOrgPolicy_ReturnsLiveValue(t *testing.T) {
	fs := newFakeStore()
	fs.p2().orgPolicy = true // org already has external sharing ON
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := authed(a.GetOrgPolicy, claims("u", "org_1", "member"))
	req := httptest.NewRequest(http.MethodGet, "/v1/orgs/policy", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		AllowExternalSharing bool `json:"allow_external_sharing"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.AllowExternalSharing {
		t.Error("allow_external_sharing = false, want true (the live org value)")
	}
}

// jsonReq builds a request with a JSON body and the auth header set.
func jsonReq(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, stringReader(body))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	return req
}
