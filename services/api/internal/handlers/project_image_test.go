package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

type imageResourceStore struct {
	*fakeStore
	created bool
}

func (s *imageResourceStore) CreateProjectImage(ctx context.Context, tenant store.Tenant, slug, title string) (store.Site, error) {
	s.created = true
	return store.Site{ID: "image", OrgID: tenant.OrgID, Slug: slug, Title: title, ResourceKind: "image", AccessMode: "org_only"}, nil
}

func TestProjectImageCreationRequiresHumanAndRollout(t *testing.T) {
	for _, tc := range []struct {
		name, class string
		rollout     bool
		status      int
	}{
		{"human enabled", auth.ClassHumanSession, true, 201},
		{"flag off", auth.ClassHumanSession, false, 403},
		{"MCP denied", auth.ClassMCPOAuth, true, 403},
		{"unclassified denied", "", true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &imageResourceStore{fakeStore: newFakeStore()}
			a := NewFull(quota.Unlimited{}, fs, nil, nil)
			a.CollaborationAllowed = func(context.Context, string, string) bool { return tc.rollout }
			handler := middleware.Auth(classedVerifier{claims: claims("user_1", "org_1", "member"), class: tc.class})(http.HandlerFunc(a.CreateSite))
			request := jsonReq(http.MethodPost, "/v1/sites", `{"slug":"image-upload","resource_kind":"image","title":"Design.png","access_mode":"public"}`)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if fs.created != (tc.status == 201) {
				t.Fatalf("created=%v", fs.created)
			}
		})
	}
}
