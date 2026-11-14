//go:build integration

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package integration

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_CandidateApplication(t *testing.T) {
	ctx := store.WithHumanSession(context.Background(), true)
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})
	org, owner := "11111111-1111-1111-1111-111111111111", "a0000000-0000-0000-0000-000000000001"
	tn := store.Tenant{OrgID: org, UserID: owner}
	mustExec(t, "INSERT INTO app.org_meta (id,allow_external_sharing) VALUES ($1,true)", org)
	seedAuthOrg(t, org, "orga")
	must(t, st.EnsureOrgProvisioned(ctx, tn))
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	site, err := st.CreateSite(ctx, tn, "apply", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	version := func(hash string) store.SiteVersion {
		v, e := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: site.ID, ContentHash: strings.Repeat(hash, 32), SizeBytes: 1, Status: "ready"})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	base, proposed := version("ab"), version("cd")
	if _, err := st.Publish(ctx, tn, site.ID, base.ID); err != nil {
		t.Fatal(err)
	}
	room, err := st.CreateProjectRoom(ctx, tn, "Apply", store.AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	assertIndependentSiteMutations(t, ctx, st, tn, room.ID, site.ID, base.ID)
	must(t, st.AddProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	c := submit(t, ctx, st, tn, room.ID, site.ID, base.ID, proposed.ContentHash)
	req := approvalReq(c)
	req.VerifiedVersionID = proposed.ID
	if _, err := st.ApplyCandidate(ctx, tn, room.ID, req); !errors.Is(err, projectroom.ErrCandidateDenied) {
		t.Fatalf("pending apply: %v", err)
	}
	if _, err := st.ApproveCandidate(ctx, tn, room.ID, req, auditRec(owner)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyCandidate(context.Background(), tn, room.ID, req); !errors.Is(err, projectroom.ErrHumanRequired) {
		t.Fatalf("unclassified caller: %v", err)
	}
	swapped := req
	swapped.ContentHash = base.ContentHash
	if _, err := st.ApplyCandidate(ctx, tn, room.ID, swapped); !errors.Is(err, projectroom.ErrCandidateMismatch) {
		t.Fatalf("swapped: %v", err)
	}
	wrong := req
	wrong.VerifiedVersionID = base.ID
	if _, err := st.ApplyCandidate(ctx, tn, room.ID, wrong); !errors.Is(err, store.ErrCandidateArtifact) {
		t.Fatalf("wrong version: %v", err)
	}
	if _, err := st.Publish(ctx, tn, site.ID, proposed.ID); !errors.Is(err, store.ErrCandidatePublishDenied) {
		t.Fatalf("legacy approved publish bypass: %v", err)
	}
	// Duplicate callers serialize on the same site, publish once, then repair only
	// projection on retries. Neither approval nor publication effects repeat.
	var wg sync.WaitGroup
	results := make([]store.CandidateApplication, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = st.ApplyCandidate(ctx, tn, room.ID, req) }(i)
	}
	wg.Wait()
	changed := 0
	for i, res := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if res.Changed {
			changed++
		}
		if res.VersionID != proposed.ID || len(res.Publish.Routes) == 0 {
			t.Fatalf("bad result %+v", res)
		}
	}
	if changed != 1 {
		t.Fatalf("changed=%d", changed)
	}
	if counts := auditCounts(t, ctx, pool, org, "candidate:"+c.ID); counts["candidate.apply"] != 1 {
		t.Fatalf("audit counts %v", counts)
	}
	detail, err := st.GetRoomCandidate(ctx, tn, room.ID, c.ID)
	if err != nil || detail.AppliedVersionID == nil || *detail.AppliedVersionID != proposed.ID {
		t.Fatalf("missing persisted receipt %+v %v", detail, err)
	}
	page, err := st.PageRoomCandidates(ctx, tn, room.ID, "", 50)
	if err != nil || len(page.Candidates) != 1 || page.Candidates[0].AppliedVersionID == nil {
		t.Fatalf("missing list receipt %+v %v", page, err)
	}
	live, err := st.GetSite(ctx, tn, site.ID)
	if err != nil || live.CurrentVersionID == nil || *live.CurrentVersionID != proposed.ID {
		t.Fatalf("live %+v %v", live, err)
	}
	newer := version("ef")
	if _, err := st.Publish(ctx, tn, site.ID, newer.ID); err != nil {
		t.Fatal(err)
	}
	// A projection failure does not erase the committed application or its
	// durable job. A new process/store instance can recover it after live moves.
	if err := st.FlushCandidateProjection(ctx, tn, c.ID, failedCandidateProjection{}); err == nil {
		t.Fatal("expected projection outage")
	}
	recovered := store.New(pool, quota.Unlimited{})
	edge := projection.NewLocal()
	if n, err := recovered.DrainCandidateProjections(ctx, edge); err != nil || n != 1 {
		t.Fatalf("restart recovery count=%d err=%v", n, err)
	}
	host := projection.HostForSite("orga", site.Slug)
	if route, ok := edge.Get(host); !ok || route.VersionID != newer.ID {
		t.Fatalf("delayed job replayed old candidate: %+v %v", route, ok)
	}
	if n, err := recovered.DrainCandidateProjections(ctx, edge); err != nil || n != 0 {
		t.Fatalf("acknowledged job repeated count=%d err=%v", n, err)
	}
	// Legacy projection writes can arrive late. A bounded periodic reconciliation
	// repairs that cache without replaying the application/audit transaction.
	oldRoute, _ := edge.Get(host)
	oldRoute.VersionID = base.ID
	must(t, edge.PutRoute(ctx, host, oldRoute))
	mustExec(t, `UPDATE app.candidate_projection_outbox SET delivered_at=now()-interval '6 minutes' WHERE org_id=$1`, org)
	if n, err := recovered.DrainCandidateProjections(ctx, edge); err != nil || n != 1 {
		t.Fatalf("periodic repair count=%d err=%v", n, err)
	}
	if route, ok := edge.Get(host); !ok || route.VersionID != newer.ID {
		t.Fatalf("stale cache not repaired: %+v %v", route, ok)
	}
	if _, err := st.ApplyCandidate(ctx, tn, room.ID, req); !errors.Is(err, projectroom.ErrCandidateConflict) {
		t.Fatalf("old receipt replay: %v", err)
	}
	stale := submit(t, ctx, st, tn, room.ID, site.ID, newer.ID, strings.Repeat("12", 32))
	staleReq := approvalReq(stale)
	staleVersion := version("12")
	staleReq.VerifiedVersionID = staleVersion.ID
	if _, err := st.ApproveCandidate(ctx, tn, room.ID, staleReq, auditRec(owner)); err != nil {
		t.Fatal(err)
	}
	latest := version("34")
	if _, err := st.Publish(ctx, tn, site.ID, latest.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyCandidate(ctx, tn, room.ID, staleReq); !errors.Is(err, projectroom.ErrCandidateConflict) {
		t.Fatalf("stale baseline: %v", err)
	}
	live, err = st.GetSite(ctx, tn, site.ID)
	if err != nil || live.CurrentVersionID == nil || *live.CurrentVersionID != latest.ID {
		t.Fatalf("replay changed live %+v %v", live, err)
	}
	if _, err := st.Publish(ctx, tn, site.ID, proposed.ID); !errors.Is(err, store.ErrCandidatePublishDenied) {
		t.Fatalf("legacy replay bypass: %v", err)
	}
	gc, err := st.GCOrg(ctx, storage.NewFake(), org, store.GCPolicy{KeepLastN: 0, DryRun: true})
	if err != nil || gc.RetainedVersions != 5 {
		t.Fatalf("GC must retain review versions: %+v %v", gc, err)
	}
	// Feature rollback restores normal publication for human and API-key/MCP
	// callers (the unclassified context models the latter store authorization).
	must(t, st.SetProjectRoomsEnabled(ctx, tn, false, store.AuditRecord{}))
	for _, caller := range []context.Context{ctx, context.Background()} {
		if _, err := st.Publish(caller, tn, site.ID, proposed.ID); err != nil {
			t.Fatalf("disabled rooms historical rollback: %v", err)
		}
	}
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	if _, err := st.Publish(ctx, tn, site.ID, proposed.ID); !errors.Is(err, store.ErrCandidatePublishDenied) {
		t.Fatalf("reenabled candidate bypass: %v", err)
	}
	if _, err := st.Publish(context.Background(), tn, site.ID, latest.ID); err != nil {
		t.Fatalf("reenabled ordinary non-human publish: %v", err)
	}
	must(t, st.RemoveProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	for _, caller := range []context.Context{ctx, context.Background()} {
		if _, err := st.Publish(caller, tn, site.ID, proposed.ID); err != nil {
			t.Fatalf("detached historical rollback: %v", err)
		}
	}
	if _, err := st.ApplyCandidate(ctx, tn, room.ID, req); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("detached site: %v", err)
	}
	// Receipt foreign keys must not block deleting the owning site.
	if _, err := st.DeleteSite(ctx, tn, site.ID); err != nil {
		t.Fatalf("delete applied site: %v", err)
	}
	for _, direction := range []string{"down", "up"} {
		cmd := exec.Command("go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.22.0", "-dir", repoRoot(t)+"/db/migrations/app", "postgres", ownerDSN, direction)
		cmd.Dir = repoRoot(t)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("migration %s: %v\n%s", direction, err, output)
		}
	}

}

