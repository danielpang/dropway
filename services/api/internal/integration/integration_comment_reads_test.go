//go:build integration

// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func TestIntegration_CommentReadsAndCandidatePublish(t *testing.T) {
	ctx := context.Background()
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})

	org := "11111111-1111-1111-1111-111111111111"
	owner := "a0000000-0000-0000-0000-000000000001"
	reviewer := "a0000000-0000-0000-0000-000000000002"
	outsider := "b0000000-0000-0000-0000-000000000001"
	tn := store.Tenant{OrgID: org, UserID: owner}
	tr := store.Tenant{OrgID: org, UserID: reviewer}
	to := store.Tenant{OrgID: "22222222-2222-2222-2222-222222222222", UserID: outsider}
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", org)
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", to.OrgID)
	seedAuthOrg(t, org, "orga")
	seedAuthOrg(t, to.OrgID, "orgb")
	must(t, st.EnsureOrgProvisioned(ctx, tn))
	must(t, st.EnsureOrgProvisioned(ctx, to))
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	mustExec(t, `CREATE TABLE IF NOT EXISTS identity.member (id text PRIMARY KEY DEFAULT gen_random_uuid()::text, "organizationId" uuid NOT NULL, "userId" uuid NOT NULL, "role" text NOT NULL)`)
	mustExec(t, `GRANT USAGE ON SCHEMA identity TO dropway_app`)
	mustExec(t, `GRANT SELECT ON identity.member TO dropway_app`)
	mustExec(t, `INSERT INTO identity.member ("organizationId","userId","role") VALUES ($1,$2,'member')`, org, reviewer)

	site, err := st.CreateSite(ctx, tn, "alpha", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{
		SiteID: site.ID, ContentHash: strings.Repeat("ab", 32), SizeBytes: 1, Status: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Publish(ctx, tn, site.ID, v1.ID); err != nil {
		t.Fatal(err)
	}
	room, err := st.CreateProjectRoom(ctx, tn, "Review", store.AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectRoomMember(ctx, tn, room.ID, reviewer, "reviewer", store.AuditRecord{}); err != nil {
		t.Fatal(err)
	}
	var threads []string
	for i := 0; i < 3; i++ {
		th, err := st.CreateReviewThread(ctx, tr, room.ID, site.ID, v1.ID, "/index.html", "page", []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		threads = append(threads, th.ID)
	}
	var replies []string
	for i := 0; i < 3; i++ {
		rp, err := st.AddReviewReply(ctx, tr, room.ID, threads[0], "reply")
		if err != nil {
			t.Fatal(err)
		}
		replies = append(replies, rp.ID)
	}
	if _, err := st.CreatePostComment(ctx, tn, store.CreatePostCommentParams{SubjectType: store.SubjectSite, SubjectID: site.ID, Body: "feed"}); err != nil {
		t.Fatal(err)
	}
	resolved, err := st.ResolveReviewThread(ctx, tr, room.ID, threads[1], store.AuditRecord{})
	if err != nil || resolved.Status != "resolved" || len(resolved.Revisions) != 1 {
		t.Fatalf("resolve %+v %v", resolved.Status, err)
	}
	reanchored, err := st.ReanchorReviewThread(ctx, tr, room.ID, threads[2], "page", "orphaned", "", []byte(`{}`), store.AuditRecord{})
	if err != nil || reanchored.AnchorState != "orphaned" || len(reanchored.Revisions) != 2 || reanchored.VersionID != v1.ID || reanchored.Page != "/index.html" {
		t.Fatalf("reanchor %+v %v", reanchored, err)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := st.PageRoomThreads(ctx, tr, room.ID, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Threads) > 1 {
			t.Fatalf("page size %d", len(page.Threads))
		}
		for _, th := range page.Threads {
			if seen[th.ID] {
				t.Fatalf("duplicate thread %s", th.ID)
			}
			seen[th.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("cursor stuck")
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("threads seen %d", len(seen))
	}
	gotReplies := map[string]bool{}
	cursor = ""
	for {
		page, err := st.PageReviewReplies(ctx, tr, threads[0], cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, rp := range page.Replies {
			gotReplies[rp.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(gotReplies) != len(replies) {
		t.Fatalf("replies %d", len(gotReplies))
	}
	cand, err := st.SubmitCandidate(ctx, tn, room.ID, site.ID, v1.ID, strings.Repeat("cd", 32), strings.Repeat("ef", 32), "rename")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{
		SiteID: site.ID, ContentHash: cand.ContentHash, SizeBytes: 1, Status: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Publish(store.WithHumanSession(ctx, true), tn, site.ID, v2.ID); !errors.Is(err, store.ErrCandidatePublishDenied) {
		t.Fatalf("unapproved publish err=%v", err)
	}
	live, err := st.GetSite(ctx, tn, site.ID)
	if err != nil || live.CurrentVersionID == nil || *live.CurrentVersionID != v1.ID {
		t.Fatal("live pointer changed")
	}
	if _, err := st.PageReviewReplies(ctx, to, threads[0], "", 1); !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrProjectRoomsDisabled) {
		t.Fatalf("cross-tenant err=%v", err)
	}
	if err := st.RemoveProjectRoomMember(ctx, tn, room.ID, reviewer, store.AuditRecord{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PageReviewReplies(ctx, tr, threads[0], "", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked member err=%v", err)
	}
	detail, err := st.GetRoomCandidate(ctx, tn, room.ID, cand.ID)
	if err != nil || detail.ContentHash != cand.ContentHash || detail.State != projectroom.CandidatePending {
		t.Fatalf("candidate %+v %v", detail, err)
	}
}
