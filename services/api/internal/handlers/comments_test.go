// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// mountComments wires the comment + feed-meta routes behind the real Auth
// middleware (a fake verifier injects the claims).
func mountComments(a *API, c *auth.Claims) http.Handler {
	r := chi.NewRouter()
	v := fakeVerifier{claims: c}
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Get("/v1/sites/{id}/comments", a.ListComments)
		r.Post("/v1/sites/{id}/comments", a.AddComment)
		r.Put("/v1/sites/{id}/feed-meta", a.SetSiteFeedMeta)
	})
	return r
}

// TestAddComment_MemberCanPostAndTag verifies any member can comment and that
// only mention ids who are real org members are kept (others dropped).
func TestAddComment_MemberCanPostAndTag(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s", OwnerUserID: "user_2"}
	fs.p2().members["user_1"] = store.RoleMember
	fs.p2().members["user_2"] = store.RoleOwner
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountComments(a, claims("user_1", "org_1", "member"))

	// Tags user_2 (a member) and "ghost" (not a member → dropped).
	rr := postJSON(h, "/v1/sites/site_1/comments",
		`{"body":"nice work @teammate","mentioned_user_ids":["user_2","ghost"]}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		AuthorID         string   `json:"author_id"`
		Body             string   `json:"body"`
		MentionedUserIDs []string `json:"mentioned_user_ids"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AuthorID != "user_1" {
		t.Fatalf("author = %q, want user_1 (the caller)", resp.AuthorID)
	}
	if len(resp.MentionedUserIDs) != 1 || resp.MentionedUserIDs[0] != "user_2" {
		t.Fatalf("mentions = %v, want [user_2] (non-member dropped)", resp.MentionedUserIDs)
	}
}

// TestAddComment_EmptyBody400 verifies an empty/whitespace body is rejected.
func TestAddComment_EmptyBody400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountComments(a, claims("user_1", "org_1", "member"))

	rr := postJSON(h, "/v1/sites/site_1/comments", `{"body":"   "}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

// TestAddComment_UnknownSite404 verifies posting to an absent site is a 404.
func TestAddComment_UnknownSite404(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountComments(a, claims("user_1", "org_1", "member"))

	rr := postJSON(h, "/v1/sites/missing/comments", `{"body":"hi"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

// TestListComments_ReturnsThread verifies the thread round-trips, oldest first.
func TestListComments_ReturnsThread(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s"}
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountComments(a, claims("user_1", "org_1", "member"))

	postJSON(h, "/v1/sites/site_1/comments", `{"body":"first"}`)
	postJSON(h, "/v1/sites/site_1/comments", `{"body":"second"}`)

	rr := getReq(h, "/v1/sites/site_1/comments")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Comments) != 2 || resp.Comments[0].Body != "first" || resp.Comments[1].Body != "second" {
		t.Fatalf("thread = %+v, want [first second]", resp.Comments)
	}
}

// TestSetFeedMeta_OwnerSets verifies the site owner (a plain member) can set the
// feed title + description.
func TestSetFeedMeta_OwnerSets(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s", OwnerUserID: "user_1"}
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountComments(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/site_1/feed-meta",
		`{"title":"My Dashboard","description":"Q3 metrics"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Title != "My Dashboard" || resp.Description != "Q3 metrics" {
		t.Fatalf("meta = %+v, want {My Dashboard, Q3 metrics}", resp)
	}
}

// TestSetFeedMeta_NonOwnerMemberForbidden verifies a non-owner member can't set
// another user's site metadata.
func TestSetFeedMeta_NonOwnerMemberForbidden(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s", OwnerUserID: "user_2"}
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountComments(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/site_1/feed-meta", `{"title":"hijack"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

// TestSetFeedMeta_TitleTooLong400 verifies the title length cap is enforced.
func TestSetFeedMeta_TitleTooLong400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s", OwnerUserID: "user_1"}
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountComments(a, claims("user_1", "org_1", "member"))

	long := strings.Repeat("x", maxFeedTitleLen+1)
	rr := putJSON(h, "/v1/sites/site_1/feed-meta", `{"title":"`+long+`"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
}
