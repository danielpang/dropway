//go:build integration

// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func TestIntegration_ProjectRooms(t *testing.T) {
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
	otherOrg := "22222222-2222-2222-2222-222222222222"
	owner := "a0000000-0000-0000-0000-000000000001"
	reviewer := "a0000000-0000-0000-0000-000000000002"
	outsider := "b0000000-0000-0000-0000-000000000001"
	tOwner := store.Tenant{OrgID: org, UserID: owner}
	tReviewer := store.Tenant{OrgID: org, UserID: reviewer}
	tOut := store.Tenant{OrgID: otherOrg, UserID: outsider}

	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", org)
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", otherOrg)
	seedAuthOrg(t, org, "orga")
	seedAuthOrg(t, otherOrg, "orgb")
	must(t, st.EnsureOrgProvisioned(ctx, tOwner))
	must(t, st.EnsureOrgProvisioned(ctx, tOut))

	if enabled, err := st.ProjectRoomsEnabled(ctx, tOwner); err != nil || !enabled {
		t.Fatalf("rooms default on, enabled=%v err=%v", enabled, err)
	}
	must(t, st.SetProjectRoomsEnabled(ctx, tOwner, false, store.AuditRecord{}))
	if _, err := st.CreateProjectRoom(ctx, tOwner, "Review", store.AuditRecord{}); !errors.Is(err, store.ErrProjectRoomsDisabled) {
		t.Fatalf("explicit organization disable must still apply, err=%v", err)
	}
	must(t, st.SetProjectRoomsEnabled(ctx, tOwner, true, store.AuditRecord{}))
	must(t, st.SetProjectRoomsEnabled(ctx, tOut, true, store.AuditRecord{}))

	site, err := st.CreateSite(ctx, tOwner, "alpha", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	ownerBefore := site.OwnerUserID
	ver, err := st.CreateSiteVersion(ctx, tOwner, store.CreateSiteVersionParams{
		SiteID: site.ID, ContentHash: strings.Repeat("ab", 32), SizeBytes: 1, Status: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Publish(ctx, tOwner, site.ID, ver.ID); err != nil {
		t.Fatal(err)
	}

	room, err := st.CreateProjectRoom(ctx, tOwner, "Review", store.AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectRoomSite(ctx, tOwner, room.ID, site.ID, store.AuditRecord{}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSite(ctx, tOwner, site.ID)
	if err != nil || got.OwnerUserID != ownerBefore {
		t.Fatalf("site ownership changed: %+v %v", got, err)
	}
	if _, err := st.GetProjectRoom(ctx, tOut, room.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-org err=%v", err)
	}
	if _, err := st.GetProjectRoom(ctx, tReviewer, room.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("non-member err=%v", err)
	}

	mustExec(t, `CREATE TABLE IF NOT EXISTS identity.member (
		id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
		"organizationId" uuid NOT NULL,
		"userId" uuid NOT NULL,
		"role" text NOT NULL)`)
	mustExec(t, `GRANT USAGE ON SCHEMA identity TO dropway_app`)
	mustExec(t, `GRANT SELECT ON identity.member TO dropway_app`)
	mustExec(t, `INSERT INTO identity.member ("organizationId","userId","role") VALUES ($1,$2,'member')`, org, reviewer)
	if err := st.AddProjectRoomMember(ctx, tOwner, room.ID, reviewer, "reviewer", store.AuditRecord{}); err != nil {
		t.Fatal(err)
	}

	th, err := st.CreateReviewThread(ctx, tReviewer, room.ID, site.ID, ver.ID, "/index.html", "page", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddReviewReply(ctx, tReviewer, room.ID, th.ID, "looks good"); err != nil {
		t.Fatal(err)
	}
	var comments int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.post_comments`).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if comments != 0 {
		t.Fatalf("feed comments = %d, review replies must stay separate", comments)
	}
	if _, err := st.SubmitCandidate(ctx, tReviewer, room.ID, site.ID, ver.ID, strings.Repeat("cd", 32), strings.Repeat("ef", 32), "rename the title"); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("reviewer submit err=%v", err)
	}

	cand, err := st.SubmitCandidate(ctx, tOwner, room.ID, site.ID, ver.ID, strings.Repeat("cd", 32), strings.Repeat("ef", 32), "rename the title")
	if err != nil {
		t.Fatal(err)
	}
	req := projectroom.ApprovalRequest{
		CandidateID: cand.ID, ContentHash: cand.ContentHash,
		BaseVersionID: cand.BaseVersionID, BaseContentHash: cand.BaseContentHash,
		HumanSession: true,
	}
	dec, err := st.ApproveCandidate(ctx, tOwner, room.ID, req, store.AuditRecord{Ctx: audit.Context{ActorUser: owner}})
	if err != nil || !dec.Changed || dec.Candidate.State != projectroom.CandidateApproved {
		t.Fatalf("approve %+v %v", dec, err)
	}
	again, err := st.ApproveCandidate(ctx, tOwner, room.ID, req, store.AuditRecord{})
	if err != nil || again.Changed {
		t.Fatalf("idempotent approve %+v %v", again, err)
	}
	replay := req
	replay.CandidateID = "99999999-9999-9999-9999-999999999999"
	if _, err := st.ApproveCandidate(ctx, tOwner, room.ID, replay, store.AuditRecord{}); !errors.Is(err, store.ErrNotFound) && !errors.Is(err, projectroom.ErrCandidateMismatch) {
		t.Fatalf("replay err=%v", err)
	}
	if _, err := st.ApplyCandidate(store.WithHumanSession(ctx, true), tOwner, room.ID, req); !errors.Is(err, store.ErrCandidateArtifact) {
		t.Fatalf("apply err=%v", err)
	}
	live, err := st.GetSite(ctx, tOwner, site.ID)
	if err != nil || live.CurrentVersionID == nil || *live.CurrentVersionID != ver.ID {
		t.Fatalf("live pointer changed: %+v %v", live, err)
	}

	other, err := st.SubmitCandidate(ctx, tOwner, room.ID, site.ID, ver.ID, strings.Repeat("12", 32), strings.Repeat("34", 32), "a second proposal")
	if err != nil {
		t.Fatal(err)
	}
	rej := projectroom.ApprovalRequest{
		CandidateID: other.ID, ContentHash: other.ContentHash,
		BaseVersionID: other.BaseVersionID, BaseContentHash: other.BaseContentHash, HumanSession: true,
	}
	if _, err := st.RejectCandidate(ctx, tOwner, room.ID, rej, store.AuditRecord{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveCandidate(ctx, tOwner, room.ID, rej, store.AuditRecord{}); !errors.Is(err, projectroom.ErrCandidateTerminal) {
		t.Fatalf("approve after reject err=%v", err)
	}
	req.HumanSession = false
	if _, err := st.ApproveCandidate(ctx, tOwner, room.ID, req, store.AuditRecord{}); err == nil {
		t.Fatal("non-human approval must fail")
	}
}
