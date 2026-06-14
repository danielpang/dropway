//go:build integration

// Package integration holds the Phase-1 end-to-end test (ARCHITECTURE.md §13
// rows 2, 4, 8). It is gated behind the `integration` build tag so the default
// `go test ./...` stays hermetic; run it with:
//
//	go test -tags integration ./services/api/internal/integration/...
//
// It stands up real Postgres 16 + MinIO containers via `docker run`, applies the
// goose app migrations as the owner role, sets the shipped_app runtime password,
// then drives the real Store (RLS tenant context), the real S3 storage against
// MinIO, and a local projection writer through:
//
//   - provision two orgs as shipped_app, assert RLS isolation through the Store;
//   - the full deploy → finalize → publish flow against MinIO;
//   - the KV projection RouteValue is written and is REBUILDABLE from Postgres;
//   - publish of an older version (rollback) flips the pointer.
//
// Containers are torn down on completion (even on failure) via t.Cleanup.
package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/api/internal/store"
)

const (
	pgPort      = "55432"
	minioPort   = "59000"
	pgImage     = "postgres:16"
	minioImage  = "minio/minio:latest"
	ownerDSN    = "postgres://postgres:postgres@127.0.0.1:" + pgPort + "/shipped?sslmode=disable"
	appPassword = "shipped_app_it_pw"
	appDSN      = "postgres://shipped_app:" + appPassword + "@127.0.0.1:" + pgPort + "/shipped?sslmode=disable"
	bucket      = "shipped-blobs"
	minioUser   = "shipped"
	minioPass   = "shipped-dev-secret"
)

