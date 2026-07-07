package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
)

// skillsFeedRouter mounts the feed + skill-feed routes (visibility, meta, vote,
// comments) plus create/get, authenticated as (orgID, userID).
func skillsFeedRouter(a *API, orgID, userID string) http.Handler {
	v := fakeVerifier{claims: claims(userID, orgID, "member")}
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Get("/v1/feed", a.ListFeed)
		r.Post("/v1/skills", a.CreateSkill)
		r.Get("/v1/skills/{id}", a.GetSkill)
		r.Put("/v1/skills/{id}/feed", a.SetSkillFeedVisibility)
		r.Put("/v1/skills/{id}/feed-meta", a.SetSkillFeedMeta)
		r.Put("/v1/skills/{id}/vote", a.SetSkillVote)
		r.Get("/v1/skills/{id}/comments", a.ListSkillComments)
		r.Post("/v1/skills/{id}/comments", a.AddSkillComment)
	})
	return r
}

type feedPost struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Slug         string `json:"slug"`
	FeedVisible  bool   `json:"feed_visible"`
	Score        int64  `json:"score"`
	MyVote       int    `json:"my_vote"`
	CommentCount int64  `json:"comment_count"`
}

func feedPosts(t *testing.T, h http.Handler) []feedPost {
	t.Helper()
	rr := getReq(h, "/v1/feed")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/feed = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Posts []feedPost `json:"posts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode feed: %v", err)
	}
	return body.Posts
}

// createFeedSkill creates a skill and returns its id.
func createFeedSkill(t *testing.T, h http.Handler, slug string) string {
	t.Helper()
	rr := do(t, h, http.MethodPost, "/v1/skills", `{"slug":"`+slug+`"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create skill: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Skill skillResponse `json:"skill"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode skill: %v", err)
	}
	return out.Skill.ID
}

// TestSkillFeed_AutoShareVoteComment verifies a newly created skill auto-joins the
// feed (feed_visible default), can be voted on and commented on, and that making
// it private removes it from the feed and rejects further votes.
func TestSkillFeed_AutoShareVoteComment(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := skillsFeedRouter(a, "org_1", "user_1")

	skillID := createFeedSkill(t, h, "pr-review")

	// The skill is on the feed, tagged kind=skill, owner sees a draft (no version).
	posts := feedPosts(t, h)
	var found *feedPost
	for i := range posts {
		if posts[i].ID == skillID {
			found = &posts[i]
		}
	}
	if found == nil {
		t.Fatalf("new skill not on the feed: %+v", posts)
	}
	if found.Kind != "skill" || found.Slug != "pr-review" || !found.FeedVisible {
		t.Fatalf("feed post = %+v, want kind=skill slug=pr-review feed_visible", *found)
	}

	// Vote it up.
	rr := putJSON(h, "/v1/skills/"+skillID+"/vote", `{"value":1}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("vote: %d %s", rr.Code, rr.Body.String())
	}
	var vote struct {
		Kind   string `json:"kind"`
		Score  int64  `json:"score"`
		MyVote int    `json:"my_vote"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &vote)
	if vote.Kind != "skill" || vote.Score != 1 || vote.MyVote != 1 {
		t.Fatalf("vote resp = %+v, want skill/1/1", vote)
	}

	// Comment on it.
	rr = do(t, h, http.MethodPost, "/v1/skills/"+skillID+"/comments", `{"body":"handy!"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("comment: %d %s", rr.Code, rr.Body.String())
	}
	var cmt commentResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &cmt)
	if cmt.SubjectType != "skill" || cmt.SubjectID != skillID || cmt.Body != "handy!" {
		t.Fatalf("comment = %+v, want skill subject", cmt)
	}

	// The feed now reflects the vote + comment count.
	posts = feedPosts(t, h)
	for _, p := range posts {
		if p.ID == skillID {
			if p.Score != 1 || p.MyVote != 1 || p.CommentCount != 1 {
				t.Fatalf("feed post social = %+v, want score/vote/comments 1/1/1", p)
			}
		}
	}

	// The comment thread lists it.
	rr = getReq(h, "/v1/skills/"+skillID+"/comments")
	if rr.Code != http.StatusOK {
		t.Fatalf("list comments: %d %s", rr.Code, rr.Body.String())
	}
	var thread struct {
		Comments []commentResponse `json:"comments"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &thread)
	if len(thread.Comments) != 1 || thread.Comments[0].Body != "handy!" {
		t.Fatalf("thread = %+v, want one comment", thread.Comments)
	}

	// Make it private → it leaves the feed and votes are rejected.
	rr = putJSON(h, "/v1/skills/"+skillID+"/feed", `{"visible":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("go private: %d %s", rr.Code, rr.Body.String())
	}
	for _, p := range feedPosts(t, h) {
		if p.ID == skillID {
			t.Fatalf("private skill still on the feed: %+v", p)
		}
	}
	rr = putJSON(h, "/v1/skills/"+skillID+"/vote", `{"value":1}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("vote on private skill = %d, want 404", rr.Code)
	}
}

// TestSkillFeed_MetaEdit verifies the owner can set a skill's feed title/description
// (which are the skill's own title/description).
func TestSkillFeed_MetaEdit(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), projection.NewLocal())
	h := skillsFeedRouter(a, "org_1", "user_1")

	skillID := createFeedSkill(t, h, "pr-review")

	rr := putJSON(h, "/v1/skills/"+skillID+"/feed-meta", `{"title":"PR Review","description":"Checklist"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("set meta: %d %s", rr.Code, rr.Body.String())
	}
	rr = getReq(h, "/v1/skills/"+skillID)
	var out struct {
		Skill skillResponse `json:"skill"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Skill.Title != "PR Review" || out.Skill.Description != "Checklist" {
		t.Fatalf("skill meta = %+v, want title/description set", out.Skill)
	}
}
