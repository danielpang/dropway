//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// TestIntegration_Feed exercises the org feed end-to-end against real Postgres +
// RLS: ListFeedSites returns the active org's feed-visible sites newest-first,
// SetSiteFeedVisible takes a site off the feed (without touching access), and RLS
// keeps one org's feed and toggles from reaching another org's sites.
func TestIntegration_Feed(t *testing.T) {
	ctx := context.Background()
	repoRoot := repoRoot(t)

	startPostgres(t)
	applyMigrations(t, repoRoot)
	seedAuthMemberTable(t)

	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as dropway_app: %v", err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})

	orgA := "11111111-1111-1111-1111-1111111111aa"
	orgB := "22222222-2222-2222-2222-2222222222bb"
	userOwnerA := "a0000000-0000-0000-0000-0000000000a1"
	userMemberA := "a0000000-0000-0000-0000-0000000000a2"
	userB := "b0000000-0000-0000-0000-0000000000b1"
	tA := store.Tenant{OrgID: orgA, UserID: userOwnerA}
	tAMember := store.Tenant{OrgID: orgA, UserID: userMemberA}
	tB := store.Tenant{OrgID: orgB, UserID: userB}

	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgA)
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgB)
	seedAuthOrg(t, orgA, "orga")
	seedAuthOrg(t, orgB, "orgb")
	must(t, st.EnsureOrgProvisioned(ctx, tA))
	must(t, st.EnsureOrgProvisioned(ctx, tB))
	insertMember(t, orgA, userOwnerA, store.RoleOwner)
	insertMember(t, orgA, userMemberA, store.RoleMember)
	insertMember(t, orgB, userB, store.RoleMember)

	// Three sites in org A, owned by two different users. Pin created_at so the
	// newest-first order is deterministic (oldest → newest = s1 → s2 → s3).
	s1, err := st.CreateSite(ctx, tA, "oldest", projection.AccessOrgOnly)
	must2(t, err)
	s2, err := st.CreateSite(ctx, tAMember, "middle", projection.AccessOrgOnly)
	must2(t, err)
	s3, err := st.CreateSite(ctx, tA, "newest", projection.AccessOrgOnly)
	must2(t, err)
	mustExec(t, "UPDATE app.sites SET created_at = now() - interval '3 hours' WHERE id = $1", s1.ID)
	mustExec(t, "UPDATE app.sites SET created_at = now() - interval '2 hours' WHERE id = $1", s2.ID)
	mustExec(t, "UPDATE app.sites SET created_at = now() - interval '1 hours' WHERE id = $1", s3.ID)

	// All three are feed-visible by default → feed lists them newest-first.
	feed, err := st.ListFeedSites(ctx, tA)
	must2(t, err)
	if got := slugs(feed); !equalStrings(got, []string{"newest", "middle", "oldest"}) {
		t.Fatalf("feed order = %v, want [newest middle oldest]", got)
	}
	for _, s := range feed {
		if !s.FeedVisible {
			t.Fatalf("site %q should be feed-visible by default", s.Slug)
		}
	}

	// Make the middle site private. Feed visibility is orthogonal to access, so the
	// access_mode is untouched and the site simply leaves the feed.
	updated, err := st.SetSiteFeedVisible(ctx, tAMember, s2.ID, false)
	must2(t, err)
	if updated.FeedVisible {
		t.Fatal("SetSiteFeedVisible(false) should return feed_visible=false")
	}
	if updated.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("access_mode changed by feed toggle: %q", updated.AccessMode)
	}

	feed, err = st.ListFeedSites(ctx, tA)
	must2(t, err)
	if got := slugs(feed); !equalStrings(got, []string{"newest", "oldest"}) {
		t.Fatalf("feed after private = %v, want [newest oldest]", got)
	}

	// A plain member of org A sees the same org-scoped feed (it's not per-user).
	feedMember, err := st.ListFeedSites(ctx, tAMember)
	must2(t, err)
	if got := slugs(feedMember); !equalStrings(got, []string{"newest", "oldest"}) {
		t.Fatalf("member feed = %v, want [newest oldest]", got)
	}

	// Re-share it: it returns to the feed at its (older) position.
	if _, err := st.SetSiteFeedVisible(ctx, tA, s2.ID, true); err != nil {
		t.Fatalf("re-share: %v", err)
	}
	feed, err = st.ListFeedSites(ctx, tA)
	must2(t, err)
	if got := slugs(feed); !equalStrings(got, []string{"newest", "middle", "oldest"}) {
		t.Fatalf("feed after re-share = %v, want [newest middle oldest]", got)
	}

	// RLS: org B has no sites of its own → an empty feed, and it can never see or
	// toggle org A's sites (the row is invisible under B's tenant → ErrNotFound).
	feedB, err := st.ListFeedSites(ctx, tB)
	must2(t, err)
	if len(feedB) != 0 {
		t.Fatalf("org B feed should be empty, got %v", slugs(feedB))
	}
	if _, err := st.SetSiteFeedVisible(ctx, tB, s1.ID, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-org toggle err = %v, want ErrNotFound", err)
	}
	// And A's site stayed visible (B's toggle was a no-op miss).
	if _, err := st.GetSite(ctx, tA, s1.ID); err != nil {
		t.Fatalf("get s1 after cross-org toggle: %v", err)
	}

	// --- Feed metadata: set a title + description, then clear them. ---
	withMeta, err := st.SetSiteFeedMeta(ctx, tA, s1.ID, "Quarterly Report", "Q3 numbers")
	must2(t, err)
	if withMeta.Title != "Quarterly Report" || withMeta.Description != "Q3 numbers" {
		t.Fatalf("meta = %+v, want title/description set", withMeta)
	}
	// The metadata persists on a fresh read.
	got1, err := st.GetSite(ctx, tA, s1.ID)
	must2(t, err)
	if got1.Title != "Quarterly Report" || got1.Description != "Q3 numbers" {
		t.Fatalf("persisted meta = %+v", got1)
	}
	// Empty strings clear back to NULL (round-trips as empty).
	cleared, err := st.SetSiteFeedMeta(ctx, tA, s1.ID, "", "")
	must2(t, err)
	if cleared.Title != "" || cleared.Description != "" {
		t.Fatalf("cleared meta = %+v, want empty", cleared)
	}

	// --- Comments: post with a mention, list the thread, assert RLS isolation. ---
	c1, err := st.CreateSiteComment(ctx, tAMember, store.CreateSiteCommentParams{
		SiteID:           s1.ID,
		Body:             "nice work",
		MentionedUserIDs: []string{userOwnerA},
	})
	must2(t, err)
	if c1.AuthorUserID != userMemberA || len(c1.MentionedUserIDs) != 1 || c1.MentionedUserIDs[0] != userOwnerA {
		t.Fatalf("comment = %+v, want author=member, mention=owner", c1)
	}
	thread, err := st.ListSiteComments(ctx, tA, s1.ID)
	must2(t, err)
	if len(thread) != 1 || thread[0].Body != "nice work" {
		t.Fatalf("thread = %+v, want one comment 'nice work'", thread)
	}
	// RLS: org B sees none of org A's comments.
	threadB, err := st.ListSiteComments(ctx, tB, s1.ID)
	must2(t, err)
	if len(threadB) != 0 {
		t.Fatalf("org B should see no comments on org A's site, got %d", len(threadB))
	}
}

// must2 fails the test on a non-nil error (a two-value helper for the `x, err :=`
// call sites the single-value `must` can't take).
func must2(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func slugs(sites []store.Site) []string {
	out := make([]string, len(sites))
	for i, s := range sites {
		out[i] = s.Slug
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
