//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_SchemaPreflightRollingUpgrade(t *testing.T) {
	startPostgres(t)
	root := repoRoot(t)
	applyMigrations(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := store.CheckSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// The real release command must work with only database configuration, even
	// if unrelated runtime settings are invalid. It must exit rather than serve.
	command := exec.CommandContext(ctx, "go", "run", "./services/api/cmd/api", "--check-schema")
	command.Dir = root
	command.Env = append(os.Environ(), "DATABASE_URL="+appDSN, "ROOM_CREDENTIAL_MASTER_KEY=not-a-key", "PORT=invalid")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Database schema is compatible") {
		t.Fatalf("release check: %v %s", err, output)
	}
	// LIMIT0 metadata checks alone must not mask a missing function EXECUTE grant.
	mustExec(t, `REVOKE EXECUTE ON FUNCTION app.resolve_api_key(text) FROM dropway_app`)
	if err := store.CheckSchema(ctx, pool); err == nil {
		t.Fatal("missing resolver grant accepted")
	}
	mustExec(t, `GRANT EXECUTE ON FUNCTION app.resolve_api_key(text) TO dropway_app`)
	if err := store.CheckSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Both the immediately preceding schema and the pre-room schema still serve
	// old binaries, but cannot accept this release without invitation lookup.
	for _, version := range []string{"28", "18"} {
		goose := exec.CommandContext(ctx, "go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.22.0", "-dir", root+"/db/migrations/app", "postgres", ownerDSN, "down-to", version)
		goose.Dir = root
		if out, err := goose.CombinedOutput(); err != nil {
			t.Fatalf("down-to%s: %v %s", version, err, out)
		}
		if err := store.CheckSchema(ctx, pool); err == nil || !strings.Contains(err.Error(), "migrations through 0029") {
			t.Fatalf("schema %s should fail with migration hint, got %v", version, err)
		}
		// The existing API's seven-column lookup remains valid during rollback.
		rows, err := pool.Query(ctx, `SELECT id,org_id,created_by,expires_at,revoked_at,org_status,api_keys_enabled FROM app.resolve_api_key('no-key')`)
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	applyMigrations(t, root)
	if err := store.CheckSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
}
