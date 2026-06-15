package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/shipped/internal/auth"
	"github.com/danielpang/shipped/internal/customdomains"
	"github.com/danielpang/shipped/internal/edgetoken"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/pwhash"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/services/api/internal/store"
)

func testSigner(t *testing.T) *edgetoken.Signer {
	t.Helper()
	s, _, _, err := edgetoken.LoadOrGenerateSigner("")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// mountAccess builds a chi router for the Phase-2 routes so chi.URLParam works and
// the real Auth middleware injects the claims (or, for nil claims / the JWT-free
// password + JWKS routes, no Auth). Mirrors services/api/internal/router without
// importing it (avoids the router→handlers cycle).
func mountAccess(a *API, c *auth.Claims) http.Handler {
	r := chi.NewRouter()
	// JWT-free routes.
	r.Post("/v1/authz/password", a.AuthzPassword)
	r.Get("/.well-known/edge-jwks", a.EdgeJWKS)
	// Authenticated routes.
	v := fakeVerifier{claims: c}
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Post("/v1/authz/mint", a.AuthzMint)
		r.Get("/v1/members", a.ListMembers)
		r.Put("/v1/orgs/allow-external-sharing", a.SetAllowExternalSharing)
		r.Put("/v1/sites/{id}/access", a.SetSiteAccess)
		r.Post("/v1/sites/{id}/allowlist", a.AddAllowlistEntry)
		r.Delete("/v1/sites/{id}/allowlist", a.RemoveAllowlistEntry)
		r.Get("/v1/sites/{id}/allowlist", a.ListAllowlist)
		r.Post("/v1/sites/{id}/domains", a.AddDomain)
		r.Get("/v1/sites/{id}/domains", a.ListDomains)
		r.Get("/v1/domains/{domainID}/status", a.GetDomainStatus)
	})
	return r
}

// --- small test helpers (method-specific wrappers around the shared `do`). ---

func postJSON(h http.Handler, path, body string) *httptest.ResponseRecorder {
	return doReq(h, http.MethodPost, path, body)
}

func putJSON(h http.Handler, path, body string) *httptest.ResponseRecorder {
	return doReq(h, http.MethodPut, path, body)
}

func getReq(h http.Handler, path string) *httptest.ResponseRecorder {
	return doReq(h, http.MethodGet, path, "")
}

func doReq(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, stringReader(body))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := pwhash.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func newFakeDomains() customdomains.Provider { return customdomains.NewFake() }

func TestEdgeJWKS_ServesKeys(t *testing.T) {
	a := New(quota.Unlimited{})
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, nil)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/.well-known/edge-jwks", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var set edgetoken.JWKS
	if err := json.Unmarshal(rr.Body.Bytes(), &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 1 || set.Keys[0].Alg != "EdDSA" || set.Keys[0].Kty != "OKP" {
		t.Fatalf("jwks = %+v", set)
	}
}

