// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

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
	rec, h := publishHarness(t, middleware.Auth(humanVerifier{claims: claims("user_1", "org_1", "member")}), true)
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

type humanVerifier struct{ claims *auth.Claims }

func (h humanVerifier) Verify(context.Context, string) (*auth.Claims, error) { return h.claims, nil }
func (humanVerifier) CredentialClass(*auth.Claims) string                    { return auth.ClassHumanSession }

type classedVerifier struct {
	claims *auth.Claims
	class  string
}

func (c classedVerifier) Verify(context.Context, string) (*auth.Claims, error) {
	return c.claims, nil
}
func (c classedVerifier) CredentialClass(*auth.Claims) string { return c.class }

func TestGenericJWTCannotPublishControlledSite(t *testing.T) {
	rec, h := publishHarness(t, middleware.Auth(fakeVerifier{claims: claims("user_1", "org_1", "member")}), true)
	req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/publish", strings.NewReader(`{"version_id":"ver"}`))
	req.Header.Set("Authorization", "Bearer valid-but-unclassified")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || rec.published {
		t.Fatalf("status=%d published=%v body=%s", rr.Code, rec.published, rr.Body.String())
	}
}

func TestUnknownClassifierCannotPublishControlledSite(t *testing.T) {
	for _, class := range []string{"", "not-a-class"} {
		rec, h := publishHarness(t, middleware.Auth(classedVerifier{claims: claims("user_1", "org_1", "member"), class: class}), true)
		req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/publish", strings.NewReader(`{"version_id":"ver"}`))
		req.Header.Set("Authorization", "Bearer classified-unknown")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden || rec.published {
			t.Fatalf("class %q status=%d published=%v body=%s", class, rr.Code, rec.published, rr.Body.String())
		}
	}
}

func TestMCPAndKeyCannotDeleteOrRetargetControlledSite(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site"] = store.Site{ID: "site", OrgID: "org_1", Slug: "s", OwnerUserID: "user_1", AccessMode: projection.AccessOrgOnly}
	fs.controlled = map[string]bool{"site": true}
	fs.p2().members["user_1"] = store.RoleAdmin
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	r := chi.NewRouter()
	r.Use(middleware.Auth(mcpVerifier{claims: claims("user_1", "org_1", "admin")}))
	r.Delete("/v1/sites/{id}", a.DeleteSite)
	r.Put("/v1/sites/{id}/access", a.SetSiteAccess)
	r.Patch("/v1/orgs/project-rooms", a.SetProjectRoomSettings)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodDelete, "/v1/sites/site", ""},
		{http.MethodPut, "/v1/sites/site/access", `{"mode":"public"}`},
		{http.MethodPatch, "/v1/orgs/project-rooms", `{"enabled":false}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer mcp-user-jwt")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}
	if _, ok := fs.sites["site"]; !ok || fs.sites["site"].AccessMode != projection.AccessOrgOnly {
		t.Fatal("controlled site was deleted or retargeted")
	}

	c := claims("user_1", "org_1", "admin")
	keyed := chi.NewRouter()
	keyed.Use(middleware.AuthWithKeys(fakeVerifier{claims: c}, keyAuthFunc(func() *middleware.KeyPrincipal {
		return &middleware.KeyPrincipal{Claims: c, KeyID: "key_1", Scopes: []string{"sites:*"}}
	})))
	keyed.Use(EnforceAPIKeyGrant)
	keyed.Delete("/v1/sites/{id}", a.DeleteSite)
	req := httptest.NewRequest(http.MethodDelete, "/v1/sites/site", nil)
	req.Header.Set("Authorization", "Bearer dw_live_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	rr := httptest.NewRecorder()
	keyed.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("key delete status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := fs.sites["site"]; !ok {
		t.Fatal("api key deleted a controlled site")
	}
}

func TestVerifiedMixedAudienceCannotPublishOrApprove(t *testing.T) {
	const issuer = "https://app.dropway.test"
	const apiAud = "https://api.dropway.test"
	const mcpBase = "https://mcp.dropway.test"
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "OKP", "crv": "Ed25519", "kid": "k1",
			"x": base64.RawURLEncoding.EncodeToString(pub),
		}}})
	}))
	t.Cleanup(jwks.Close)
	verifier := auth.NewVerifier(jwks.URL, issuer, apiAud,
		auth.WithHTTPClient(jwks.Client()),
		auth.WithExtraAudiences(auth.MCPResourceAudiences(mcpBase)...))
	mcpAud := auth.MCPResourceAudiences(mcpBase)[2]
	sign := func(aud jwt.ClaimStrings) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, auth.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer: issuer, Audience: aud, Subject: "user_1",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
			},
			OrgID: "org_1", Role: "member",
		})
		tok.Header["kid"] = "k1"
		raw, err := tok.SignedString(priv)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	approve := chi.NewRouter()
	approve.Use(middleware.Auth(verifier))
	approve.Post("/v1/project-rooms/{roomID}/candidates/{candidateID}/approve", NewFull(quota.Unlimited{}, newFakeStore(), nil, nil).ApproveCandidate)

	for _, aud := range []jwt.ClaimStrings{{apiAud, mcpAud}, {mcpAud, apiAud}} {
		_, pub := publishHarness(t, middleware.Auth(verifier), true)
		req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/publish", strings.NewReader(`{"version_id":"ver"}`))
		req.Header.Set("Authorization", "Bearer "+sign(aud))
		rr := httptest.NewRecorder()
		pub.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("publish aud %v status=%d body=%s", aud, rr.Code, rr.Body.String())
		}
		req = httptest.NewRequest(http.MethodPost, "/v1/project-rooms/room/candidates/cand/approve", strings.NewReader(`{"content_hash":"ab","base_version_id":"ver","base_content_hash":"cd"}`))
		req.Header.Set("Authorization", "Bearer "+sign(aud))
		rr = httptest.NewRecorder()
		approve.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("approve aud %v status=%d body=%s", aud, rr.Code, rr.Body.String())
		}
	}
	rec, humanH := publishHarness(t, middleware.Auth(verifier), true)
	req := httptest.NewRequest(http.MethodPost, "/v1/sites/site/publish", strings.NewReader(`{"version_id":"ver"}`))
	req.Header.Set("Authorization", "Bearer "+sign(jwt.ClaimStrings{apiAud}))
	rr := httptest.NewRecorder()
	humanH.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !rec.published {
		t.Fatalf("api-only publish status=%d published=%v body=%s", rr.Code, rec.published, rr.Body.String())
	}
}

type rejectingVerifier struct{}

func (rejectingVerifier) Verify(context.Context, string) (*auth.Claims, error) {
	return nil, errors.New("not a session jwt")
}
