//go:build integration

// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func TestIntegration_ApprovalRaces(t *testing.T) {
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
	tn := store.Tenant{OrgID: org, UserID: owner}
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", org)
	seedAuthOrg(t, org, "orga")
	must(t, st.EnsureOrgProvisioned(ctx, tn))
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))

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

	t.Run("approve versus reject", func(t *testing.T) {
		c := submit(t, ctx, st, tn, room.ID, site.ID, v1.ID, strings.Repeat("11", 32))
		req := approvalReq(c)
		var approveErr, rejectErr error
		var approveDec store.CandidateDecision
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			approveDec, approveErr = st.ApproveCandidate(ctx, tn, room.ID, req, auditRec(owner))
		}()
		go func() {
			defer wg.Done()
			<-start
			_, rejectErr = st.RejectCandidate(ctx, tn, room.ID, req, auditRec(owner))
		}()
		close(start)
		wg.Wait()
		state := candidateState(t, ctx, pool, org, c.ID)
		if state != projectroom.CandidateApproved && state != projectroom.CandidateRejected {
			t.Fatalf("state = %s", state)
		}
		if state == projectroom.CandidateApproved && approveErr != nil {
			t.Fatal(approveErr)
		}
		if state == projectroom.CandidateRejected && rejectErr != nil {
			t.Fatal(rejectErr)
		}
		audits := auditCounts(t, ctx, pool, org, "candidate:"+c.ID)
		if audits["candidate.approve_change"]+audits["candidate.reject_change"] != 1 {
			t.Fatalf("audits = %+v", audits)
		}
		if _, err := st.ApproveCandidate(ctx, tn, room.ID, req, auditRec(owner)); state == projectroom.CandidateRejected && !errors.Is(err, projectroom.ErrCandidateTerminal) {
			t.Fatalf("replay approve err = %v", err)
		}
		if again, err := st.ApproveCandidate(ctx, tn, room.ID, req, auditRec(owner)); state == projectroom.CandidateApproved && (err != nil || again.Changed) {
			t.Fatalf("duplicate approve %+v %v", again, err)
		}
		if got := auditCounts(t, ctx, pool, org, "candidate:"+c.ID); got["candidate.approve_change"]+got["candidate.reject_change"] != 1 {
			t.Fatalf("audits after replay = %+v", got)
		}
		_ = approveDec
	})

	t.Run("swapped hashes stay on their own candidates", func(t *testing.T) {
		a := submit(t, ctx, st, tn, room.ID, site.ID, v1.ID, strings.Repeat("21", 32))
		b := submit(t, ctx, st, tn, room.ID, site.ID, v1.ID, strings.Repeat("22", 32))
		swapped := approvalReq(a)
		swapped.ContentHash = b.ContentHash
		if _, err := st.ApproveCandidate(ctx, tn, room.ID, swapped, auditRec(owner)); !errors.Is(err, projectroom.ErrCandidateMismatch) {
			t.Fatalf("swapped hash err = %v", err)
		}
		if candidateState(t, ctx, pool, org, a.ID) != projectroom.CandidatePending || candidateState(t, ctx, pool, org, b.ID) != projectroom.CandidatePending {
			t.Fatal("swapped hash changed a candidate")
		}
		if _, err := st.ApproveCandidate(ctx, tn, room.ID, approvalReq(a), auditRec(owner)); err != nil {
			t.Fatal(err)
		}
		if candidateState(t, ctx, pool, org, a.ID) != projectroom.CandidateApproved {
			t.Fatal("A was not approved")
		}
		if candidateState(t, ctx, pool, org, b.ID) != projectroom.CandidatePending {
			t.Fatal("approval carried to B")
		}
	})

	t.Run("publish races approval", func(t *testing.T) {
		c := submit(t, ctx, st, tn, room.ID, site.ID, v1.ID, strings.Repeat("31", 32))
		v2, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{
			SiteID: site.ID, ContentHash: strings.Repeat("32", 32), SizeBytes: 1, Status: "ready",
		})
		if err != nil {
			t.Fatal(err)
		}
		req := approvalReq(c)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var approveErr, publishErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, approveErr = st.ApproveCandidate(ctx, tn, room.ID, req, auditRec(owner))
		}()
		go func() {
			defer wg.Done()
			<-start
			_, publishErr = st.Publish(ctx, tn, site.ID, v2.ID)
		}()
		close(start)
		wg.Wait()
		if publishErr != nil {
			t.Fatal(publishErr)
		}
		live, err := st.GetSite(ctx, tn, site.ID)
		if err != nil || live.CurrentVersionID == nil || *live.CurrentVersionID != v2.ID {
			t.Fatalf("live = %+v %v", live.CurrentVersionID, err)
		}
		state := candidateState(t, ctx, pool, org, c.ID)
		audits := auditCounts(t, ctx, pool, org, "candidate:"+c.ID)
		switch state {
		case projectroom.CandidateConflict:
			if approveErr == nil || !errors.Is(approveErr, projectroom.ErrCandidateConflict) {
				t.Fatalf("conflict err = %v", approveErr)
			}
			if audits["candidate.conflict"] != 1 || audits["candidate.approve_change"] != 0 {
				t.Fatalf("audits = %+v", audits)
			}
		case projectroom.CandidateApproved:
			if approveErr != nil {
				t.Fatal(approveErr)
			}
			if audits["candidate.approve_change"] != 1 || audits["candidate.conflict"] != 0 {
				t.Fatalf("audits = %+v", audits)
			}
			base := candidateBase(t, ctx, pool, org, c.ID)
			if base != v1.ID {
				t.Fatalf("approved base = %s, want %s", base, v1.ID)
			}
		default:
			t.Fatalf("state = %s err = %v", state, approveErr)
		}
		if _, err := st.ApplyCandidate(ctx, tn, room.ID, req); err == nil {
			t.Fatal("apply must not publish")
		}
		live, _ = st.GetSite(ctx, tn, site.ID)
		if live.CurrentVersionID == nil || *live.CurrentVersionID != v2.ID {
			t.Fatalf("apply moved live pointer to %v", live.CurrentVersionID)
		}
		// Put the live pointer back so later subtests that assume v1 still publish explicitly.
		if _, err := st.Publish(ctx, tn, site.ID, v1.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func submit(t *testing.T, ctx context.Context, st *store.Store, tn store.Tenant, roomID, siteID, versionID, hash string) store.Candidate {
	t.Helper()
	c, err := st.SubmitCandidate(ctx, tn, roomID, siteID, versionID, hash, strings.Repeat("ee", 32), "plain language summary")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func approvalReq(c store.Candidate) projectroom.ApprovalRequest {
	return projectroom.ApprovalRequest{
		CandidateID: c.ID, ContentHash: c.ContentHash,
		BaseVersionID: c.BaseVersionID, BaseContentHash: c.BaseContentHash,
		HumanSession: true,
	}
}

func auditRec(user string) store.AuditRecord {
	return store.AuditRecord{Ctx: audit.Context{ActorUser: user}}
}

func candidateState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org, id string) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', $1, true)`, org); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM app.review_candidates WHERE id = $1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func candidateBase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org, id string) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', $1, true)`, org); err != nil {
		t.Fatal(err)
	}
	var base string
	if err := tx.QueryRow(ctx, `SELECT base_version_id::text FROM app.review_candidates WHERE id = $1`, id).Scan(&base); err != nil {
		t.Fatal(err)
	}
	return base
}

func auditCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org, target string) map[string]int {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', $1, true)`, org); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT action, count(*) FROM app.audit_log WHERE org_id = $1 AND target = $2 GROUP BY action`, org, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var action string
		var n int
		if err := rows.Scan(&action, &n); err != nil {
			t.Fatal(err)
		}
		out[action] = n
	}
	return out
}