func TestEdgeJWKS_NoSigner_503(t *testing.T) {
	a := New(quota.Unlimited{})
	h := mountAccess(a, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/.well-known/edge-jwks", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

// --- authz mint: org_only allows member / denies non-member ---

func TestAuthzMint_OrgOnly_AllowsMember(t *testing.T) {
	fs := newFakeStore()
	signer := testSigner(t)
	fs.p2().mintFn = func(v store.MintViewer, host string) (store.MintDecision, error) {
		if v.OrgID != "org_1" {
			return store.MintDecision{}, store.ErrNotOrgMember
		}
		return store.MintDecision{Host: host, SiteID: "site_1", OrgID: "org_1", Mode: edgetoken.ModeOrgOnly, Subject: v.UserID}, nil
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = signer
	h := mountAccess(a, claims("user_1", "org_1", "member"))

	rr := postJSON(h, "/v1/authz/mint", `{"host":"acme.shippedusercontent.com","next":"/"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body mintResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Token == "" || body.Mode != edgetoken.ModeOrgOnly {
		t.Fatalf("mint response = %+v", body)
	}
	// The minted token verifies against the signer for the bound host.
	claims, err := edgetoken.VerifierForSigner(signer).Verify(body.Token, "acme.shippedusercontent.com")
	if err != nil {
		t.Fatalf("verify minted token: %v", err)
	}
	if claims.Subject != "user_1" || claims.SiteID != "site_1" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestAuthzMint_OrgOnly_DeniesNonMember_403(t *testing.T) {
	fs := newFakeStore()
	fs.p2().mintFn = func(v store.MintViewer, host string) (store.MintDecision, error) {
		return store.MintDecision{}, store.ErrNotOrgMember
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, claims("user_2", "org_2", "member"))

	rr := postJSON(h, "/v1/authz/mint", `{"host":"acme.shippedusercontent.com"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestAuthzMint_Expired_403(t *testing.T) {
	fs := newFakeStore()
	fs.p2().mintFn = func(v store.MintViewer, host string) (store.MintDecision, error) {
		return store.MintDecision{}, store.ErrPolicyExpired
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, claims("u", "o", "member"))
	rr := postJSON(h, "/v1/authz/mint", `{"host":"acme.shippedusercontent.com"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (expired)", rr.Code)
	}
}

func TestAuthzMint_NoSigner_503(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil) // no signer
	h := mountAccess(a, claims("u", "o", "member"))
	rr := postJSON(h, "/v1/authz/mint", `{"host":"acme.shippedusercontent.com"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

// --- authz password ---

func TestAuthzPassword_Correct_Mints(t *testing.T) {
	fs := newFakeStore()
	signer := testSigner(t)
	// Store a bcrypt hash of "swordfish" via pwhash by returning it from the fake.
	hash := mustHash(t, "swordfish")
	fs.p2().passwordFn = func(host string) (store.PasswordDecision, string, error) {
		return store.PasswordDecision{Host: host, SiteID: "site_1", OrgID: "org_1", Mode: projection.AccessPassword}, hash, nil
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = signer
	h := mountAccess(a, nil)

	rr := postJSON(h, "/v1/authz/password", `{"host":"acme.shippedusercontent.com","password":"swordfish"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body mintResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	claims, err := edgetoken.VerifierForSigner(signer).Verify(body.Token, "acme.shippedusercontent.com")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Mode != projection.AccessPassword {
		t.Errorf("mode = %q", claims.Mode)
	}
	if claims.Subject == "" || claims.Subject[:5] != "anon:" {
		t.Errorf("password token must have anon subject, got %q", claims.Subject)
	}
}

func TestAuthzPassword_Wrong_401(t *testing.T) {
	fs := newFakeStore()
	hash := mustHash(t, "swordfish")
	fs.p2().passwordFn = func(host string) (store.PasswordDecision, string, error) {
		return store.PasswordDecision{Host: host, SiteID: "site_1", OrgID: "org_1", Mode: projection.AccessPassword}, hash, nil
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, nil)

	rr := postJSON(h, "/v1/authz/password", `{"host":"acme.shippedusercontent.com","password":"nope"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// --- access change: admin-only gating ---

func TestSetSiteAccess_MemberRole_Forbidden_403(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleMember // not admin
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountAccess(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/site_1/access", `{"mode":"org_only"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (member can't change access): %s", rr.Code, rr.Body.String())
	}
}

func TestSetSiteAccess_Admin_OK(t *testing.T) {
	fs := newFakeStore()
	ver := "33333333-3333-3333-3333-333333333333"
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s", CurrentVersionID: &ver}
	fs.p2().members["user_1"] = store.RoleAdmin
	proj := projection.NewLocal()
	a := NewFull(quota.Unlimited{}, fs, nil, proj)
	h := mountAccess(a, claims("user_1", "org_1", "admin"))

	rr := putJSON(h, "/v1/sites/site_1/access", `{"mode":"org_only"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	// The route was rewritten to org_only (org-namespaced canonical host).
	rv, ok := proj.Get("org--s.shippedusercontent.com")
	if !ok || rv.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("route not rewritten: %+v ok=%v", rv, ok)
	}
}

func TestSetSiteAccess_Password_RequiresPassword_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleOwner
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountAccess(a, claims("user_1", "org_1", "owner"))
	rr := putJSON(h, "/v1/sites/site_1/access", `{"mode":"password"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (password required)", rr.Code)
	}
}

// --- allowlist CRUD admin gating ---

func TestAddAllowlist_MemberForbidden(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "member"))
	rr := postJSON(h, "/v1/sites/site_1/allowlist", `{"email":"a@x.com"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestAddAllowlist_Admin_OK_MarksExternal(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleAdmin
	fs.p2().orgPolicy = true // external allowed
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "admin"))
	rr := postJSON(h, "/v1/sites/site_1/allowlist", `{"email":"a@external.com"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body allowlistEntryResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if !body.IsExternal {
		t.Errorf("email with non-verified domain should be external, got %+v", body)
	}
}

// --- allow-external-sharing toggle admin gating + reconcile ---

func TestAllowExternalSharing_MemberForbidden(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "member"))
	rr := putJSON(h, "/v1/orgs/allow-external-sharing", `{"enabled":false}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestAllowExternalSharing_Admin_Reconciles(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleAdmin
	ver := "33333333-3333-3333-3333-333333333333"
	fs.p2().reconcile = store.ReconcileResult{
		Downgraded: []store.DowngradedRoute{{
			Host: "s.shippedusercontent.com",
			Route: projection.RouteValue{
				OrgID: "org_1", SiteID: "site_1", VersionID: ver,
				AccessMode: projection.AccessOrgOnly, SchemaVersion: projection.SchemaVersion,
			},
		}},
	}
	proj := projection.NewLocal()
	_ = proj.PutRoute(nil, "s.shippedusercontent.com", projection.RouteValue{
		OrgID: "org_1", SiteID: "site_1", VersionID: ver, AccessMode: projection.AccessPublic, SchemaVersion: projection.SchemaVersion,
	})
	a := NewFull(quota.Unlimited{}, fs, nil, proj)
	h := mountAccess(a, claims("user_1", "org_1", "admin"))

	rr := putJSON(h, "/v1/orgs/allow-external-sharing", `{"enabled":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	// The downgraded route was rewritten to org_only at the edge.
	rv, _ := proj.Get("s.shippedusercontent.com")
	if rv.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("reconcile did not rewrite route: %+v", rv)
	}
}

// --- admin re-check when the member table is unavailable (FIX 3) ---
//
// Strict by default: with the JWT-role fallback DISABLED, an admin-gated action is
// DENIED when auth.member is unavailable even if the JWT claims admin. With the
// fallback explicitly ENABLED (self-host pre-Better-Auth), the verified JWT role
// claim is trusted: admin → allowed, member → still forbidden.

func TestRequireAdmin_StrictByDefault_DeniesWhenAuthSchemaUnavailable(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().memberErr = store.ErrAuthSchemaUnavailable
	fs.p2().orgPolicy = true
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	// Flag defaults false → strict. JWT says admin, but membership can't be confirmed
	// live → DENY (don't trust the claim).
	if a.AllowJWTRoleFallback {
		t.Fatal("AllowJWTRoleFallback must default to false (strict)")
	}
	h := mountAccess(a, claims("user_1", "org_1", "admin"))
	rr := putJSON(h, "/v1/sites/site_1/access", `{"mode":"public"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (strict: no fallback): %s", rr.Code, rr.Body.String())
	}
}

func TestRequireAdmin_FallsBackToJWT_WhenEnabled(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().memberErr = store.ErrAuthSchemaUnavailable
	fs.p2().orgPolicy = true
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	a.AllowJWTRoleFallback = true // self-host pre-Better-Auth opt-in
	// JWT says admin → allowed even though the member table is unavailable.
	h := mountAccess(a, claims("user_1", "org_1", "admin"))
	rr := putJSON(h, "/v1/sites/site_1/access", `{"mode":"public"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (JWT fallback enabled): %s", rr.Code, rr.Body.String())
	}

	// JWT says member → forbidden even with the fallback enabled.
	h2 := mountAccess(a, claims("user_2", "org_1", "member"))
	rr2 := putJSON(h2, "/v1/sites/site_1/access", `{"mode":"public"}`)
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (member, JWT fallback)", rr2.Code)
	}
}

// --- custom domains ---

func TestAddDomain_Admin_CreatesPending(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleAdmin
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.Domains = newFakeDomains()
	h := mountAccess(a, claims("user_1", "org_1", "admin"))

	rr := postJSON(h, "/v1/sites/site_1/domains", `{"hostname":"docs.acme.com"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body domainResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Hostname != "docs.acme.com" || body.VerifyStatus != store.DomainPending || body.DCVRecord == "" {
		t.Fatalf("domain = %+v", body)
	}
}

func TestAddDomain_MemberForbidden(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.Domains = newFakeDomains()
	h := mountAccess(a, claims("user_1", "org_1", "member"))
	rr := postJSON(h, "/v1/sites/site_1/domains", `{"hostname":"docs.acme.com"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestListMembers_OK(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleOwner
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	h := mountAccess(a, claims("user_1", "org_1", "owner"))
	rr := getReq(h, "/v1/members")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body struct {
		Members []memberResponse `json:"members"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Members) != 1 || body.Members[0].Role != store.RoleOwner {
		t.Fatalf("members = %+v", body.Members)
	}
}
