// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/shipped/internal/auth"
	"github.com/danielpang/shipped/internal/edgetoken"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// This file exercises the error/edge BRANCHES of the deploy-flow, authz, and
// revoke handlers — the dependency-guard 503s, the malformed-input 400s, and the
// store-error mappings — that the happy-path flow tests don't reach. They assert
// real fail-closed behavior (a missing dependency must surface, not silently
// no-op), not just touch lines.

// mountDeploy wires the deploy-flow routes for branch tests (prepare/finalize/
// publish) so chi.URLParam resolves. Mirrors the router without importing it.
func mountDeploy(a *API, c *auth.Claims) http.Handler {
	r := chi.NewRouter()
	v := fakeVerifier{claims: c}
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Post("/v1/sites/{id}/deployments/prepare", a.PrepareDeployment)
		r.Post("/v1/sites/{id}/deployments", a.FinalizeDeployment)
		r.Post("/v1/sites/{id}/publish", a.Publish)
	})
	return r
}

// ---------------------------------------------------------------------------
// Dependency-guard 503s: each deploy step needs its backing dependency, and a
// missing one must surface a 503 (fail closed) rather than panic or no-op.
// ---------------------------------------------------------------------------

func TestPrepareDeployment_NoObjects_503(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	a := NewFull(quota.Unlimited{}, fs, nil, nil) // no Objects
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	rr := postJSON(h, "/v1/sites/s1/deployments/prepare", `{"manifest":[]}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no object storage)", rr.Code)
	}
}

func TestPublish_NoProjection_503(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil) // no Projection
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	rr := postJSON(h, "/v1/sites/s1/publish", `{"version_id":"v1"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no projection writer)", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// PrepareDeployment input validation.
// ---------------------------------------------------------------------------

func TestPrepareDeployment_SiteNotFound_404(t *testing.T) {
	fs := newFakeStore() // empty
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	rr := postJSON(h, "/v1/sites/ghost/deployments/prepare", `{"manifest":[{"path":"a","sha256":"`+hex64('a')+`","size":1}]}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (site not owned)", rr.Code)
	}
}

func TestPrepareDeployment_EmptyManifest_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	rr := postJSON(h, "/v1/sites/s1/deployments/prepare", `{"manifest":[]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (empty manifest)", rr.Code)
	}
}

func TestPrepareDeployment_BadSHA_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	// A too-short / non-hex sha256.
	rr := postJSON(h, "/v1/sites/s1/deployments/prepare", `{"manifest":[{"path":"a","sha256":"deadbeef","size":1}]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (bad sha256)", rr.Code)
	}
}

func TestPrepareDeployment_DedupsRepeatedSHA(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	obj := storage.NewFake()
	a := NewFull(quota.Unlimited{}, fs, obj, projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))

	// Two manifest entries reference the SAME blob (a shared asset). Prepare must
	// list it missing exactly once (upload once, even though two paths back it).
	sha := hex64('a')
	body := `{"manifest":[` +
		`{"path":"index.html","sha256":"` + sha + `","size":3},` +
		`{"path":"copy.html","sha256":"` + sha + `","size":3}]}`
	rr := postJSON(h, "/v1/sites/s1/deployments/prepare", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var prep prepareResponse
	mustJSON(t, rr, &prep)
	if len(prep.Missing) != 1 || len(prep.Uploads) != 1 {
		t.Fatalf("a blob backing two paths must be listed once: %+v", prep)
	}
}

// ---------------------------------------------------------------------------
// FinalizeDeployment input validation.
// ---------------------------------------------------------------------------

func TestFinalizeDeployment_MissingDigest_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	// Manifest present but no digest.
	rr := postJSON(h, "/v1/sites/s1/deployments",
		`{"manifest":[{"path":"a","sha256":"`+hex64('a')+`","size":1}]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (digest required)", rr.Code)
	}
}

func TestFinalizeDeployment_BadDigest_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	rr := postJSON(h, "/v1/sites/s1/deployments",
		`{"digest":"not-a-valid-sha","manifest":[{"path":"a","sha256":"`+hex64('a')+`","size":1}]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (bad digest)", rr.Code)
	}
}

func TestFinalizeDeployment_SiteNotFound_404(t *testing.T) {
	fs := newFakeStore() // empty
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	rr := postJSON(h, "/v1/sites/ghost/deployments",
		`{"digest":"`+hex64('b')+`","manifest":[{"path":"a","sha256":"`+hex64('a')+`","size":1}]}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Publish input validation.
// ---------------------------------------------------------------------------

func TestPublish_MissingVersionID_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	rr := postJSON(h, "/v1/sites/s1/publish", `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (version_id required)", rr.Code)
	}
}

func TestPublish_VersionMismatch_400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "s"}
	// A version that belongs to a DIFFERENT site → ErrVersionMismatch → 400.
	fs.versions["v_other"] = store.SiteVersion{ID: "v_other", SiteID: "other", OrgID: "org_1"}
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := mountDeploy(a, claims("u", "org_1", "owner"))
	rr := postJSON(h, "/v1/sites/s1/publish", `{"version_id":"v_other"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (version mismatch)", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// AuthzMint branches: missing host, password-mode redirect, bad JSON.
// ---------------------------------------------------------------------------

func TestAuthzMint_MissingHost_400(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, claims("u", "o", "member"))
	rr := postJSON(h, "/v1/authz/mint", `{"host":"   "}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (host required)", rr.Code)
	}
}

func TestAuthzMint_PasswordModeRedirect_400(t *testing.T) {
	fs := newFakeStore()
	fs.p2().mintFn = func(_ store.MintViewer, _ string) (store.MintDecision, error) {
		return store.MintDecision{}, store.ErrPasswordModeUsesPasswordEndpoint
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, claims("u", "o", "member"))
	rr := postJSON(h, "/v1/authz/mint", `{"host":"acme.shippedusercontent.com"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (password sites use /authz/password)", rr.Code)
	}
}

func TestAuthzMint_HostNotFound_404(t *testing.T) {
	fs := newFakeStore() // default mintFn returns ErrHostNotFound
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, claims("u", "o", "member"))
	rr := postJSON(h, "/v1/authz/mint", `{"host":"ghost.shippedusercontent.com"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (host not found)", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// AuthzPassword branches: no store/signer 503s, missing fields 400, expired 403,
// existence-oracle protection (unknown host → generic 401).
// ---------------------------------------------------------------------------

func TestAuthzPassword_NoSigner_503(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil) // no signer
	h := mountAccess(a, nil)
	rr := postJSON(h, "/v1/authz/password", `{"host":"x.shippedusercontent.com","password":"p"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no signer)", rr.Code)
	}
}

func TestAuthzPassword_MissingFields_400(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, nil)
	rr := postJSON(h, "/v1/authz/password", `{"host":"x.shippedusercontent.com"}`) // no password
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (host+password required)", rr.Code)
	}
}

func TestAuthzPassword_Expired_403(t *testing.T) {
	fs := newFakeStore()
	fs.p2().passwordFn = func(_ string) (store.PasswordDecision, string, error) {
		return store.PasswordDecision{}, "", store.ErrPolicyExpired
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, nil)
	rr := postJSON(h, "/v1/authz/password", `{"host":"x.shippedusercontent.com","password":"p"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (expired link)", rr.Code)
	}
}

func TestAuthzPassword_UnknownHost_Generic401(t *testing.T) {
	fs := newFakeStore() // default passwordFn returns ErrHostNotFound
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	h := mountAccess(a, nil)
	// An unknown host must NOT be distinguishable from a wrong password — both 401,
	// so the password gate isn't an existence oracle.
	rr := postJSON(h, "/v1/authz/password", `{"host":"ghost.shippedusercontent.com","password":"p"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no existence oracle)", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Revoke handlers: the no-revoker 503 + bad-id 400 branches.
// ---------------------------------------------------------------------------

func TestRevokeMember_NoRevoker_503(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["u-admin"] = store.RoleAdmin
	a := New(quota.Unlimited{})
	a.Store = fs // no Revoker
	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/members/victim/revoke", `{}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no revoker configured)", rr.Code)
	}
}

func TestRevokeMember_BadID_400(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["u-admin"] = store.RoleAdmin
	a := New(quota.Unlimited{})
	a.Store = fs
	a.Revoker = newFakeRevoker()
	h := mountPhase4(a, adminClaims())
	// A userId with a space is malformed (looksLikeID rejects). The chi route param
	// can't carry a slash, so a space is the testable malformed case.
	rr := postJSON(h, "/v1/members/bad%20id/revoke", `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (malformed userId)", rr.Code)
	}
}

func TestRevokeSiteAccess_NotOwned_404(t *testing.T) {
	fs := newFakeStore() // no sites
	fs.p2().members["u-admin"] = store.RoleAdmin
	a := New(quota.Unlimited{})
	a.Store = fs
	a.Revoker = newFakeRevoker()
	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/sites/ghost/revoke-access", `{}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (site not owned)", rr.Code)
	}
}

func TestRevokeAccess_RevokerError_500(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["u-admin"] = store.RoleAdmin
	rev := newFakeRevoker()
	rev.err = errAnyRevoke
	a := New(quota.Unlimited{})
	a.Store = fs
	a.Revoker = rev
	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/orgs/revoke-access", `{"kind":"user","id":"victim"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (revoker write failed surfaces)", rr.Code)
	}
}

// errAnyRevoke is a generic error the fake revoker returns to drive the 500 path.
var errAnyRevoke = errRevoke("denylist write failed")

type errRevoke string

func (e errRevoke) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// hex64 builds a 64-char hex sha256 of a single repeated nibble (a valid-shaped
// sha for the input-validation tests, which never read the actual bytes).
// ---------------------------------------------------------------------------

func hex64(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

// Keep the edgetoken import meaningful (the signer the authz tests mint with).
var _ = edgetoken.ModeOrgOnly

// Ensure the deploy-flow router param plumbing is referenced.
var _ = context.Background
