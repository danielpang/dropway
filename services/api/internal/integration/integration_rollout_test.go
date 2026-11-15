//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The previous serving binary must keep authenticating API keys after the
// migrations land but before the replacement image becomes ready.
func TestIntegration_RolloutPreviousAPIKeyProjection(t *testing.T) {
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	ctx := context.Background()
	org := "11111111-1111-1111-1111-111111111111"
	user := "a0000000-0000-0000-0000-000000000001"
	mustExec(t, `INSERT INTO app.org_meta(id,api_keys_enabled) VALUES($1,true)`, org)
	mustExec(t, `INSERT INTO app.api_keys(org_id,created_by,name,key_hash,key_prefix) VALUES($1,$2,'existing key','rollout-hash','dw_test')`, org, user)
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var id, gotOrg, creator, status string
	var expires, revoked *time.Time
	var enabled bool
	// Exact projection used by the pre-project-room API, through the non-owner
	// runtime role and without a tenant context (the auth-boundary contract).
	err = pool.QueryRow(ctx, `SELECT id,org_id,created_by,expires_at,revoked_at,org_status,api_keys_enabled FROM app.resolve_api_key($1)`, "rollout-hash").Scan(&id, &gotOrg, &creator, &expires, &revoked, &status, &enabled)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || gotOrg != org || creator != user || !enabled || revoked != nil {
		t.Fatalf("previous API projection changed: org=%s creator=%s enabled=%v", gotOrg, creator, enabled)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('app.current_org_id',$1,true)", org); err != nil {
		t.Fatal(err)
	}
	var roomsEnabled bool
	err = tx.QueryRow(ctx, `SELECT project_rooms_enabled FROM app.org_meta WHERE id=$1`, org).Scan(&roomsEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if !roomsEnabled {
		t.Fatal("new organizations should have project rooms enabled by default")
	}
}
