//go:build integration

package integration

import (
	"context"
	"os/exec"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_ProjectRoomDefaultMigration(t *testing.T) {
	startPostgres(t)
	root := repoRoot(t)
	applyMigrations(t, root)
	migrate := func(direction string, args ...string) {
		t.Helper()
		cmd := exec.Command("go", append([]string{"run", "github.com/pressly/goose/v3/cmd/goose@v3.22.0", "-dir", root + "/db/migrations/app", "postgres", ownerDSN, direction}, args...)...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("migration %s: %v\n%s", direction, err, output)
		}
	}
	migrate("down-to", "25")
	untouched := "11111111-1111-1111-1111-111111111111"
	explicitOff := "22222222-2222-2222-2222-222222222222"
	usedRooms := "33333333-3333-3333-3333-333333333333"
	explicitOn := "44444444-4444-4444-4444-444444444444"
	newOrg := "55555555-5555-5555-5555-555555555555"
	for _, org := range []string{untouched, explicitOff, usedRooms} {
		mustExec(t, `INSERT INTO app.org_meta(id) VALUES($1)`, org)
	}
	mustExec(t, `INSERT INTO app.org_meta(id,project_rooms_enabled) VALUES($1,true)`, explicitOn)
	mustExec(t, `INSERT INTO app.audit_log(org_id,action,metadata) VALUES($1,'org.project_rooms_toggle','{"enabled":false}')`, explicitOff)
	// Even if old audit events were pruned, don't reactivate an existing project.
	mustExec(t, `INSERT INTO app.project_rooms(org_id,name,owner_user_id) VALUES($1,'Existing project','a0000000-0000-0000-0000-000000000001')`, usedRooms)
	migrate("up")
	mustExec(t, `INSERT INTO app.org_meta(id) VALUES($1)`, newOrg)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, ownerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	assertEnabled := func(org string, want bool) {
		t.Helper()
		var enabled bool
		if err := pool.QueryRow(ctx, `SELECT project_rooms_enabled FROM app.org_meta WHERE id=$1`, org).Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != want {
			t.Fatalf("org %s enabled=%v, want %v", org, enabled, want)
		}
	}
	assertEnabled(untouched, true)
	assertEnabled(explicitOff, false)
	assertEnabled(usedRooms, false)
	assertEnabled(explicitOn, true)
	assertEnabled(newOrg, true)
	// Downgrade restores the old default, without disabling projects users may
	// have created or resetting administrators' choices after the upgrade.
	migrate("down-to", "25")
	assertEnabled(untouched, true)
	assertEnabled(newOrg, true)
	assertEnabled(explicitOff, false)
	migrate("up")
}
