// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// publishAnalyticsHarness mounts Publish behind the given authenticator with a
// spy analytics emitter. The site's live version before the publish is `current`
// (nil = never published); the store holds versions v1 (no. 1) and v2 (no. 2).
func publishAnalyticsHarness(t *testing.T, authn func(http.Handler) http.Handler, current *string, proj projection.Writer) (*spyEmitter, *fakeStore, http.Handler) {
	t.Helper()
	fs := newFakeStore()
	fs.sites["s1"] = store.Site{ID: "s1", OrgID: "org_1", Slug: "docs", OwnerUserID: "user_1",
		AllowMemberEdits: true, AccessMode: projection.AccessPublic, CurrentVersionID: current}
	fs.versions["v1"] = store.SiteVersion{ID: "v1", SiteID: "s1", OrgID: "org_1", VersionNo: 1}
	fs.versions["v2"] = store.SiteVersion{ID: "v2", SiteID: "s1", OrgID: "org_1", VersionNo: 2}
	fs.p2().members["user_1"] = store.RoleMember
	spy := &spyEmitter{}
	a := NewFull(quota.Unlimited{}, fs, nil, proj)
	a.Analytics = spy
	r := chi.NewRouter()
	r.Use(authn)
	r.Use(EnforceAPIKeyGrant)
	r.Post("/v1/sites/{id}/publish", a.Publish)
	return spy, fs, r
}

func publishAs(h http.Handler, token, versionID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/sites/s1/publish", strings.NewReader(`{"version_id":"`+versionID+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func onlyEvent(t *testing.T, spy *spyEmitter, name string) analytics.Event {
	t.Helper()
	if len(spy.events) != 1 || spy.events[0].Event != name {
		t.Fatalf("events = %+v, want exactly one %s", spy.events, name)
	}
	return spy.events[0]
}

// A successful publish emits site_published, attributed to the acting user and
// rolled up per org, with the kind of publish and the version number.
func TestPublish_EmitsSitePublished(t *testing.T) {
	v1 := "v1"
	v2 := "v2"
	human := middleware.Auth(humanVerifier{claims: claims("user_1", "org_1", "member")})
	for _, tc := range []struct {
		name      string
		current   *string
		versionID string
		kind      string
		versionNo int32
	}{
		{"first publish", nil, "v1", "first_publish", 1},
		{"update to a new version", &v1, "v2", "update", 2},
		{"rollback to an older version", &v2, "v1", "update", 1},
		{"same version again", &v1, "v1", "republish", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy, _, h := publishAnalyticsHarness(t, human, tc.current, projection.NewLocal())
			rr := publishAs(h, "human-session", tc.versionID)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			ev := onlyEvent(t, spy, "site_published")
			if ev.DistinctID != "user_1" || ev.Groups["organization"] != "org_1" {
				t.Errorf("distinct_id=%q groups=%v", ev.DistinctID, ev.Groups)
			}
			want := map[string]any{
				"org_id": "org_1", "site_id": "s1", "slug": "docs", "version_id": tc.versionID,
				"kind": tc.kind, "source": "dashboard", "version_no": tc.versionNo,
			}
			for k, v := range want {
				if ev.Properties[k] != v {
					t.Errorf("%s = %v (%T), want %v (%T)", k, ev.Properties[k], ev.Properties[k], v, v)
				}
			}
			if host, _ := ev.Properties["$host"].(string); host == "" {
				t.Error("$host is empty; it joins site_published to site_visit")
			}
		})
	}
}

// The source property names the caller's credential class.
func TestPublish_SitePublishedSource(t *testing.T) {
	c := claims("user_1", "org_1", "member")
	for _, tc := range []struct {
		name  string
		authn func(http.Handler) http.Handler
		token string
		want  string
	}{
		{"dashboard session", middleware.Auth(humanVerifier{claims: c}), "human-session", "dashboard"},
		{"MCP client", middleware.Auth(mcpVerifier{claims: c}), "mcp-token", "mcp"},
		{"CLI or SDK API key", middleware.AuthWithKeys(fakeVerifier{claims: c}, keyAuthFunc(func() *middleware.KeyPrincipal {
			return &middleware.KeyPrincipal{Claims: c, KeyID: "key_1", Scopes: []string{"sites:*"}}
		})), "dw_live_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "api_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy, _, h := publishAnalyticsHarness(t, tc.authn, nil, projection.NewLocal())
			if rr := publishAs(h, tc.token, "v1"); rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			if got := onlyEvent(t, spy, "site_published").Properties["source"]; got != tc.want {
				t.Errorf("source = %v, want %s", got, tc.want)
			}
		})
	}
}

// A publish whose edge route write fails did not go live, so it emits nothing:
// the event counts publishes visitors can actually see.
func TestPublish_NoSitePublishedWhenRouteWriteFails(t *testing.T) {
	human := middleware.Auth(humanVerifier{claims: claims("user_1", "org_1", "member")})
	spy, _, h := publishAnalyticsHarness(t, human, nil, quotaProjection{})
	if rr := publishAs(h, "human-session", "v1"); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body %s", rr.Code, rr.Body.String())
	}
	if len(spy.events) != 0 {
		t.Fatalf("events = %+v, want none", spy.events)
	}
}

func deleteAs(h http.Handler, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/v1/sites/"+id, nil)
	req.Header.Set("Authorization", "Bearer test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// Deleting a site emits site_deleted, so live sites per org = created - deleted.
func TestDeleteSite_EmitsSiteDeleted(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = store.RoleMember
	const id = "11111111-1111-1111-1111-111111111111"
	fs.sites[id] = store.Site{ID: id, OrgID: "org_1", Slug: "doomed", OwnerUserID: "user_1", AccessMode: projection.AccessPublic}
	spy := &spyEmitter{}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	a.Analytics = spy

	rr := deleteAs(deleteRouter(a, "user_1", "member"), id)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	ev := onlyEvent(t, spy, "site_deleted")
	if ev.Properties["org_id"] != "org_1" || ev.Properties["site_id"] != id || ev.Properties["slug"] != "doomed" {
		t.Errorf("properties = %v", ev.Properties)
	}
	if ev.Groups["organization"] != "org_1" {
		t.Errorf("groups = %v", ev.Groups)
	}
}

// A rejected delete emits nothing.
func TestDeleteSite_NoSiteDeletedOnFailure(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_2"] = store.RoleMember
	const id = "22222222-2222-2222-2222-222222222222"
	fs.sites[id] = store.Site{ID: id, OrgID: "org_1", Slug: "kept", OwnerUserID: "someone_else", AccessMode: projection.AccessPublic}
	spy := &spyEmitter{}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	a.Analytics = spy

	rr := deleteAs(deleteRouter(a, "user_2", "member"), id)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (a member can't delete a site they don't own), body %s", rr.Code, rr.Body.String())
	}
	if len(spy.events) != 0 {
		t.Fatalf("events = %+v, want none", spy.events)
	}
}
