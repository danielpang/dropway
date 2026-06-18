//go:build cloud && integration

// Cloud billing integration test (PROPRIETARY, cloud-only — docs/ARCHITECTURE.md
// §9/§13 rows 9, 9b). It is gated behind BOTH the `cloud` and `integration` build
// tags so neither the default `go test ./...` nor the OSS integration test ever
// compiles it. Run it with:
//
//	go test -tags 'cloud integration' -run TestIntegration_CloudBilling \
//	    ./services/api/internal/integration/...
//
// It stands up fresh Postgres 16 + MinIO containers via `docker run`, applies BOTH
// the app migrations (db/migrations/app) AND the cloud billing migration
// (db/migrations/billing) as the owner role, seeds a synthetic Better Auth
// identity.member table, then drives the REAL production billing path:
//
//   - the cloud quota provider (Free 10 sites/org) on the FSL store.CreateSite —
//     11th site → 402 (cloud cap), proving the hard cap;
//   - a SIGNED checkout.session.completed webhook (real RealSignatureVerifier +
//     real *billing.Handler over the SAME non-BYPASSRLS dropway_app pool) sets
//     plan_tier=business → asserts billing.subscriptions.plan_tier='business' AND
//     app.org_meta.plan_tier='business' (the org_meta write is RLS-permitted only
//     because the BillingStore sets app.current_org_id to the EVENT'S org);
//   - the SAME user can now create the 11th..100th site — paying RAISED the cap;
//   - replaying the same event.id → ignored (exactly one upsert);
//   - a FORGED signature → 400 and NO DB write;
//   - customer.subscription.deleted → org_meta.plan_tier='free' + org_status set,
//     with NO data deleted (read-only downgrade);
//   - CONCURRENCY: N goroutines create sites for a capped Free org → exactly the
//     cap succeed (the per-org advisory lock in store.CreateSite).
//
// Containers are torn down on completion (even on failure) via t.Cleanup.
//
// It lives in the `integration` package (not cloud/billing) because it imports
// services/api/internal/store, which is internal and only importable from within
// services/api/... — and it carries the `cloud` tag so OSS integration runs never
// compile it. All identifiers are cb-prefixed to avoid colliding with the FSL
// integration files in this same package.
package integration

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v84/webhook"

	cloudbilling "github.com/danielpang/dropway/cloud/billing"
	cloudquota "github.com/danielpang/dropway/cloud/quota"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

const (
	cbPort     = "55433" // distinct from the FSL integration test's 55432
	cbPgImage  = "postgres:16"
	cbOwnerDSN = "postgres://postgres:postgres@127.0.0.1:" + cbPort + "/dropway?sslmode=disable"
	cbAppPw    = "dropway_app_cb_pw"
	cbAppDSN   = "postgres://dropway_app:" + cbAppPw + "@127.0.0.1:" + cbPort + "/dropway?sslmode=disable"
	cbWhSecret = "whsec_integration_secret"
	cbPriceBiz = "price_business_test"
	cbPriceEnt = "price_enterprise_test"
)

