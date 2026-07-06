//go:build integration

// RLS policy test suite (Phase 4): for EVERY
// app.* tenant table, assert tenant isolation as the non-BYPASSRLS dropway_app role:
//
//   - SELECT  : under org A's GUC, org B's rows are invisible (0 rows).
//   - UPDATE  : under org A's GUC, an UPDATE targeting org B's rows affects 0 rows
//     (RLS USING hides them).
//   - DELETE  : under org A's GUC, a DELETE targeting org B's rows affects 0 rows.
//   - INSERT  : under org A's GUC, inserting a row carrying org B's org_id is
//     rejected by the policy WITH CHECK (insufficient_privilege / 42501).
//   - default-deny : with NO tenant GUC set, the table shows 0 rows.
//
// Table-driven over the full tenant-table set. It runs against real Postgres 16 with
// the goose app migrations applied as the owner; the assertions run on a dedicated
// dropway_app pgx connection (FORCE RLS applies to it). org_meta is special-cased
// (its PK *is* the org id, so its policy compares `id`, and it has no separate
// org_id column to forge on INSERT).
//
// Run with:
//
//	go test -tags integration -run TestIntegration_RLSPolicySuite ./services/api/internal/integration/...

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// rlsOrgA / rlsOrgB are the two tenants the suite isolates. Distinct from the other
// integration tests' org ids so a shared container (if reused) doesn't collide.
const (
	rlsOrgA  = "aaaaaaaa-0000-0000-0000-000000000001"
	rlsOrgB  = "bbbbbbbb-0000-0000-0000-000000000002"
	rlsUserA = "a0000000-0000-0000-0000-0000000000a1"
	rlsUserB = "b0000000-0000-0000-0000-0000000000b1"
)

// rlsTable describes one tenant table and how to seed + probe it under RLS.
type rlsTable struct {
	name string // app.<table>
	// orgCol is the column carrying the tenant id (the RLS predicate column). For
	// org_meta it is "id" (its PK is the org id); for everything else "org_id".
	orgCol string
	// seed returns the INSERT (column list + VALUES) used to plant one row for the
	// given org, run as the OWNER with that org's tenant context. It returns the SQL
	// and args.
	seed func(org string) (sql string, args []any)
	// forgeInsert returns an INSERT the suite runs as dropway_app UNDER org A's GUC
	// but carrying org B's tenant id, to prove WITH CHECK rejects it. nil skips the
	// INSERT check (e.g. org_meta, where forging a different id is the same as the
	// generic check and is covered by org_usage et al.).
	forgeInsert func(otherOrg string) (sql string, args []any)
}