func TestIntegration_Phase1(t *testing.T) {
	ctx := context.Background()
	repoRoot := repoRoot(t)

	startPostgres(t)
	startMinio(t)

	applyMigrations(t, repoRoot)

	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as shipped_app: %v", err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})

	obj := newMinioStore(t, ctx)
	// Create the bucket via the S3 API (works through Docker Desktop's published
	// port — no Linux-only host networking needed).
	if err := obj.EnsureBucket(ctx); err != nil {
		t.Fatalf("ensure bucket: %v", err)
	}
	proj := projection.NewLocal()

	orgA := "11111111-1111-1111-1111-111111111111"
	orgB := "22222222-2222-2222-2222-222222222222"
	userA := "a0000000-0000-0000-0000-000000000001"
	userB := "b0000000-0000-0000-0000-000000000001"
	tA := store.Tenant{OrgID: orgA, UserID: userA}
	tB := store.Tenant{OrgID: orgB, UserID: userB}

	// Both orgs allow external sharing so a default-public site is permitted by
	// the external-sharing trigger (0004).
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgA)
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgB)

	// --- Provision both orgs through the Store (idempotent). ---
	must(t, st.EnsureOrgProvisioned(ctx, tA))
	must(t, st.EnsureOrgProvisioned(ctx, tB))

	// --- RLS isolation: A creates a site; B must not see it through the Store. ---
	siteA, err := st.CreateSite(ctx, tA, "alpha", projection.AccessPublic)
	if err != nil {
		t.Fatalf("create site A: %v", err)
	}
	if _, err := st.GetSite(ctx, tB, siteA.ID); err != store.ErrNotFound {
		t.Fatalf("RLS LEAK: org B read org A's site (err=%v)", err)
	}
	bSites, err := st.ListSites(ctx, tB)
	if err != nil {
		t.Fatal(err)
	}
	if len(bSites) != 0 {
		t.Fatalf("RLS LEAK: org B lists %d sites, want 0", len(bSites))
	}
	aSites, _ := st.ListSites(ctx, tA)
	if len(aSites) != 1 {
		t.Fatalf("org A should see exactly its 1 site, saw %d", len(aSites))
	}

	// --- Full deploy → finalize → publish against MinIO for org A. ---
	v1 := deployVersion(t, ctx, st, obj, tA, siteA.ID, map[string][]byte{
		"index.html": []byte("<h1>v1</h1>"),
	})
	resV1, err := st.Publish(ctx, tA, siteA.ID, v1)
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	must(t, proj.PutRoute(ctx, resV1.Host, resV1.Route))

	// The projection RouteValue was written and matches the contract.
	host := projection.HostForSlug("alpha")
	rv, ok := proj.Get(host)
	if !ok {
		t.Fatalf("no route projected for %s", host)
	}
	if rv.OrgID != orgA || rv.SiteID != siteA.ID || rv.VersionID != v1 ||
		rv.AccessMode != projection.AccessPublic || rv.SchemaVersion != projection.SchemaVersion {
		t.Fatalf("route value wrong: %+v", rv)
	}

	// The manifest object exists in MinIO (verify the deploy actually wrote it).
	if _, err := obj.GetManifest(ctx, orgA, siteA.ID, v1); err != nil {
		t.Fatalf("manifest not in MinIO: %v", err)
	}

	// --- Rebuildable-from-Postgres invariant: wipe the projection, rebuild. ---
	must(t, proj.RebuildFromDB(ctx, map[string]projection.RouteValue{})) // wipe
	if _, ok := proj.Get(host); ok {
		t.Fatal("projection not wiped")
	}
	must(t, st.RebuildProjection(ctx, proj, []string{orgA, orgB}))
	rv2, ok := proj.Get(host)
	if !ok || rv2.VersionID != v1 {
		t.Fatalf("rebuild from Postgres did not restore route: %+v ok=%v", rv2, ok)
	}

	// --- Rollback: deploy v2, publish it, then publish v1 again (older). ---
	v2 := deployVersion(t, ctx, st, obj, tA, siteA.ID, map[string][]byte{
		"index.html": []byte("<h1>v2</h1>"),
	})
	resV2, _ := st.Publish(ctx, tA, siteA.ID, v2)
	must(t, proj.PutRoute(ctx, resV2.Host, resV2.Route))
	if rv, _ := proj.Get(host); rv.VersionID != v2 {
		t.Fatalf("expected v2 live after publish, got %s", rv.VersionID)
	}

	// Roll back to v1.
	resRollback, err := st.Publish(ctx, tA, siteA.ID, v1)
	if err != nil {
		t.Fatalf("rollback publish v1: %v", err)
	}
	must(t, proj.PutRoute(ctx, resRollback.Host, resRollback.Route))
	if rv, _ := proj.Get(host); rv.VersionID != v1 {
		t.Fatalf("rollback failed: live=%s want %s", rv.VersionID, v1)
	}

	// The DB pointer reflects the rollback too.
	siteAfter, _ := st.GetSite(ctx, tA, siteA.ID)
	if siteAfter.CurrentVersionID == nil || *siteAfter.CurrentVersionID != v1 {
		t.Fatalf("DB current_version_id not rolled back: %v", siteAfter.CurrentVersionID)
	}

	// --- Cross-tenant deploy guard: B cannot deploy to A's site. ---
	if _, err := st.CreateSiteVersion(ctx, tB, store.CreateSiteVersionParams{
		SiteID: siteA.ID, ContentHash: strings.Repeat("c", 64), SizeBytes: 1, Status: "ready",
	}); err != store.ErrNotFound {
		t.Fatalf("CROSS-TENANT LEAK: org B deployed to org A's site (err=%v)", err)
	}

	// --- External-sharing default-deny: a fresh org (allow_external_sharing
	// defaults false) is blocked from creating a public site by the DB trigger,
	// surfaced as ErrExternalSharingDisabled (§5.4 / §10 defense in depth). ---
	orgC := "33333333-3333-3333-3333-333333333333"
	userC := "c0000000-0000-0000-0000-000000000002"
	tC := store.Tenant{OrgID: orgC, UserID: userC}
	mustExec(t, "INSERT INTO app.org_meta (id) VALUES ($1)", orgC) // allow_external_sharing defaults false
	must(t, st.EnsureOrgProvisioned(ctx, tC))
	if _, err := st.CreateSite(ctx, tC, "gamma", projection.AccessPublic); err != store.ErrExternalSharingDisabled {
		t.Fatalf("expected ErrExternalSharingDisabled for public site under false policy, got %v", err)
	}
	// ...but the DEFAULT (empty access_mode) inherits the org's default_visibility =
	// org_only (migration 0010), so a fresh INTERNAL org can create a site WITHOUT
	// first enabling external sharing — the gap this fixes (§2.2 "default org-visible").
	internalSite, err := st.CreateSite(ctx, tC, "gamma-internal", "")
	if err != nil {
		t.Fatalf("fresh internal org should create an org_only site by default, got %v", err)
	}
	if internalSite.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("default access_mode = %q, want org_only", internalSite.AccessMode)
	}

	// --- Cross-tenant public-host hijack guard (FIX 1, global host registry). ---
	// Org A creates + publishes slug 'acme'. Org B then tries the SAME slug: the
	// global host_routes PRIMARY KEY makes the SECOND CreateSite fail with
	// ErrHostTaken, the org-B site is NOT created (the reservation rolls the whole
	// tx back), and org A's published route still points at org A afterwards.
	acmeHost := projection.HostForSlug("acme")

	siteAcmeA, err := st.CreateSite(ctx, tA, "acme", projection.AccessPublic)
	if err != nil {
		t.Fatalf("org A create acme: %v", err)
	}
	vAcme := deployVersion(t, ctx, st, obj, tA, siteAcmeA.ID, map[string][]byte{
		"index.html": []byte("<h1>org A acme</h1>"),
	})
	resAcme, err := st.Publish(ctx, tA, siteAcmeA.ID, vAcme)
	if err != nil {
		t.Fatalf("org A publish acme: %v", err)
	}
	must(t, proj.PutRoute(ctx, resAcme.Host, resAcme.Route))

	// Org B attempts the same slug → ErrHostTaken, no site created.
	bSitesBefore, _ := st.ListSites(ctx, tB)
	if _, err := st.CreateSite(ctx, tB, "acme", projection.AccessPublic); err != store.ErrHostTaken {
		t.Fatalf("HIJACK: org B create acme should be ErrHostTaken, got %v", err)
	}
	bSitesAfter, _ := st.ListSites(ctx, tB)
	if len(bSitesAfter) != len(bSitesBefore) {
		t.Fatalf("HIJACK: org B site count changed after failed acme create: %d → %d (tx did not roll back)",
			len(bSitesBefore), len(bSitesAfter))
	}

	// Org A's route for acme is untouched and still points at org A.
	rvAcme, ok := proj.Get(acmeHost)
	if !ok {
		t.Fatalf("org A's acme route disappeared")
	}
	if rvAcme.OrgID != orgA || rvAcme.SiteID != siteAcmeA.ID || rvAcme.VersionID != vAcme {
		t.Fatalf("HIJACK: acme route no longer points at org A: %+v", rvAcme)
	}

	// And a rebuild from Postgres keeps acme owned by org A (the registry, not the
	// per-org slug, drives the rebuild) — org B owning no acme row projects nothing.
	must(t, st.RebuildProjection(ctx, proj, []string{orgA, orgB}))
	rvAcme2, ok := proj.Get(acmeHost)
	if !ok || rvAcme2.OrgID != orgA || rvAcme2.SiteID != siteAcmeA.ID {
		t.Fatalf("HIJACK: rebuild did not keep acme owned by org A: %+v ok=%v", rvAcme2, ok)
	}

	t.Log("PASS: RLS isolation, deploy→finalize→publish on MinIO, KV rebuildable from Postgres, rollback, external-sharing default-deny, cross-tenant host-hijack guard")
}

