//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// TestIntegration_SkillFeed exercises skills as first-class feed posts against real
// Postgres + RLS: a created skill is feed-visible by default, the polymorphic
// post_votes / post_comments tables carry its vote score and thread, making it
// private takes it off ListFeedSkills, and RLS keeps another org from seeing or
// voting on it.
func TestIntegration_SkillFeed(t *testing.T) {
	ctx := context.Background()
	repoRoot := repoRoot(t)

	startPostgres(t)
	applyMigrations(t, repoRoot)
	seedAuthMemberTable(t)

	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})

	orgA := "33333333-3333-3333-3333-3333333333aa"
	orgB := "44444444-4444-4444-4444-4444444444bb"
	userOwnerA := "c0000000-0000-0000-0000-0000000000a1"
	userMemberA := "c0000000-0000-0000-0000-0000000000a2"
	userB := "d0000000-0000-0000-0000-0000000000b1"
	tA := store.Tenant{OrgID: orgA, UserID: userOwnerA}
	tAMember := store.Tenant{OrgID: orgA, UserID: userMemberA}
	tB := store.Tenant{OrgID: orgB, UserID: userB}

	mustExec(t, "INSERT INTO app.org_meta (id) VALUES ($1)", orgA)
	mustExec(t, "INSERT INTO app.org_meta (id) VALUES ($1)", orgB)
	seedAuthOrg(t, orgA, "orga")
	seedAuthOrg(t, orgB, "orgb")
	must(t, st.EnsureOrgProvisioned(ctx, tA))
	must(t, st.EnsureOrgProvisioned(ctx, tB))
	insertMember(t, orgA, userOwnerA, store.RoleOwner)
	insertMember(t, orgA, userMemberA, store.RoleMember)
	insertMember(t, orgB, userB, store.RoleMember)

	// A new skill is feed-visible by default → it's the owner's feed post. (No
	// upload yet: a skill with no current version is shown only to its owner.)
	sk, err := st.CreateSkill(ctx, tA, "pr-review", "PR review", nil)
	must2(t, err)
	if !sk.FeedVisible {
		t.Fatal("new skill should be feed-visible by default")
	}
	feed, err := st.ListFeedSkills(ctx, tA)
	must2(t, err)
	if len(feed) != 1 || feed[0].Slug != "pr-review" {
		t.Fatalf("owner feed = %v, want [pr-review]", skillFeedSlugs(feed))
	}
	// A different member can't yet see the draft (no current version).
	feedM, err := st.ListFeedSkills(ctx, tAMember)
	must2(t, err)
	if len(feedM) != 0 {
		t.Fatalf("member feed should not show a draft skill, got %v", skillFeedSlugs(feedM))
	}

	// Publish a version so the skill is visible to the whole org.
	v, err := st.CreateSkillVersion(ctx, tA, store.CreateSkillVersionParams{
		SkillID: sk.ID, ContentHash: "hash-1", SizeBytes: 10,
	})
	must2(t, err)
	must(t, st.PublishSkillVersion(ctx, tA, sk.ID, v.ID))

	feedM, err = st.ListFeedSkills(ctx, tAMember)
	must2(t, err)
	if len(feedM) != 1 {
		t.Fatalf("member should see the published skill, got %v", skillFeedSlugs(feedM))
	}

	// Votes: owner +1, member +1 → score 2, each caller's own vote reflected.
	if _, _, err := st.SetPostVote(ctx, tA, store.SubjectSkill, sk.ID, 1); err != nil {
		t.Fatalf("owner upvote: %v", err)
	}
	score, myVote, err := st.SetPostVote(ctx, tAMember, store.SubjectSkill, sk.ID, 1)
	must2(t, err)
	if score != 2 || myVote != 1 {
		t.Fatalf("after two upvotes score=%d my_vote=%d, want 2/1", score, myVote)
	}
	feedM, err = st.ListFeedSkills(ctx, tAMember)
	must2(t, err)
	if feedM[0].Score != 2 || feedM[0].MyVote != 1 {
		t.Fatalf("member feed social = (%d,%d), want (2,1)", feedM[0].Score, feedM[0].MyVote)
	}

	// Comments: member posts, thread lists it, RLS hides it from org B.
	if _, err := st.CreatePostComment(ctx, tAMember, store.CreatePostCommentParams{
		SubjectType: store.SubjectSkill, SubjectID: sk.ID, Body: "handy",
	}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	thread, err := st.ListPostComments(ctx, tA, store.SubjectSkill, sk.ID)
	must2(t, err)
	if len(thread) != 1 || thread[0].Body != "handy" {
		t.Fatalf("thread = %+v, want one comment", thread)
	}
	threadB, err := st.ListPostComments(ctx, tB, store.SubjectSkill, sk.ID)
	must2(t, err)
	if len(threadB) != 0 {
		t.Fatalf("org B must not see org A's skill comments, got %d", len(threadB))
	}

	// Make it private → it leaves the feed for everyone.
	if _, err := st.SetSkillFeedVisible(ctx, tA, sk.ID, false); err != nil {
		t.Fatalf("go private: %v", err)
	}
	feedM, err = st.ListFeedSkills(ctx, tAMember)
	must2(t, err)
	if len(feedM) != 0 {
		t.Fatalf("private skill should leave the feed, got %v", skillFeedSlugs(feedM))
	}

	// Deleting the skill runs the polymorphic vote+comment cleanup (no FK cascade,
	// so DeleteSkill removes them explicitly) against real tables — a malformed
	// cleanup query would error here. Then the thread reads back empty.
	must(t, st.DeleteSkill(ctx, tA, sk.ID))
	gone, err := st.ListPostComments(ctx, tA, store.SubjectSkill, sk.ID)
	must2(t, err)
	if len(gone) != 0 {
		t.Fatalf("skill comments should be cleaned up on delete, got %d", len(gone))
	}
}

func skillFeedSlugs(feed []store.FeedSkill) []string {
	out := make([]string, len(feed))
	for i, f := range feed {
		out[i] = f.Slug
	}
	return out
}