func TestIntegration_RLSPolicySuite(t *testing.T) {
	ctx := context.Background()
	repoRoot := repoRoot(t)

	startPostgres(t)
	applyMigrations(t, repoRoot)

	// Seed both orgs' anchor rows as the owner (org_meta + org_usage), with the
	// parent rows the child tables FK to. Seeding runs as owner with each org's
	// tenant GUC so the WITH CHECK is satisfied while planting cross-tenant rows.
	for _, org := range []string{rlsOrgA, rlsOrgB} {
		// org_meta uses org_only-safe defaults (allow_external_sharing false is fine;
		// we never create a public site here).
		mustExecRaw(t, fmt.Sprintf(`SET app.current_org_id = '%s';
			INSERT INTO app.org_meta (id) VALUES ('%s');
			INSERT INTO app.org_usage (org_id) VALUES ('%s');`, org, org, org))
	}

	tables := rlsTables()

	// Seed one row per table per org (as owner, with that org's tenant context).
	for _, tbl := range tables {
		for _, org := range []string{rlsOrgA, rlsOrgB} {
			sql, args := tbl.seed(org)
			mustExecOwnerTx(t, ctx, org, sql, args)
		}
	}

	// Connect as the non-BYPASSRLS dropway_app role for the assertions.
	conn, err := pgx.Connect(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as dropway_app: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })

	// Sanity: confirm we are really the non-BYPASSRLS runtime role.
	var role string
	var bypass bool
	if err := conn.QueryRow(ctx,
		`SELECT current_user, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&role, &bypass); err != nil {
		t.Fatal(err)
	}
	if role != "dropway_app" || bypass {
		t.Fatalf("expected non-BYPASSRLS dropway_app, got %s (bypassrls=%v)", role, bypass)
	}

	for _, tbl := range tables {
		t.Run(tbl.name, func(t *testing.T) {
			// --- Under org A's GUC: org B's rows are invisible / immutable. ---
			setGUC(t, ctx, conn, rlsOrgA)

			// SELECT: org B's rows invisible.
			if n := countWhere(t, ctx, conn, tbl.name, tbl.orgCol, rlsOrgB); n != 0 {
				t.Errorf("SELECT LEAK: org A sees %d of org B's rows in %s", n, tbl.name)
			}
			// Org A sees exactly its own seeded row.
			if n := countWhere(t, ctx, conn, tbl.name, tbl.orgCol, rlsOrgA); n != 1 {
				t.Errorf("org A should see its 1 row in %s, saw %d", tbl.name, n)
			}

			// UPDATE: targeting org B's rows affects 0 (RLS USING hides them).
			if n := execRows(t, ctx, conn,
				fmt.Sprintf(`UPDATE %s SET %s = %s WHERE %s = $1`, tbl.name, tbl.orgCol, tbl.orgCol, tbl.orgCol),
				rlsOrgB); n != 0 {
				t.Errorf("UPDATE LEAK: org A updated %d of org B's rows in %s", n, tbl.name)
			}

			// DELETE: targeting org B's rows affects 0.
			if n := execRows(t, ctx, conn,
				fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, tbl.name, tbl.orgCol),
				rlsOrgB); n != 0 {
				t.Errorf("DELETE LEAK: org A deleted %d of org B's rows in %s", n, tbl.name)
			}

			// INSERT WITH CHECK: a row carrying org B's id is rejected under org A.
			if tbl.forgeInsert != nil {
				sql, args := tbl.forgeInsert(rlsOrgB)
				_, err := conn.Exec(ctx, sql, args...)
				if !isInsufficientPrivilege(err) {
					t.Errorf("WITH CHECK LEAK: org A inserted an org B row into %s (err=%v)", tbl.name, err)
				}
			}

			// --- Default-deny: with NO tenant GUC, nothing is visible. ---
			resetGUC(t, ctx, conn)
			if n := countAll(t, ctx, conn, tbl.name); n != 0 {
				t.Errorf("DEFAULT-DENY LEAK: %s shows %d rows with no tenant context", tbl.name, n)
			}
		})
	}

	// =======================================================================
	// app.all_org_ids() is OPS-ONLY (migration 0009): a normal request running as
	// dropway_app must NOT be able to enumerate every org id, even though the role
	// holds EXECUTE — the body is gated on the app.ops_mode GUC the DR/GC path sets.
	// =======================================================================
	t.Run("all_org_ids ops-mode gate", func(t *testing.T) {
		resetGUC(t, ctx, conn)

		// Without ops mode → denied (insufficient_privilege). This is the request path:
		// a normal request can never enumerate cross-tenant org ids.
		var dummy string
		err := conn.QueryRow(ctx, `SELECT id::text FROM app.all_org_ids() LIMIT 1`).Scan(&dummy)
		if !isInsufficientPrivilege(err) {
			t.Fatalf("all_org_ids() WITHOUT ops mode must be denied (insufficient_privilege), got err=%v", err)
		}

		// With ops mode set for the tx (the DR/GC escalation) → it returns every org.
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT set_config('app.ops_mode', '1', true)`); err != nil {
			t.Fatalf("set ops mode: %v", err)
		}
		rows, err := tx.Query(ctx, `SELECT id FROM app.all_org_ids()`)
		if err != nil {
			t.Fatalf("all_org_ids() WITH ops mode should succeed: %v", err)
		}
		var n int
		for rows.Next() {
			n++
		}
		rows.Close()
		if n < 2 {
			t.Fatalf("ops-mode all_org_ids() should enumerate >= 2 seeded orgs, got %d", n)
		}
		t.Logf("PASS: all_org_ids() denied without ops mode, returns %d orgs with ops mode", n)
	})

	t.Log("PASS: RLS cross-tenant deny (SELECT/UPDATE/DELETE + WITH CHECK on INSERT) and default-deny hold for every app.* tenant table")
}