type failedCandidateProjection struct{ projection.Writer }

func (failedCandidateProjection) PutRoute(context.Context, string, projection.RouteValue) error {
	return errors.New("edge outage")
}

// A site operation must not exclusively serialize unrelated sites in its org,
// while feature toggles and same-site attachment must wait for its locks.
func assertIndependentSiteMutations(t *testing.T, ctx context.Context, st *store.Store, tn store.Tenant, roomID, siteID, versionID string) {
	t.Helper()
	other, err := st.CreateSite(ctx, tn, "parallel", projection.AccessOrgOnly)
	must(t, err)
	ver, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: other.ID, ContentHash: strings.Repeat("56", 32), SizeBytes: 1, Status: "ready"})
	must(t, err)
	conn, err := pgx.Connect(ctx, ownerDSN)
	must(t, err)
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	must(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `SELECT id FROM app.org_meta WHERE id=$1 FOR SHARE`, tn.OrgID)
	must(t, err)
	_, err = tx.Exec(ctx, `SELECT id FROM app.sites WHERE id=$1 FOR UPDATE`, siteID)
	must(t, err)
	free, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, err = st.Publish(free, tn, other.ID, ver.ID)
	cancel()
	if err != nil {
		t.Fatalf("unrelated site blocked behind shared org lock: %v", err)
	}
	for name, operation := range map[string]func(context.Context) error{
		"same site publish": func(c context.Context) error { _, err := st.Publish(c, tn, siteID, versionID); return err },
		"room attach": func(c context.Context) error {
			return st.AddProjectRoomSite(c, tn, roomID, siteID, store.AuditRecord{})
		},
		"feature toggle": func(c context.Context) error { return st.SetProjectRoomsEnabled(c, tn, false, store.AuditRecord{}) },
	} {
		blocked, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		err := operation(blocked)
		cancel()
		if err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s did not wait for protection lock: %v", name, err)
		}
	}
	must(t, tx.Rollback(ctx))
	_, err = st.DeleteSite(ctx, tn, other.ID)
	must(t, err)
}
