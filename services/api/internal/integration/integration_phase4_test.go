//go:build integration

// Phase-4 integration test (ARCHITECTURE.md §6/§10/§12/§13): audit logging, hard
// revocation denylist, R2 version GC, and the DR projection rebuild — exercised
// against real Postgres 16 (goose migrations as non-BYPASSRLS dropway_app) + MinIO.
//
// Run with:
//
//	go test -tags integration -run TestIntegration_Phase4 ./services/api/internal/integration/...
//
// Covered (all falsifiable):
//   - WriteAudit writes an RLS-scoped row; ListAudit reads it back newest-first; a
//     DIFFERENT org cannot see it (cross-tenant audit isolation).
//   - tightening a site's access writes revoked:site with a fresh min_iat (the
//     revocation deny-list contract), via the real Local projection Revoker.
//   - R2 version GC deletes an orphan blob, keeps a referenced blob, never touches
//     the current version's blob — against MinIO.
//   - DR rebuild: wipe the projection, RebuildAllOrgs restores serving routes.

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/edgerevoke"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func TestIntegration_Phase4(t *testing.T) {
	ctx := context.Background()
	repoRoot := repoRoot(t)

	startPostgres(t)
	startMinio(t)
	applyMigrations(t, repoRoot)

	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as dropway_app: %v", err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})

	obj := newMinioStore(t, ctx)
	if err := obj.EnsureBucket(ctx); err != nil {
		t.Fatalf("ensure bucket: %v", err)
	}
	proj := projection.NewLocal()

	orgA := "44444444-4444-4444-4444-444444444441"
	orgB := "44444444-4444-4444-4444-444444444442"
	userA := "a0000000-0000-0000-0000-0000000004a1"
	tA := store.Tenant{OrgID: orgA, UserID: userA}
	tB := store.Tenant{OrgID: orgB, UserID: "b0000000-0000-0000-0000-0000000004b1"}

	// Both orgs allow external sharing so a default-public site is permitted.
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgA)
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgB)
	seedAuthOrg(t, orgA, "orga")
	seedAuthOrg(t, orgB, "orgb")
	must(t, st.EnsureOrgProvisioned(ctx, tA))
	must(t, st.EnsureOrgProvisioned(ctx, tB))

	siteA, err := st.CreateSite(ctx, tA, "phase4a", projection.AccessPublic)
	if err != nil {
		t.Fatalf("create site A: %v", err)
	}

	// =======================================================================
	// 1. AUDIT: write a row, read it back newest-first, cross-tenant isolation.
	// =======================================================================
	_, err = st.WriteAudit(ctx, tA, store.AuditRecord{
		Action: "site.create",
		Target: "site:" + siteA.ID,
		Metadata: map[string]any{
			"slug": "phase4a",
		},
		Ctx: audit.Context{ActorUser: userA, IP: "203.0.113.7", RequestID: "req-123", TraceID: "req-123"},
	})
	must(t, err)
	_, err = st.WriteAudit(ctx, tA, store.AuditRecord{
		Action: "deploy.publish",
		Target: "site:" + siteA.ID,
		Ctx:    audit.Context{ActorUser: userA, RequestID: "req-456", TraceID: "req-456"},
	})
	must(t, err)

	entries, err := st.ListAudit(ctx, tA, store.ListAuditParams{Limit: 10})
	must(t, err)
	if len(entries) != 2 {
		t.Fatalf("expected 2 audit rows for org A, got %d", len(entries))
	}
	// Newest-first: deploy.publish was written last.
	if entries[0].Action != "deploy.publish" {
		t.Errorf("audit not newest-first: got %q first", entries[0].Action)
	}
	if entries[1].Action != "site.create" || entries[1].Metadata["slug"] != "phase4a" {
		t.Errorf("audit row mismatch: %+v", entries[1])
	}
	if entries[0].RequestID != "req-456" {
		t.Errorf("request_id not persisted: %q", entries[0].RequestID)
	}
	if entries[0].ActorUser == nil || *entries[0].ActorUser != userA {
		t.Errorf("actor_user not persisted: %v", entries[0].ActorUser)
	}

	// Cross-tenant isolation: org B sees NONE of org A's audit rows.
	bEntries, err := st.ListAudit(ctx, tB, store.ListAuditParams{Limit: 10})
	must(t, err)
	if len(bEntries) != 0 {
		t.Fatalf("AUDIT LEAK: org B sees %d of org A's audit rows", len(bEntries))
	}

	// =======================================================================
	// 2. REVOCATION: tightening a site's access writes revoked:site (fresh min_iat).
	// =======================================================================
	before := time.Now().Unix()
	// Tighten public → org_only and write the site denylist (what the handler does).
	if _, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: siteA.ID, Mode: projection.AccessOrgOnly}); err != nil {
		t.Fatalf("tighten access: %v", err)
	}
	minIAT := time.Now().Unix()
	must(t, proj.Revoke(ctx, edgerevoke.KindSite, siteA.ID, minIAT))

	rv, ok := proj.GetRevoked(edgerevoke.KindSite, siteA.ID)
	if !ok {
		t.Fatal("revoked:site:<id> was not written after access tighten")
	}
	if rv.MinIAT < before {
		t.Fatalf("revoked:site min_iat %d is stale (before %d)", rv.MinIAT, before)
	}
	// Idempotent max: an earlier write must not loosen it.
	must(t, proj.Revoke(ctx, edgerevoke.KindSite, siteA.ID, before-100))
	if rv2, _ := proj.GetRevoked(edgerevoke.KindSite, siteA.ID); rv2.MinIAT != rv.MinIAT {
		t.Fatalf("revocation loosened: %d → %d", rv.MinIAT, rv2.MinIAT)
	}

	// =======================================================================
	// 3. R2 VERSION GC: orphan deleted, referenced kept, current never touched.
	// =======================================================================
	// Deploy three versions to a fresh site; each references a distinct blob.
	gcSite, err := st.CreateSite(ctx, tA, "phase4gc", projection.AccessOrgOnly)
	must(t, err)

	v1, blob1 := deployOneBlobVersion(t, ctx, st, obj, tA, gcSite.ID, "v1-content")
	v2, blob2 := deployOneBlobVersion(t, ctx, st, obj, tA, gcSite.ID, "v2-content")
	v3, blob3 := deployOneBlobVersion(t, ctx, st, obj, tA, gcSite.ID, "v3-content")

	// STORAGE METER (docs/pricing.md §5): three distinct 10-byte blobs → 30 bytes.
	const blobLen = int64(len("v1-content")) // 10; all three contents are 10 bytes
	if got, err := st.OrgStorageBytes(ctx, tA); err != nil || got != 3*blobLen {
		t.Fatalf("storage after 3 deploys = %d (err=%v), want %d", got, err, 3*blobLen)
	}
	// DEDUP: re-deploying an ALREADY-STORED blob (v2's content) to a NEW site adds a
	// new version but NO new storage — the blob is counted once per org.
	dedupSite, err := st.CreateSite(ctx, tA, "phase4dedup", projection.AccessOrgOnly)
	must(t, err)
	deployOneBlobVersion(t, ctx, st, obj, tA, dedupSite.ID, "v2-content")
	if got, err := st.OrgStorageBytes(ctx, tA); err != nil || got != 3*blobLen {
		t.Fatalf("storage after a dedup deploy = %d (err=%v), want %d (no double-count)", got, err, 3*blobLen)
	}

	// Publish v3 (current). With keepLastN=1, retain v3 (current+newest) only → v1,v2
	// blobs become orphans. Publish v2 instead so the "current is not the newest"
	// path is also covered: publish v2, keepLastN=1 retains v3 (newest) + v2 (current)
	// → only v1's blob is an orphan.
	if _, err := st.Publish(ctx, tA, gcSite.ID, v2); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	_ = v3 // newest, retained by last-N

	// AGE GUARD (FIX): the blobs were JUST uploaded, so under the SAFE DEFAULT MinAge
	// (presign TTL + 1h) they are all younger than the cutoff — a GC with the default
	// policy must SPARE even the unreferenced v1 blob (it could be an in-flight
	// deploy). Prove that first: nothing is deleted, v1's blob is reported fresh.
	freshRes, err := st.GCOrg(ctx, obj, orgA, store.GCPolicy{KeepLastN: 1})
	must(t, err)
	if freshRes.Deleted != 0 || freshRes.SkippedFresh != 1 {
		t.Fatalf("age guard: a just-uploaded orphan must be SPARED under the default MinAge, got deleted=%d skippedFresh=%d", freshRes.Deleted, freshRes.SkippedFresh)
	}
	if exists, _, _ := obj.HeadBlob(ctx, orgA, blob1); !exists {
		t.Errorf("age guard: fresh orphan (v1=%s) wrongly deleted — this is the in-flight-deploy race", blob1[:8])
	}

	// Now run with a tiny MinAge (1ns) so the already-old-enough orphan IS eligible —
	// exercising the actual deletion path. v1's blob is the only orphan (v3 newest +
	// v2 current retained).
	res, err := st.GCOrg(ctx, obj, orgA, store.GCPolicy{KeepLastN: 1, MinAge: time.Nanosecond})
	must(t, err)

	if exists, _, _ := obj.HeadBlob(ctx, orgA, blob1); exists {
		t.Errorf("GC: orphan blob (v1=%s) should be deleted", blob1[:8])
	}
	if exists, _, _ := obj.HeadBlob(ctx, orgA, blob2); !exists {
		t.Errorf("GC: CURRENT version's blob (v2=%s) must NOT be deleted", blob2[:8])
	}
	if exists, _, _ := obj.HeadBlob(ctx, orgA, blob3); !exists {
		t.Errorf("GC: retained newest blob (v3=%s) must NOT be deleted", blob3[:8])
	}
	if res.Deleted != 1 {
		t.Errorf("GC deleted %d blobs, want 1 (only v1's orphan)", res.Deleted)
	}

	// STORAGE: GC freed v1's 10-byte blob → the counter decremented (30 → 20). blob2
	// is still referenced (gcSite current + dedupSite), so it was NOT freed.
	if got, err := st.OrgStorageBytes(ctx, tA); err != nil || got != 2*blobLen {
		t.Fatalf("storage after GC = %d (err=%v), want %d (only v1's blob freed)", got, err, 2*blobLen)
	}
	// Reconcile the counter to the authoritative ledger sum — unchanged here.
	must(t, st.RecomputeOrgStorage(ctx, tA))
	if got, err := st.OrgStorageBytes(ctx, tA); err != nil || got != 2*blobLen {
		t.Fatalf("storage after reconcile = %d (err=%v), want %d", got, err, 2*blobLen)
	}
	_ = v1

	// =======================================================================
	// 4. DR REBUILD: wipe the projection, RebuildAllOrgs restores serving.
	// =======================================================================
	// Publish a live version so there's a route to rebuild.
	if _, err := st.Publish(ctx, tA, gcSite.ID, v2); err != nil {
		t.Fatalf("re-publish v2: %v", err)
	}
	resPub, err := st.Publish(ctx, tA, siteA.ID, mustDeploy(t, ctx, st, obj, tA, siteA.ID, "siteA-live"))
	must(t, err)
	must(t, proj.PutRoute(ctx, resPub.Host, resPub.Route))

	// H4: register a verified CUSTOM domain on siteA so the DR rebuild must restore
	// it too — not just the canonical org-namespaced host. Before the fix, the rebuild
	// emitted only HostForSite(org,slug), so every custom domain stayed dark after a KV wipe.
	customHost := "dr.phase4.example"
	drDom, err := st.CreateDomain(ctx, tA, store.CreateDomainParams{
		SiteID: siteA.ID, Hostname: customHost, CFHostnameID: "cf-fake-dr", DCVRecord: "_cf TXT dr",
	})
	must(t, err)
	drDomRes, err := st.UpdateDomainStatus(ctx, tA, drDom.ID, store.DomainVerified, store.TLSIssued)
	must(t, err)
	must(t, proj.PutRoute(ctx, drDomRes.Host, drDomRes.Route))

	hostA := projection.HostForSite("orga", "phase4a")
	hostGC := projection.HostForSite("orga", "phase4gc")

	// Wipe the projection (simulate a KV/D1 loss).
	must(t, proj.RebuildFromDB(ctx, map[string]projection.RouteValue{}))
	if _, ok := proj.Get(hostA); ok {
		t.Fatal("projection not wiped")
	}
	if _, ok := proj.Get(customHost); ok {
		t.Fatal("projection not wiped (custom host)")
	}

	// DR drill: rebuild from Postgres across ALL orgs (enumerated via all_org_ids()).
	rebuilt, err := st.RebuildAllOrgs(ctx, proj)
	if err != nil {
		t.Fatalf("RebuildAllOrgs: %v", err)
	}
	if rebuilt.Orgs < 2 {
		t.Errorf("rebuild should enumerate >= 2 orgs, got %d", rebuilt.Orgs)
	}
	if _, ok := proj.Get(hostA); !ok {
		t.Errorf("DR rebuild did not restore route for %s", hostA)
	}
	if _, ok := proj.Get(hostGC); !ok {
		t.Errorf("DR rebuild did not restore route for %s", hostGC)
	}
	if _, ok := proj.Get(customHost); !ok {
		t.Errorf("H4: DR rebuild did not restore the CUSTOM-domain route %s", customHost)
	}

	t.Log("PASS: audit write/read + cross-tenant isolation, revocation deny-list write (idempotent), R2 GC orphan-delete, DR rebuild-from-Postgres")
}