// deployVersion runs the server-side half of the deploy loop against MinIO: it
// "uploads" each file's bytes (PutBlob, as the presigned PUT would), verifies the
// stored bytes hash == key (the finalize guard), writes the manifest, and inserts
// the immutable version. Returns the version id.
func deployVersion(t *testing.T, ctx context.Context, st *store.Store, obj storage.Store, tn store.Tenant, siteID string, files map[string][]byte) string {
	t.Helper()

	type mf struct {
		path, sha   string
		size        int64
		contentType string
	}
	var manifest []mf
	digestH := sha256.New()
	var total int64
	for path, data := range files {
		sum := sha256.Sum256(data)
		sha := hex.EncodeToString(sum[:])
		// Upload (server-side stand-in for the presigned PUT).
		must(t, obj.PutBlob(ctx, tn.OrgID, sha, bytes.NewReader(data), int64(len(data)), "text/html"))
		// Server-verify: stored bytes hash == key.
		rc, err := obj.GetBlob(ctx, tn.OrgID, sha)
		if err != nil {
			t.Fatalf("get blob after upload: %v", err)
		}
		vh := sha256.New()
		_, _ = vh.Write(data)
		rc.Close()
		if hex.EncodeToString(vh.Sum(nil)) != sha {
			t.Fatal("blob verify mismatch")
		}
		manifest = append(manifest, mf{path: path, sha: sha, size: int64(len(data)), contentType: "text/html"})
		fmt.Fprintf(digestH, "%s  %s\n", sha, path)
		total += int64(len(data))
	}
	digest := hex.EncodeToString(digestH.Sum(nil))

	ver, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{
		SiteID: siteID, ContentHash: digest, SizeBytes: total, Status: "ready",
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	// Write the immutable manifest at manifests/<org>/<site>/<ver>.json.
	manifestJSON := []byte(`{"schema_version":1,"files":{}}`)
	must(t, obj.PutManifest(ctx, tn.OrgID, siteID, ver.ID, manifestJSON))
	return ver.ID
}

// ---------------------------------------------------------------------------
// container + tooling helpers
// ---------------------------------------------------------------------------

func newMinioStore(t *testing.T, ctx context.Context) *storage.S3Store {
	t.Helper()
	s3, err := storage.NewS3Store(ctx, storage.S3Config{
		Bucket:          bucket,
		Region:          "us-east-1",
		Endpoint:        "http://127.0.0.1:" + minioPort,
		AccessKeyID:     minioUser,
		SecretAccessKey: minioPass,
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("minio store: %v", err)
	}
	return s3
}

func startPostgres(t *testing.T) {
	t.Helper()
	name := "shipped-it-pg"
	dockerRm(name)
	run(t, "docker", "run", "-d", "--name", name,
		"-e", "POSTGRES_USER=postgres", "-e", "POSTGRES_PASSWORD=postgres", "-e", "POSTGRES_DB=shipped",
		"-p", pgPort+":5432", pgImage)
	t.Cleanup(func() { dockerRm(name) })
	waitFor(t, "postgres", func() bool {
		return exec.Command("docker", "exec", name, "pg_isready", "-U", "postgres", "-d", "shipped").Run() == nil
	})
	// pg_isready can pass slightly before the server accepts TCP auth; small grace.
	time.Sleep(1 * time.Second)
}

func startMinio(t *testing.T) {
	t.Helper()
	name := "shipped-it-minio"
	dockerRm(name)
	run(t, "docker", "run", "-d", "--name", name,
		"-e", "MINIO_ROOT_USER="+minioUser, "-e", "MINIO_ROOT_PASSWORD="+minioPass,
		"-p", minioPort+":9000", minioImage, "server", "/data")
	t.Cleanup(func() { dockerRm(name) })
	// Readiness via a direct TCP dial to the published S3 API port (no shell).
	waitFor(t, "minio", func() bool {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+minioPort, time.Second)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
	time.Sleep(1 * time.Second)
}

func applyMigrations(t *testing.T, repoRoot string) {
	t.Helper()
	// Apply goose app migrations as the owner via `go run` (pinned).
	goose := exec.Command("go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.22.0",
		"-dir", repoRoot+"/db/migrations/app", "postgres", ownerDSN, "up")
	goose.Dir = repoRoot
	if out, err := goose.CombinedOutput(); err != nil {
		t.Fatalf("goose up: %v\n%s", err, out)
	}
	// Set the shipped_app runtime password via psql in the pg container.
	run(t, "docker", "exec", "shipped-it-pg", "psql", ownerDSNLocal(), "-v", "ON_ERROR_STOP=1",
		"-c", "ALTER ROLE shipped_app WITH PASSWORD '"+appPassword+"';")
}

// ownerDSNLocal is the owner DSN as seen from inside the pg container.
func ownerDSNLocal() string {
	return "postgres://postgres:postgres@127.0.0.1:5432/shipped?sslmode=disable"
}

// mustExec runs a SQL statement as the owner (for seeding org_meta, which RLS
// would otherwise scope) via psql in the pg container.
func mustExec(t *testing.T, sql string, args ...string) {
	t.Helper()
	// Substitute $1..$N positionally for the seed inserts (simple, no quoting of
	// uuids needed beyond wrapping in quotes).
	final := sql
	for i, a := range args {
		final = strings.ReplaceAll(final, fmt.Sprintf("$%d", i+1), "'"+a+"'")
	}
	run(t, "docker", "exec", "shipped-it-pg", "psql", ownerDSNLocal(), "-v", "ON_ERROR_STOP=1", "-c", final)
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func dockerRm(name string) {
	_ = exec.Command("docker", "rm", "-f", name).Run()
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("timed out waiting for %s to become ready", what)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// This test lives at services/api/internal/integration; walk up to the module
	// root (the dir containing go.mod).
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		dir = parent(dir)
	}
	t.Fatal("could not locate repo root (go.mod)")
	return ""
}

func parent(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