// rlsTables is the table-driven set of every app.* tenant table.
func rlsTables() []rlsTable {
	// Shared child ids so FKs resolve; per-org so seeding two orgs doesn't collide.
	siteID := func(org string) string {
		if org == rlsOrgA {
			return "11111111-0000-0000-0000-0000000000a1"
		}
		return "11111111-0000-0000-0000-0000000000b1"
	}
	verID := func(org string) string {
		if org == rlsOrgA {
			return "22222222-0000-0000-0000-0000000000a1"
		}
		return "22222222-0000-0000-0000-0000000000b1"
	}
	skillID := func(org string) string {
		if org == rlsOrgA {
			return "33333333-0000-0000-0000-0000000000a1"
		}
		return "33333333-0000-0000-0000-0000000000b1"
	}
	skillFolderID := func(org string) string {
		if org == rlsOrgA {
			return "44444444-0000-0000-0000-0000000000a1"
		}
		return "44444444-0000-0000-0000-0000000000b1"
	}

	return []rlsTable{
		{
			name:   "app.org_meta",
			orgCol: "id",
			seed:   func(org string) (string, []any) { return "", nil }, // seeded in setup
			// org_meta has no separate org_id to forge; the generic WITH CHECK is
			// covered by the org_usage/sites cases. We still exercise SELECT/UPDATE/
			// DELETE isolation on it below (forgeInsert nil).
			forgeInsert: nil,
		},
		{
			name:        "app.org_usage",
			orgCol:      "org_id",
			seed:        func(org string) (string, []any) { return "", nil }, // seeded in setup
			forgeInsert: nil,                                                 // PK is org_id (one row already exists for B); covered by sites
		},
		{
			name:   "app.sites",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.sites (id, org_id, slug, owner_user_id, access_mode)
						VALUES ($1, $2, $3, $4, 'org_only')`,
					[]any{siteID(org), org, "site-" + org[:4], ownerUser(org)}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.sites (org_id, slug, owner_user_id, access_mode)
						VALUES ($1, 'sneaky', $2, 'org_only')`,
					[]any{other, ownerUser(other)}
			},
		},
		{
			name:   "app.site_versions",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.site_versions (id, org_id, site_id, version_no, status, r2_prefix, content_hash, size_bytes, created_by)
						VALUES ($1, $2, $3, 1, 'ready', 'manifests/x', $4, 1, $5)`,
					[]any{verID(org), org, siteID(org), "hash-" + org[:4], ownerUser(org)}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.site_versions (org_id, site_id, version_no, status, r2_prefix, content_hash, size_bytes, created_by)
						VALUES ($1, $2, 99, 'ready', 'manifests/x', 'sneaky-hash', 1, $3)`,
					[]any{other, siteID(other), ownerUser(other)}
			},
		},
		{
			name:   "app.site_access_policy",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.site_access_policy (site_id, org_id, mode)
						VALUES ($1, $2, 'org_only')`,
					[]any{siteID(org), org}
			},
			forgeInsert: func(other string) (string, []any) {
				// site_id PK collides with the existing B row, so use a fresh fake
				// site_id; the WITH CHECK on org_id fires before any FK check matters.
				return `INSERT INTO app.site_access_policy (site_id, org_id, mode)
						VALUES (gen_random_uuid(), $1, 'org_only')`,
					[]any{other}
			},
		},
		{
			name:   "app.allowlist_entries",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.allowlist_entries (org_id, site_id, email, is_external)
						VALUES ($1, $2, $3, false)`,
					[]any{org, siteID(org), "a@" + org[:4] + ".com"}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.allowlist_entries (org_id, site_id, email, is_external)
						VALUES ($1, $2, 'sneaky@x.com', false)`,
					[]any{other, siteID(other)}
			},
		},
		{
			name:   "app.domains",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.domains (org_id, site_id, hostname)
						VALUES ($1, $2, $3)`,
					[]any{org, siteID(org), "host-" + org[:4] + ".example.com"}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.domains (org_id, site_id, hostname)
						VALUES ($1, $2, 'sneaky.example.com')`,
					[]any{other, siteID(other)}
			},
		},
		{
			name:   "app.deploy_tokens",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.deploy_tokens (org_id, token_hash)
						VALUES ($1, $2)`,
					[]any{org, "tokhash-" + org[:6]}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.deploy_tokens (org_id, token_hash)
						VALUES ($1, 'sneaky-tokhash')`,
					[]any{other}
			},
		},
		{
			name:   "app.host_routes",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.host_routes (host, org_id, site_id)
						VALUES ($1, $2, $3)`,
					[]any{"route-" + org[:4] + ".dropwaycontent.com", org, siteID(org)}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.host_routes (host, org_id, site_id)
						VALUES ('sneaky.dropwaycontent.com', $1, $2)`,
					[]any{other, siteID(other)}
			},
		},
		{
			name:   "app.audit_log",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.audit_log (org_id, action, target)
						VALUES ($1, 'test.seed', $2)`,
					[]any{org, "site:" + siteID(org)}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.audit_log (org_id, action, target)
						VALUES ($1, 'sneaky', 'x')`,
					[]any{other}
			},
		},
		{
			name:   "app.skills",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.skills (id, org_id, slug, owner_user_id)
						VALUES ($1, $2, $3, $4)`,
					[]any{skillID(org), org, "skill-" + org[:4], ownerUser(org)}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.skills (org_id, slug, owner_user_id)
						VALUES ($1, 'sneaky-skill', $2)`,
					[]any{other, ownerUser(other)}
			},
		},
		{
			name:   "app.skill_versions",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.skill_versions (org_id, skill_id, version_no, status, content_hash, size_bytes, created_by)
						VALUES ($1, $2, 1, 'ready', $3, 1, $4)`,
					[]any{org, skillID(org), "skillhash-" + org[:4], ownerUser(org)}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.skill_versions (org_id, skill_id, version_no, status, content_hash, size_bytes, created_by)
						VALUES ($1, $2, 99, 'ready', 'sneaky-skillhash', 1, $3)`,
					[]any{other, skillID(other), ownerUser(other)}
			},
		},
		{
			name:   "app.skill_folders",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.skill_folders (id, org_id, slug, title)
						VALUES ($1, $2, $3, 'Folder')`,
					[]any{skillFolderID(org), org, "folder-" + org[:4]}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.skill_folders (org_id, slug, title)
						VALUES ($1, 'sneaky-folder', 'Sneaky')`,
					[]any{other}
			},
		},
		{
			name:   "app.skill_folder_items",
			orgCol: "org_id",
			seed: func(org string) (string, []any) {
				return `INSERT INTO app.skill_folder_items (org_id, folder_id, skill_id, is_preset, added_by)
						VALUES ($1, $2, $3, true, $4)`,
					[]any{org, skillFolderID(org), skillID(org), ownerUser(org)}
			},
			forgeInsert: func(other string) (string, []any) {
				return `INSERT INTO app.skill_folder_items (org_id, folder_id, skill_id, is_preset, added_by)
						VALUES ($1, $2, $3, false, $4)`,
					[]any{other, skillFolderID(other), skillID(other), ownerUser(other)}
			},
		},
	}
}

// ownerUser returns the seed owner user id for an org.
func ownerUser(org string) string {
	if org == rlsOrgA {
		return rlsUserA
	}
	return rlsUserB
}

// ---------------------------------------------------------------------------
// helpers (dropway_app conn)
// ---------------------------------------------------------------------------

func setGUC(t *testing.T, ctx context.Context, conn *pgx.Conn, org string) {
	t.Helper()
	if _, err := conn.Exec(ctx, `SELECT set_config('app.current_org_id', $1, false)`, org); err != nil {
		t.Fatalf("set GUC: %v", err)
	}
}

func resetGUC(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	// Set to empty: the policies NULLIF('','')::uuid → NULL → default deny.
	if _, err := conn.Exec(ctx, `SELECT set_config('app.current_org_id', '', false)`); err != nil {
		t.Fatalf("reset GUC: %v", err)
	}
}

func countWhere(t *testing.T, ctx context.Context, conn *pgx.Conn, table, col, val string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(ctx,
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, table, col), val).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func countAll(t *testing.T, ctx context.Context, conn *pgx.Conn, table string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func execRows(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string, args ...any) int64 {
	t.Helper()
	tag, err := conn.Exec(ctx, sql, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
	return tag.RowsAffected()
}

// isInsufficientPrivilege reports a Postgres 42501 (RLS WITH CHECK rejection).
func isInsufficientPrivilege(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42501"
	}
	return false
}

// mustExecOwnerTx runs a seed INSERT as the OWNER inside a tx with the org's tenant
// GUC set, so the WITH CHECK is satisfied while planting a cross-tenant row. Empty
// sql is a no-op (the table is seeded in setup).
func mustExecOwnerTx(t *testing.T, ctx context.Context, org, sql string, args []any) {
	t.Helper()
	if sql == "" {
		return
	}
	conn, err := pgx.Connect(ctx, ownerDSN)
	if err != nil {
		t.Fatalf("owner connect: %v", err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', $1, true)`, org); err != nil {
		t.Fatalf("owner set GUC: %v", err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("owner seed %q: %v", sql, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("owner commit: %v", err)
	}
}
