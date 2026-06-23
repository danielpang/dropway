// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// mountFeed wires the feed routes behind the real Auth middleware (a fake verifier
// injects the claims), mirroring services/api/internal/router without importing it.
func mountFeed(a *API, c *auth.Claims) http.Handler {
	r := chi.NewRouter()
	v := fakeVerifier{claims: c}
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Get("/v1/feed", a.ListFeed)
		r.Put("/v1/sites/{id}/feed", a.SetSiteFeedVisibility)
		r.Put("/v1/sites/{id}/vote", a.SetSiteVote)
	})
	return r
}

// feedSlugs decodes a {"sites":[...]} feed body into the slugs it lists, in order.
func feedSlugs(t *testing.T, body []byte) []string {
	t.Helper()
	var out struct {
		Sites []struct {
			Slug        string `json:"slug"`
			OwnerID     string `json:"owner_id"`
			FeedVisible bool   `json:"feed_visible"`
		} `json:"sites"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode feed body: %v (%s)", err, body)
	}
	slugs := make([]string, len(out.Sites))
	for i, s := range out.Sites {
		slugs[i] = s.Slug
	}
	return slugs
}

// TestListFeed_ExcludesPrivate verifies the feed lists feed-visible sites and omits
// any site marked private (feed_visible=false), regardless of who owns it.
func TestListFeed_ExcludesPrivate(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_a"] = store.Site{ID: "site_a", OrgID: "org_1", Slug: "alpha", OwnerUserID: "user_2", FeedVisible: true}
	fs.sites["site_b"] = store.Site{ID: "site_b", OrgID: "org_1", Slug: "bravo", OwnerUserID: "user_3", FeedVisible: false} // private
	fs.sites["site_c"] = store.Site{ID: "site_c", OrgID: "org_1", Slug: "charlie", OwnerUserID: "user_1", FeedVisible: true}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountFeed(a, claims("user_1", "org_1", "member"))

	rr := getReq(h, "/v1/feed")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	got := feedSlugs(t, rr.Body.Bytes())
	for _, s := range got {
		if s == "bravo" {
			t.Fatalf("private site 'bravo' leaked into the feed: %v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("feed should list the 2 shared sites, got %v", got)
	}
}

// TestSetFeedVisibility_OwnerCanGoPrivate verifies a site's OWNER may mark their
// own site private even as a plain member (no admin role), and that it then drops
// out of the feed.
func TestSetFeedVisibility_OwnerCanGoPrivate(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "mine", OwnerUserID: "user_1", FeedVisible: true}
	fs.p2().members["user_1"] = store.RoleMember // not an admin
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountFeed(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/site_1/feed", `{"visible":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("owner make-private status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		FeedVisible bool `json:"feed_visible"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.FeedVisible {
		t.Fatalf("feed_visible should be false after making the site private")
	}
	// And it's gone from the feed now.
	if got := feedSlugs(t, getReq(h, "/v1/feed").Body.Bytes()); len(got) != 0 {
		t.Fatalf("private site should not appear in feed, got %v", got)
	}
}

// TestSetFeedVisibility_NonOwnerMemberForbidden verifies a plain member who does
// NOT own the site can't toggle its feed visibility (403) — only the owner or an
// admin may.
func TestSetFeedVisibility_NonOwnerMemberForbidden(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "theirs", OwnerUserID: "user_2", FeedVisible: true}
	fs.p2().members["user_1"] = store.RoleMember
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountFeed(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/site_1/feed", `{"visible":false}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-owner member status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

// TestSetFeedVisibility_AdminCanToggleOthers verifies an org admin may toggle the
// feed visibility of a site they don't own.
func TestSetFeedVisibility_AdminCanToggleOthers(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "theirs", OwnerUserID: "user_2", FeedVisible: true}
	fs.p2().members["user_1"] = store.RoleAdmin
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountFeed(a, claims("user_1", "org_1", "admin"))

	rr := putJSON(h, "/v1/sites/site_1/feed", `{"visible":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin toggle status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

// TestSetVote_UpThenClear verifies an upvote raises the score and a follow-up
// value=0 clears it, and that the feed reflects the caller's own vote.
func TestSetVote_UpThenClear(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s", OwnerUserID: "user_2", FeedVisible: true}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountFeed(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/site_1/vote", `{"value":1}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("upvote status = %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Score  int64 `json:"score"`
		MyVote int   `json:"my_vote"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Score != 1 || resp.MyVote != 1 {
		t.Fatalf("after upvote score=%d my_vote=%d, want 1/1", resp.Score, resp.MyVote)
	}

	// The feed shows the caller's vote + score.
	var feed struct {
		Sites []struct {
			Score  int64 `json:"score"`
			MyVote int   `json:"my_vote"`
		} `json:"sites"`
	}
	if err := json.Unmarshal(getReq(h, "/v1/feed").Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode feed: %v", err)
	}
	if len(feed.Sites) != 1 || feed.Sites[0].Score != 1 || feed.Sites[0].MyVote != 1 {
		t.Fatalf("feed = %+v, want score/my_vote 1", feed.Sites)
	}

	// Clearing the vote drops the score back to 0.
	rr = putJSON(h, "/v1/sites/site_1/vote", `{"value":0}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear status = %d: %s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Score != 0 || resp.MyVote != 0 {
		t.Fatalf("after clear score=%d my_vote=%d, want 0/0", resp.Score, resp.MyVote)
	}
}

// TestSetVote_BadValue400 rejects an out-of-range vote.
func TestSetVote_BadValue400(t *testing.T) {
	fs := newFakeStore()
	fs.sites["site_1"] = store.Site{ID: "site_1", OrgID: "org_1", Slug: "s", FeedVisible: true}
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountFeed(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/site_1/vote", `{"value":5}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

// TestSetVote_UnknownSite404 votes on an absent site → 404.
func TestSetVote_UnknownSite404(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountFeed(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/missing/vote", `{"value":1}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

// TestSetFeedVisibility_UnknownSite404 verifies an absent (or other-tenant) site
// surfaces as a 404 before any role/owner check.
func TestSetFeedVisibility_UnknownSite404(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
	h := mountFeed(a, claims("user_1", "org_1", "member"))

	rr := putJSON(h, "/v1/sites/missing/feed", `{"visible":false}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}