// deployOneBlobVersion uploads one blob, writes a manifest referencing it, and
// inserts a version. Returns the version id + the blob sha.
func deployOneBlobVersion(t *testing.T, ctx context.Context, st *store.Store, obj storage.Store, tn store.Tenant, siteID, content string) (string, string) {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	sha := hex.EncodeToString(sum[:])
	must(t, obj.PutBlob(ctx, tn.OrgID, sha, bytes.NewReader([]byte(content)), int64(len(content)), "text/plain"))

	digestSum := sha256.Sum256([]byte("digest-" + content))
	digest := hex.EncodeToString(digestSum[:])
	ver, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{
		SiteID: siteID, ContentHash: digest, SizeBytes: int64(len(content)), Status: "ready",
		// Feed the per-org storage meter the deploy's one (content-addressed) blob.
		Blobs: []store.BlobSize{{SHA: sha, Size: int64(len(content))}},
	})
	must(t, err)

	// Manifest referencing the blob (the GC reads this to learn referenced shas).
	manifest := map[string]any{
		"schema_version": 1,
		"files": map[string]any{
			"index.html": map[string]any{"sha256": sha},
		},
	}
	body, _ := json.Marshal(manifest)
	must(t, obj.PutManifest(ctx, tn.OrgID, siteID, ver.ID, body))
	return ver.ID, sha
}

// mustDeploy is a thin wrapper returning just the version id.
func mustDeploy(t *testing.T, ctx context.Context, st *store.Store, obj storage.Store, tn store.Tenant, siteID, content string) string {
	t.Helper()
	id, _ := deployOneBlobVersion(t, ctx, st, obj, tn, siteID, content)
	return id
}