func TestIntegration_CloudBilling(t *testing.T) {
	ctx := context.Background()
	root := cbRepoRoot(t)

	cbStartPostgres(t)
	cbApplyMigrations(t, root) // applies BOTH app AND billing migrations
	cbSeedAuthMember(t)

	pool, err := pgxpool.New(ctx, cbAppDSN)
	if err != nil {
		t.Fatalf("connect as dropway_app: %v", err)
	}
	t.Cleanup(pool.Close)

	// The cloud quota provider gives the FSL store its hard-cap bands; the store
	// owns the race-safe advisory-lock mechanics.
	qp := cloudquota.NewProvider(cloudquota.DashboardURLBuilder{DashboardBaseURL: "https://app.dropway.dev"}, false)
	st := store.New(pool, qp)

	// The production billing persistence + webhook handler over the SAME pool. The
	// in-memory Local projection writer stands in for Cloudflare KV so we can assert
	// the edge org_status flag is projected on the real webhook path (FIX 2): the DB
	// is the source of truth, the KV flag is what makes suspension block at the edge.
	orgStatusKV := projection.NewLocal()
	bstore := cloudbilling.NewStore(pool).WithOrgStatusWriter(orgStatusKV)
	prices := cloudbilling.NewPriceMap(cbPriceBiz, cbPriceEnt)
	verifier := cloudbilling.NewRealSignatureVerifier(cbWhSecret, prices)
	webhookHandler := cloudbilling.NewHandler(verifier, bstore, nil)

	orgID := "11111111-1111-1111-1111-111111111111"
	userID := "a0000000-0000-0000-0000-000000000001"
	tenant := store.Tenant{OrgID: orgID, UserID: userID}

	// Provision a Free org that allows external sharing (so default-public sites are
	// permitted by the 0004 trigger) — owner is non-BYPASSRLS-safe via tenant GUC.
	cbExecOwner(t, "SET app.current_org_id = '"+orgID+"'; INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ('"+orgID+"', true);")
	cbExecOwner(t, "SET app.current_org_id = '"+orgID+"'; INSERT INTO app.org_usage (org_id) VALUES ('"+orgID+"');")
	cbSeedAuthOrg(t, orgID, "orga")
	cbSeedAuthMember(t) // empty identity.member must exist before PreflightMembers (cloud always has Better Auth migrated)
	if err := st.EnsureOrgProvisioned(ctx, tenant); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// -----------------------------------------------------------------------
	// 1. FREE CAP: 10 sites OK, 11th → 402 (cloud cap).
	// -----------------------------------------------------------------------
	for i := 1; i <= 10; i++ {
		if _, err := st.CreateSite(ctx, tenant, fmt.Sprintf("free-%d", i), projection.AccessPublic); err != nil {
			t.Fatalf("free site %d should succeed: %v", i, err)
		}
	}
	_, err = st.CreateSite(ctx, tenant, "free-11", projection.AccessPublic)
	if ex, ok := quota.AsExceeded(err); !ok {
		t.Fatalf("11th site on Free must be 402 quota_exceeded, got %v", err)
	} else if ex.PlanTier != "free" || ex.NextTier != "business" || ex.Max != 10 {
		t.Fatalf("402 payload wrong: %+v", ex)
	}
	t.Log("PASS: Free org capped at 10 sites/org (11th → 402)")

	// -----------------------------------------------------------------------
	// 1b. SEATS ARE FREE (docs/pricing.md): members are unlimited on every plan, so
	// the preflight passes with 0 members AND after seeding many. The seam stays
	// wired (so seat policy could be re-tightened in the cloud provider alone), but
	// the cloud provider returns nil for ResourceMemberPerOrg today.
	// -----------------------------------------------------------------------
	if err := st.PreflightMembers(ctx, tenant); err != nil {
		t.Fatalf("members preflight with 0 members should pass: %v", err)
	}
	for i := 1; i <= 9; i++ {
		cbExecOwner(t, fmt.Sprintf(
			`INSERT INTO identity.member ("organizationId","userId","role") VALUES ('%s','c0000000-0000-0000-0000-00000000000%d','member');`,
			orgID, i))
	}
	if err := st.PreflightMembers(ctx, tenant); err != nil {
		t.Fatalf("seat-free: members preflight with 9 members on Free must still pass, got %v", err)
	}
	t.Log("PASS: seats are free — members unlimited on Free (9 members → preflight still passes)")

	// -----------------------------------------------------------------------
	// 2. SIGNED checkout.session.completed → plan_tier=business in BOTH tables.
	// -----------------------------------------------------------------------
	checkoutPayload := []byte(`{
		"id":"evt_checkout_business",
		"object":"event",
		"type":"checkout.session.completed",
		"data":{"object":{
			"object":"checkout.session",
			"client_reference_id":"` + orgID + `",
			"customer":"cus_business_1",
			"subscription":"sub_business_1",
			"metadata":{"org_id":"` + orgID + `","target_tier":"business"}
		}}
	}`)
	if rr := cbPostWebhook(t, webhookHandler, checkoutPayload, cbSign(checkoutPayload, cbWhSecret)); rr.Code != http.StatusOK {
		t.Fatalf("signed webhook status=%d body=%s", rr.Code, rr.Body.String())
	}

	if got := cbScanText(t, pool, "SELECT plan_tier FROM billing.subscriptions WHERE org_id=$1", orgID); got != "business" {
		t.Fatalf("billing.subscriptions.plan_tier=%q, want business", got)
	}
	// Read app.org_meta.plan_tier via the PRODUCTION read path (ReadPlanTier sets
	// the org GUC so the RLS-scoped read is permitted) — proving the system write
	// landed in org_meta, not just billing.subscriptions.
	if got, err := bstore.ReadPlanTier(ctx, orgID); err != nil || got != cloudbilling.TierBusiness {
		t.Fatalf("app.org_meta.plan_tier=%q err=%v, want business (the RLS-scoped system write)", got, err)
	}
	if got := cbScanText(t, pool, "SELECT org_status FROM billing.subscriptions WHERE org_id=$1", orgID); got != "active" {
		t.Fatalf("org_status=%q, want active", got)
	}
	// EDGE PROJECTION (FIX 2): an active subscription clears the org_status KV flag
	// (active = served), so no blocking entry should be present for the org.
	if status, blocked := orgStatusKV.GetOrgStatus(orgID); blocked {
		t.Fatalf("after activation the edge org_status flag must be CLEARED, got %q", status)
	}
	t.Log("PASS: signed webhook set plan_tier=business in billing.subscriptions AND app.org_meta; edge org_status cleared (active)")

	// -----------------------------------------------------------------------
	// 2b. H7: customer.subscription.created carries the seat quantity; a later
	// out-of-order/retried checkout.session.completed carries seats=0 and must NOT
	// zero the billed seat count.
	// -----------------------------------------------------------------------
	subSeatsPayload := []byte(`{
		"id":"evt_sub_seats_5",
		"object":"event",
		"type":"customer.subscription.created",
		"data":{"object":{
			"object":"subscription",
			"id":"sub_business_1",
			"customer":"cus_business_1",
			"status":"active",
			"metadata":{"org_id":"` + orgID + `"},
			"items":{"data":[{"quantity":5,"price":{"id":"` + cbPriceBiz + `"}}]}
		}}
	}`)
	if rr := cbPostWebhook(t, webhookHandler, subSeatsPayload, cbSign(subSeatsPayload, cbWhSecret)); rr.Code != http.StatusOK {
		t.Fatalf("subscription.created seats webhook status=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := cbScanInt(t, pool, "SELECT seats FROM billing.subscriptions WHERE org_id=$1", orgID); n != 5 {
		t.Fatalf("seats=%d after subscription.created, want 5", n)
	}
	checkoutSeats0 := []byte(`{
		"id":"evt_checkout_seats0",
		"object":"event",
		"type":"checkout.session.completed",
		"data":{"object":{
			"object":"checkout.session",
			"client_reference_id":"` + orgID + `",
			"customer":"cus_business_1",
			"subscription":"sub_business_1",
			"metadata":{"org_id":"` + orgID + `","target_tier":"business"}
		}}
	}`)
	if rr := cbPostWebhook(t, webhookHandler, checkoutSeats0, cbSign(checkoutSeats0, cbWhSecret)); rr.Code != http.StatusOK {
		t.Fatalf("seats=0 checkout webhook status=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := cbScanInt(t, pool, "SELECT seats FROM billing.subscriptions WHERE org_id=$1", orgID); n != 5 {
		t.Fatalf("H7: seats=%d after a seats=0 checkout event, want 5 (must not be zeroed)", n)
	}
	t.Log("PASS: H7 — a seats=0 checkout event did not zero the billed seat count")

	// -----------------------------------------------------------------------
	// 3. PAYING RAISED THE CAP: the SAME user can now create the 11th..100th site.
	// -----------------------------------------------------------------------
	for i := 11; i <= 100; i++ {
		if _, err := st.CreateSite(ctx, tenant, fmt.Sprintf("biz-%d", i), projection.AccessPublic); err != nil {
			t.Fatalf("business site %d should succeed after upgrade: %v", i, err)
		}
	}
	// Business cap is 100; the 101st must now 402 with next_tier=enterprise.
	_, err = st.CreateSite(ctx, tenant, "biz-101", projection.AccessPublic)
	if ex, ok := quota.AsExceeded(err); !ok {
		t.Fatalf("101st site on Business must be 402, got %v", err)
	} else if ex.PlanTier != "business" || ex.NextTier != "enterprise" || ex.Max != 100 {
		t.Fatalf("business 402 payload wrong: %+v", ex)
	}
	t.Log("PASS: paying raised the cap — 100 sites now allowed; 101st → 402{next:enterprise}")

	// -----------------------------------------------------------------------
	// 4. REPLAY the same event.id → ignored (exactly one upsert / one ledger row).
	// -----------------------------------------------------------------------
	if rr := cbPostWebhook(t, webhookHandler, checkoutPayload, cbSign(checkoutPayload, cbWhSecret)); rr.Code != http.StatusOK {
		t.Fatalf("replay status=%d", rr.Code)
	}
	if n := cbScanInt(t, pool, "SELECT count(*) FROM billing.processed_stripe_events WHERE event_id=$1", "evt_checkout_business"); n != 1 {
		t.Fatalf("dedupe ledger has %d rows for the replayed event, want 1", n)
	}
	t.Log("PASS: replayed event.id ignored (idempotent dedupe)")

	// -----------------------------------------------------------------------
	// 5. FORGED signature → 400, NO DB write (plan stays business; no new ledger row).
	// -----------------------------------------------------------------------
	forged := []byte(`{"id":"evt_forged","object":"event","type":"checkout.session.completed","data":{"object":{"object":"checkout.session","client_reference_id":"` + orgID + `","metadata":{"org_id":"` + orgID + `","target_tier":"enterprise"}}}}`)
	if rr := cbPostWebhook(t, webhookHandler, forged, cbSign(forged, "whsec_attacker")); rr.Code != http.StatusBadRequest {
		t.Fatalf("forged signature must be 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got, err := bstore.ReadPlanTier(ctx, orgID); err != nil || got != cloudbilling.TierBusiness {
		t.Fatalf("forged event must NOT mutate plan_tier; got %q err=%v", got, err)
	}
	if n := cbScanInt(t, pool, "SELECT count(*) FROM billing.processed_stripe_events WHERE event_id=$1", "evt_forged"); n != 0 {
		t.Fatalf("forged event must NOT be recorded in the dedupe ledger; got %d", n)
	}
	t.Log("PASS: forged signature → 400, no DB write")

	// -----------------------------------------------------------------------
	// 6. customer.subscription.deleted → Free + org_status, NO data deleted.
	//    The org has > 10 sites for the user, so the downgrade is over_limit.
	// -----------------------------------------------------------------------
	sitesBefore := cbScanIntOrg(t, pool, orgID, "SELECT count(*) FROM app.sites WHERE org_id=$1", orgID)
	delPayload := []byte(`{
		"id":"evt_sub_deleted",
		"object":"event",
		"type":"customer.subscription.deleted",
		"data":{"object":{
			"object":"subscription",
			"id":"sub_business_1",
			"customer":"cus_business_1",
			"status":"canceled",
			"metadata":{"org_id":"` + orgID + `"},
			"items":{"data":[{"quantity":1,"price":{"id":"` + cbPriceBiz + `"}}]}
		}}
	}`)
	if rr := cbPostWebhook(t, webhookHandler, delPayload, cbSign(delPayload, cbWhSecret)); rr.Code != http.StatusOK {
		t.Fatalf("subscription.deleted status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got, err := bstore.ReadPlanTier(ctx, orgID); err != nil || got != cloudbilling.TierFree {
		t.Fatalf("after deletion org_meta.plan_tier=%q err=%v, want free", got, err)
	}
	if got := cbScanText(t, pool, "SELECT plan_tier FROM billing.subscriptions WHERE org_id=$1", orgID); got != "free" {
		t.Fatalf("after deletion subscriptions.plan_tier=%q, want free", got)
	}
	if got := cbScanText(t, pool, "SELECT org_status FROM billing.subscriptions WHERE org_id=$1", orgID); got != "over_limit" {
		t.Fatalf("after deletion org_status=%q, want over_limit (user has >10 sites)", got)
	}
	// EDGE PROJECTION (FIX 2): the cancel pushed the org over the Free caps, so the
	// webhook must have projected org_status="over_limit" to the edge KV — THIS is
	// what makes the suspension actually block at the serving Worker (without it the
	// suspension is dead/fails open). Best-effort but must have landed here.
	if status, blocked := orgStatusKV.GetOrgStatus(orgID); !blocked || status != "over_limit" {
		t.Fatalf("after cancel the edge org_status flag must be over_limit (blocked=%v status=%q)", blocked, status)
	}
	if got := cbScanText(t, pool, "SELECT status FROM billing.subscriptions WHERE org_id=$1", orgID); got != "canceled" {
		t.Fatalf("after deletion subscriptions.status=%q, want canceled", got)
	}
	sitesAfter := cbScanIntOrg(t, pool, orgID, "SELECT count(*) FROM app.sites WHERE org_id=$1", orgID)
	if sitesAfter != sitesBefore || sitesAfter == 0 {
		t.Fatalf("DATA LOSS: sites %d → %d on downgrade (must be read-only, never destructive)", sitesBefore, sitesAfter)
	}
	t.Logf("PASS: subscription.deleted → Free + org_status=over_limit, %d sites preserved (no data deleted)", sitesAfter)

	// -----------------------------------------------------------------------
	// 7. CONCURRENCY: N goroutines create sites for a fresh capped Free org →
	//    exactly the cap (10) succeed (per-org advisory lock serializes).
	// -----------------------------------------------------------------------
	concOrg := "22222222-2222-2222-2222-222222222222"
	concUser := "b0000000-0000-0000-0000-000000000002"
	concTenant := store.Tenant{OrgID: concOrg, UserID: concUser}
	cbExecOwner(t, "SET app.current_org_id = '"+concOrg+"'; INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ('"+concOrg+"', true);")
	cbSeedAuthOrg(t, concOrg, "orgb")
	cbExecOwner(t, "SET app.current_org_id = '"+concOrg+"'; INSERT INTO app.org_usage (org_id) VALUES ('"+concOrg+"');")
	if err := st.EnsureOrgProvisioned(ctx, concTenant); err != nil {
		t.Fatalf("provision conc org: %v", err)
	}

	const goroutines = 30
	var success, quotaRejects int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			_, err := st.CreateSite(ctx, concTenant, fmt.Sprintf("conc-%d", n), projection.AccessPublic)
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case isQuota(err):
				atomic.AddInt64(&quotaRejects, 1)
			default:
				t.Errorf("unexpected create error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if success != 10 {
		t.Fatalf("CONCURRENCY: exactly 10 creates must succeed at the Free cap, got %d (rejects=%d)", success, quotaRejects)
	}
	dbCount := cbScanIntOrg(t, pool, concOrg, "SELECT count(*) FROM app.sites WHERE org_id=$1", concOrg)
	if dbCount != 10 {
		t.Fatalf("CONCURRENCY: DB has %d sites for the capped org, want exactly 10", dbCount)
	}
	t.Logf("PASS: %d concurrent creates → exactly 10 succeeded, %d rejected with 402 (advisory lock)", goroutines, quotaRejects)

	t.Log("ALL PASS: Free cap → signed webhook raises plan_tier (billing.subscriptions + app.org_meta) → cap raised → replay ignored → forged 400 → cancel→Free/over_limit (no data loss) → concurrency exactly-cap")
}

func isQuota(err error) bool {
	_, ok := quota.AsExceeded(err)
	return ok
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// cbSign builds a valid Stripe-Signature header (t=<unix>,v1=<hex hmac>) over the
// raw body — exactly what Stripe sends — so the test drives the REAL
// webhook.ConstructEvent verification path.
func cbSign(payload []byte, secret string) string {
	now := time.Now()
	sig := webhook.ComputeSignature(now, payload, secret)
	return fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(sig))
}

func cbPostWebhook(t *testing.T, h *cloudbilling.Handler, payload []byte, sigHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(string(payload)))
	req.Header.Set("Stripe-Signature", sigHeader)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func cbScanText(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return s
}

func cbScanInt(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

// cbScanIntOrg reads an app (RLS-protected) table for orgID by establishing the
// tenant GUC inside a tx first — the same SET LOCAL app.current_org_id the runtime
// uses. Without it, a plain dropway_app read of app.org_meta/app.sites is filtered
// out by RLS (default-deny). This makes the test's assertions read under the right
// tenant context, mirroring production.
func cbScanIntOrg(t *testing.T, pool *pgxpool.Pool, orgID, sql string, args ...any) int64 {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', $1, true)`, orgID); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var n int64
	if err := tx.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

func cbStartPostgres(t *testing.T) {
	t.Helper()
	name := "dropway-cb-pg"
	cbDockerRm(name)
	cbRun(t, "docker", "run", "-d", "--name", name,
		"-e", "POSTGRES_USER=postgres", "-e", "POSTGRES_PASSWORD=postgres", "-e", "POSTGRES_DB=dropway",
		"-p", cbPort+":5432", cbPgImage)
	t.Cleanup(func() { cbDockerRm(name) })
	cbWaitFor(t, "postgres", func() bool {
		return exec.Command("docker", "exec", name, "pg_isready", "-U", "postgres", "-d", "dropway").Run() == nil
	})
	time.Sleep(1 * time.Second)
}

// cbApplyMigrations applies BOTH the app migrations AND the cloud billing migration
// (the task requires both), then sets the dropway_app runtime password.
//
// Each migration dir gets its OWN goose version table (-table). The app and billing
// dirs both start at version 0001; with the default shared goose_db_version table,
// goose would treat billing's 0001 as already applied (same version number) and
// SKIP it. Separate version tables mirror production, where the app and billing
// schemas are migrated independently by different pipelines (§5 cloud→core only).
func cbApplyMigrations(t *testing.T, root string) {
	t.Helper()
	migs := []struct{ dir, table string }{
		{"db/migrations/app", "goose_db_version"},
		{"db/migrations/billing", "goose_db_version_billing"},
	}
	for _, m := range migs {
		g := exec.Command("go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.22.0",
			"-table", m.table, "-dir", root+"/"+m.dir, "postgres", cbOwnerDSN, "up")
		g.Dir = root
		if out, err := g.CombinedOutput(); err != nil {
			t.Fatalf("goose up %s: %v\n%s", m.dir, err, out)
		}
	}
	cbRun(t, "docker", "exec", "dropway-cb-pg", "psql",
		"postgres://postgres:postgres@127.0.0.1:5432/dropway?sslmode=disable",
		"-v", "ON_ERROR_STOP=1", "-c", "ALTER ROLE dropway_app WITH PASSWORD '"+cbAppPw+"';")
}

// cbSeedAuthMember creates the minimal Better Auth identity.member + identity.invitation
// tables the member-cap preflight reads (countMembersAndPending queries BOTH), and grants
// the runtime role SELECT on them. Cloud always has Better Auth migrated, so both exist.
func cbSeedAuthMember(t *testing.T) {
	t.Helper()
	cbExecOwner(t, `CREATE SCHEMA IF NOT EXISTS identity;
		CREATE TABLE IF NOT EXISTS identity.member (
			id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
			"organizationId" uuid NOT NULL,
			"userId" uuid NOT NULL,
			"role" text NOT NULL);
		CREATE TABLE IF NOT EXISTS identity.invitation (
			id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
			"organizationId" uuid NOT NULL,
			status text NOT NULL);
		GRANT USAGE ON SCHEMA identity TO dropway_app;
		GRANT SELECT ON identity.member TO dropway_app;
		GRANT SELECT ON identity.invitation TO dropway_app;`)
}

// cbExecOwner runs SQL as the owner (postgres) inside the pg container.
func cbExecOwner(t *testing.T, sql string) {
	t.Helper()
	cbRun(t, "docker", "exec", "dropway-cb-pg", "psql",
		"postgres://postgres:postgres@127.0.0.1:5432/dropway?sslmode=disable",
		"-v", "ON_ERROR_STOP=1", "-c", sql)
}

// cbSeedAuthOrg seeds the Better-Auth-owned identity.organization slug for an org so
// store.CreateSite can form the org-namespaced content host (projection.HostForSite).
func cbSeedAuthOrg(t *testing.T, orgID, slug string) {
	t.Helper()
	cbExecOwner(t, `CREATE SCHEMA IF NOT EXISTS identity;
		CREATE TABLE IF NOT EXISTS identity.organization (id uuid PRIMARY KEY, slug text NOT NULL, name text);
		GRANT USAGE ON SCHEMA identity TO dropway_app;
		GRANT SELECT ON identity.organization TO dropway_app;
		INSERT INTO identity.organization (id, slug, name) VALUES ('`+orgID+`', '`+slug+`', '`+slug+`') ON CONFLICT (id) DO NOTHING;`)
}

func cbRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func cbDockerRm(name string) { _ = exec.Command("docker", "rm", "-f", name).Run() }

func cbWaitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func cbRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		j := strings.LastIndex(dir, "/")
		if j <= 0 {
			break
		}
		dir = dir[:j]
	}
	t.Fatal("could not locate repo root (go.mod)")
	return ""
}
