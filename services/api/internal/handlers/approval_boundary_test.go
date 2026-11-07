// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

type recordingPublisher struct {
	*fakeStore
	published bool
}

func (r *recordingPublisher) Publish(ctx context.Context, t store.Tenant, siteID, versionID string) (store.PublishResult, error) {
	r.published = true
	return r.fakeStore.Publish(ctx, t, siteID, versionID)
}

func publishHarness(t *testing.T, authn func(http.Handler) http.Handler, controlled bool) (*recordingPublisher, http.Handler) {
	t.Helper()
	fs := newFakeStore()
	fs.sites["site"] = store.Site{ID: "site", OrgID: "org_1", Slug: "s", OwnerUserID: "user_1", AllowMemberEdits: true, AccessMode: projection.AccessOrgOnly}
	fs.versions["ver"] = store.SiteVersion{ID: "ver", SiteID: "site", OrgID: "org_1"}
	fs.p2().members["user_1"] = store.RoleMember
	if controlled {
		fs.controlled = map[string]bool{"site": true}
	}
	rec := &recordingPublisher{fakeStore: fs}
	a := NewFull(quota.Unlimited{}, rec, nil, projection.NewLocal())
	r := chi.NewRouter()
	r.Use(authn)
	r.Post("/v1/sites/{id}/publish", a.Publish)
	r.Post("/v1/sites/{id}/deployments", a.FinalizeDeployment)
	r.Post("/v1/sites/{id}/deployments/prepare", a.PrepareDeployment)
	return rec, r
}

func TestOrgAPIKeyCannotPublishControlledSiteWithoutAgentHeader(t *testing.T) {
	c := claims("user_1", "org_1", "member")
	_, h := publishHarness(t, middleware.AuthWithKeys(fakeVerifier{claims: c}, keyAuthFunc(func() *middleware.KeyPrincipal {
		return &middleware.KeyPrincipal{Claims: c, KeyID: "key_1", Scopes: []string{"sites:*"}}
	})), true)
	req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/publish", strings.NewReader(`{"version_id":"ver"}`))
	req.Header.Set("Authorization", "Bearer dw_live_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "sk-") {
		t.Fatal("response leaked key material")
	}
}

func TestOrgAPIKeyCanPublishSiteOutsideReview(t *testing.T) {
	c := claims("user_1", "org_1", "member")
	rec, h := publishHarness(t, middleware.AuthWithKeys(fakeVerifier{claims: c}, keyAuthFunc(func() *middleware.KeyPrincipal {
		return &middleware.KeyPrincipal{Claims: c, KeyID: "key_1", Scopes: []string{"sites:*"}}
	})), false)
	req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/publish", strings.NewReader(`{"version_id":"ver"}`))
	req.Header.Set("Authorization", "Bearer dw_live_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !rec.published {
		t.Fatal("uncontrolled site publish did not reach the store")
	}
}

func TestMCPOAuthCannotFinalizeControlledSite(t *testing.T) {
	v := mcpVerifier{claims: claims("user_1", "org_1", "member")}
	_, h := publishHarness(t, middleware.Auth(v), true)
	req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/deployments", strings.NewReader(`{"manifest":[{"path":"index.html","sha256":"abcd","size":1}],"digest":"abcd"}`))
	req.Header.Set("Authorization", "Bearer mcp-user-jwt")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestHumanSessionCanPassControlledSitePublish(t *testing.T) {
	rec, h := publishHarness(t, middleware.Auth(fakeVerifier{claims: claims("user_1", "org_1", "member")}), true)
	req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/publish", strings.NewReader(`{"version_id":"ver"}`))
	req.Header.Set("Authorization", "Bearer human-session")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !rec.published {
		t.Fatal("human publish did not reach the store")
	}
}

func TestJobTokenIsNotAnAPICredential(t *testing.T) {
	_, h := publishHarness(t, middleware.Auth(rejectingVerifier{}), false)
	req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/publish", strings.NewReader(`{"version_id":"ver"}`))
	req.Header.Set("Authorization", "Bearer dw_attempt_not-a-session")
	req.Header.Set("X-Dropway-Attempt-Token", "dw_attempt_not-a-session")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body %s", rr.Code, rr.Body.String())
	}
}

type keyAuthFunc func() *middleware.KeyPrincipal

func (f keyAuthFunc) AuthenticateAPIKey(context.Context, string, string) (*middleware.KeyPrincipal, error) {
	return f(), nil
}

type mcpVerifier struct{ claims *auth.Claims }

func (m mcpVerifier) Verify(context.Context, string) (*auth.Claims, error) { return m.claims, nil }
func (mcpVerifier) CredentialClass(*auth.Claims) string                    { return auth.ClassMCPOAuth }

type rejectingVerifier struct{}

func (rejectingVerifier) Verify(context.Context, string) (*auth.Claims, error) {
	return nil, errors.New("not a session jwt")
}
