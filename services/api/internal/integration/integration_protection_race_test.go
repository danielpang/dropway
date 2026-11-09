//go:build integration

// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func TestIntegration_ProtectionPublishRace(t *testing.T) {
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
	v2, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{
		SiteID: site.ID, ContentHash: strings.Repeat("cd", 32), SizeBytes: 1, Status: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := st.CreateProjectRoom(ctx, tn, "Review", store.AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org_id', $1, true)", org); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT id FROM app.org_meta WHERE id = $1 FOR UPDATE", org); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT id FROM app.sites WHERE id = $1 FOR UPDATE", site.ID); err != nil {
		t.Fatal(err)
	}

	publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	var publishErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		_, publishErr = st.Publish(store.WithHumanSession(publishCtx, false), tn, site.ID, v2.ID)
	}()
	<-started
	time.Sleep(200 * time.Millisecond)
	if _, err := tx.Exec(ctx, `INSERT INTO app.project_room_sites (org_id, room_id, site_id, created_by) VALUES ($1, $2, $3, $4)`, org, room.ID, site.ID, owner); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !errors.Is(publishErr, store.ErrControlledSite) {
		t.Fatalf("publish after concurrent attach = %v, want controlled-site denial", publishErr)
	}
	live, err := st.GetSite(ctx, tn, site.ID)
	if err != nil || live.CurrentVersionID == nil || *live.CurrentVersionID != v1.ID {
		t.Fatalf("live pointer moved: %+v %v", live.CurrentVersionID, err)
	}

	other, err := st.CreateSite(ctx, tn, "beta", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	ov, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{
		SiteID: other.ID, ContentHash: strings.Repeat("ef", 32), SizeBytes: 1, Status: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	hold, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Release()
	htx, err := hold.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := htx.Exec(ctx, "SELECT set_config('app.current_org_id', $1, true)", org); err != nil {
		t.Fatal(err)
	}
	if _, err := htx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := htx.Exec(ctx, "SELECT id FROM app.org_meta WHERE id = $1 FOR UPDATE", org); err != nil {
		t.Fatal(err)
	}
	if _, err := htx.Exec(ctx, "SELECT id FROM app.sites WHERE id = $1 FOR UPDATE", other.ID); err != nil {
		t.Fatal(err)
	}
	okCtx, cancelOK := context.WithTimeout(ctx, 5*time.Second)
	defer cancelOK()
	var okErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, okErr = st.Publish(store.WithHumanSession(okCtx, false), tn, other.ID, ov.ID)
	}()
	time.Sleep(200 * time.Millisecond)
	if err := htx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if okErr != nil {
		t.Fatalf("uncontrolled publish waited for the site lock and then failed: %v", okErr)
	}
}

func TestIntegration_VersionInsertSeesConcurrentReview(t *testing.T) {
	ctx := context.Background()
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})
	org := "33333333-3333-3333-3333-333333333333"
	owner := "a0000000-0000-0000-0000-000000000009"
	tn := store.Tenant{OrgID: org, UserID: owner}
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", org)
	seedAuthOrg(t, org, "orgc")
	must(t, st.EnsureOrgProvisioned(ctx, tn))
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	site, err := st.CreateSite(ctx, tn, "gamma", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	room, err := st.CreateProjectRoom(ctx, tn, "Review", store.AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org_id', $1, true)", org); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT id FROM app.org_meta WHERE id = $1 FOR UPDATE", org); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT id FROM app.sites WHERE id = $1 FOR UPDATE", site.ID); err != nil {
		t.Fatal(err)
	}
	var insertErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, insertErr = st.CreateSiteVersion(store.WithHumanSession(ctx, false), tn, store.CreateSiteVersionParams{
			SiteID: site.ID, ContentHash: strings.Repeat("22", 32), SizeBytes: 1, Status: "ready",
		})
	}()
	time.Sleep(200 * time.Millisecond)
	if _, err := tx.Exec(ctx, `INSERT INTO app.project_room_sites (org_id, room_id, site_id, created_by) VALUES ($1,$2,$3,$4)`, org, room.ID, site.ID, owner); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	if !errors.Is(insertErr, store.ErrControlledSite) {
		t.Fatalf("version insert err=%v", insertErr)
	}
}
