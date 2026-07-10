// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// gcNow is a fixed reference "now" for the GC tests. Blobs staged with PutBlobBytes
// are stamped at the Fake's clock; to exercise deletion under the age guard, the
// tests run the GC at gcNow with blobs staged well in the PAST (older than MinAge).
var gcNow = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// oldEnough is a timestamp safely older than the default GC MinAge (presign TTL +
// 1h ≈ 1h15m) relative to gcNow, so a blob stamped with it is GC-eligible.
var oldEnough = gcNow.Add(-48 * time.Hour)

// manifestJSON builds a stored-manifest body referencing the given blob shas.
func manifestJSON(shas ...string) []byte {
	type target struct {
		SHA256 string `json:"sha256"`
	}
	m := struct {
		SchemaVersion int               `json:"schema_version"`
		Files         map[string]target `json:"files"`
	}{SchemaVersion: 1, Files: map[string]target{}}
	for i, s := range shas {
		m.Files["f"+string(rune('a'+i))+".html"] = target{SHA256: s}
	}
	b, _ := json.Marshal(m)
	return b
}

func ver(id, siteID string, no int32, current bool) db.ListVersionsForGCRow {
	return db.ListVersionsForGCRow{
		VersionID: id, SiteID: siteID, VersionNo: no, R2Prefix: "manifests/org/" + siteID,
		IsCurrent: pgtype.Bool{Bool: current, Valid: true},
	}
}

// TestGC_SkillManifestBlobsRetained guards the skill-protection branch: a blob
// referenced ONLY by a current skill version (no site references it) must be
// kept, while a blob no skill or site references is still deleted. Without the
// skillRetained union in gcCollectAndDelete, all skill content would be treated
// as orphaned and deleted.
func TestGC_SkillManifestBlobsRetained(t *testing.T) {
	ctx := context.Background()
	obj := storage.NewFake()
	const org = "org-1"

	const blobSkill = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const blobOrphan = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	must(t, obj.PutBlobBytesAt(ctx, org, blobSkill, []byte("skill"), oldEnough))
	must(t, obj.PutBlobBytesAt(ctx, org, blobOrphan, []byte("orphan"), oldEnough))

	// The skill's current version manifest references blobSkill; no site exists.
	must(t, obj.PutSkillManifest(ctx, org, "skill-1", "sv1", manifestJSON(blobSkill)))

	skillRetained := []db.ListCurrentSkillVersionsForGCRow{
		{SkillID: "skill-1", VersionID: strPtr("sv1")},
	}

	res, err := gcCollectAndDelete(ctx, obj, org, nil, skillRetained, GCPolicy{}, gcNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Orphans) != 1 || res.Orphans[0] != blobOrphan {
		t.Fatalf("expected only the orphan deleted, got %v", res.Orphans)
	}
	if exists, _, _ := obj.HeadBlob(ctx, org, blobSkill); !exists {
		t.Error("skill current-version blob was wrongly deleted")
	}
	if exists, _, _ := obj.HeadBlob(ctx, org, blobOrphan); exists {
		t.Error("orphan blob was not deleted")
	}
}

func strPtr(s string) *string { return &s }

// TestGC_OrphanDeletedReferencedKeptCurrentUntouched is the core GC unit test:
// with the in-memory fake, an orphan blob is deleted, a
// referenced blob is kept, and the CURRENT version's blob is never touched.
func TestGC_OrphanDeletedReferencedKeptCurrentUntouched(t *testing.T) {
	ctx := context.Background()
	obj := storage.NewFake()
	const org = "org-1"

	// Three blobs:
	//   blobCurrent  — referenced by the CURRENT version (must be kept)
	//   blobRetained — referenced by a retained (last-N) version (must be kept)
	//   blobOrphan   — referenced by NO retained version (must be deleted)
	const blobCurrent = "1111111111111111111111111111111111111111111111111111111111111111"
	const blobRetained = "2222222222222222222222222222222222222222222222222222222222222222"
	const blobOrphan = "3333333333333333333333333333333333333333333333333333333333333333"
	// All staged in the PAST (older than MinAge) so the age guard doesn't spare the
	// orphan; the referenced/current blobs are kept on the reference rule regardless.
	must(t, obj.PutBlobBytesAt(ctx, org, blobCurrent, []byte("cur"), oldEnough))
	must(t, obj.PutBlobBytesAt(ctx, org, blobRetained, []byte("ret"), oldEnough))
	must(t, obj.PutBlobBytesAt(ctx, org, blobOrphan, []byte("orphan"), oldEnough))

	// One site with three versions: v3 current, v2 retained (within last N), v1 old.
	// Manifests: v3→blobCurrent, v2→blobRetained, v1→blobOrphan.
	must(t, obj.PutManifest(ctx, org, "site-1", "v3", manifestJSON(blobCurrent)))
	must(t, obj.PutManifest(ctx, org, "site-1", "v2", manifestJSON(blobRetained)))
	must(t, obj.PutManifest(ctx, org, "site-1", "v1", manifestJSON(blobOrphan)))

	rows := []db.ListVersionsForGCRow{
		ver("v3", "site-1", 3, true),
		ver("v2", "site-1", 2, false),
		ver("v1", "site-1", 1, false),
	}
	// Keep last 2 (newest): retains v3 (current+newest) and v2 (2nd newest); v1 drops.
	retained := selectRetained(rows, 2, time.Now())

	res, err := gcCollectAndDelete(ctx, obj, org, retained, nil, GCPolicy{KeepLastN: 2}, gcNow)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly the orphan blob is deleted.
	if res.Deleted != 1 || len(res.Orphans) != 1 || res.Orphans[0] != blobOrphan {
		t.Fatalf("expected only %s deleted, got orphans=%v deleted=%d", blobOrphan, res.Orphans, res.Deleted)
	}
	// The orphan is gone; the current + retained blobs remain.
	if exists, _, _ := obj.HeadBlob(ctx, org, blobOrphan); exists {
		t.Error("orphan blob was not deleted")
	}
	if exists, _, _ := obj.HeadBlob(ctx, org, blobCurrent); !exists {
		t.Error("CURRENT version's blob was wrongly deleted")
	}
	if exists, _, _ := obj.HeadBlob(ctx, org, blobRetained); !exists {
		t.Error("retained version's blob was wrongly deleted")
	}
}

// TestGC_DryRunDeletesNothing asserts DryRun reports orphans without deleting.
func TestGC_DryRunDeletesNothing(t *testing.T) {
	ctx := context.Background()
	obj := storage.NewFake()
	const org = "org-1"
	const blobOrphan = "4444444444444444444444444444444444444444444444444444444444444444"
	must(t, obj.PutBlobBytesAt(ctx, org, blobOrphan, []byte("x"), oldEnough))

	res, err := gcCollectAndDelete(ctx, obj, org, nil, nil, GCPolicy{DryRun: true}, gcNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Orphans) != 1 || res.Deleted != 0 {
		t.Fatalf("dry run: orphans=%v deleted=%d, want 1 orphan / 0 deleted", res.Orphans, res.Deleted)
	}
	if exists, _, _ := obj.HeadBlob(ctx, org, blobOrphan); !exists {
		t.Error("dry run must NOT delete blobs")
	}
}

// TestGC_CrossOrgScoped asserts the GC only ever lists/deletes the target org's
// blobs (the per-org prefix isolates tenants).
func TestGC_CrossOrgScoped(t *testing.T) {
	ctx := context.Background()
	obj := storage.NewFake()
	const blob = "5555555555555555555555555555555555555555555555555555555555555555"
	must(t, obj.PutBlobBytesAt(ctx, "org-A", blob, []byte("a"), oldEnough))
	must(t, obj.PutBlobBytesAt(ctx, "org-B", blob, []byte("b"), oldEnough))

	// GC org-A with NO retained versions → org-A's blob is an orphan, org-B untouched.
	res, err := gcCollectAndDelete(ctx, obj, "org-A", nil, nil, GCPolicy{}, gcNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Fatalf("expected org-A blob deleted, deleted=%d", res.Deleted)
	}
	if exists, _, _ := obj.HeadBlob(ctx, "org-A", blob); exists {
		t.Error("org-A blob should be gone")
	}
	if exists, _, _ := obj.HeadBlob(ctx, "org-B", blob); !exists {
		t.Error("org-B blob must NOT be touched by an org-A GC")
	}
}

// TestGC_AgeGuard_FreshOrphanSurvivesOldOrphanDeleted is the FIX regression test
// for the R2 GC time-of-check/time-of-use race: a FRESH (recently-uploaded) orphan
// blob — the in-flight deploy case, uploaded via presigned PUT before its version
// row is finalized — SURVIVES the GC, while an OLD orphan IS deleted, and a
// referenced/current blob is never touched.
func TestGC_AgeGuard_FreshOrphanSurvivesOldOrphanDeleted(t *testing.T) {
	ctx := context.Background()
	obj := storage.NewFake()
	const org = "org-1"

	// blobCurrent  — referenced by the CURRENT version (kept on the reference rule).
	// blobOld      — unreferenced AND old (older than MinAge) → an orphan → deleted.
	// blobFresh    — unreferenced but JUST uploaded (within MinAge): an in-flight
	//                deploy's blob whose version row isn't finalized → MUST survive.
	const blobCurrent = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const blobOld = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const blobFresh = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	must(t, obj.PutBlobBytesAt(ctx, org, blobCurrent, []byte("cur"), oldEnough))
	must(t, obj.PutBlobBytesAt(ctx, org, blobOld, []byte("old"), oldEnough))
	// Fresh: stamped 1 minute before "now" — well within the default MinAge window.
	must(t, obj.PutBlobBytesAt(ctx, org, blobFresh, []byte("fresh"), gcNow.Add(-1*time.Minute)))

	// One site, current version v1 → blobCurrent. blobOld and blobFresh are
	// referenced by NO retained version (both are orphans by the reference rule).
	must(t, obj.PutManifest(ctx, org, "site-1", "v1", manifestJSON(blobCurrent)))
	retained := selectRetained([]db.ListVersionsForGCRow{ver("v1", "site-1", 1, true)}, 5, time.Now())

	res, err := gcCollectAndDelete(ctx, obj, org, retained, nil, GCPolicy{KeepLastN: 5}, gcNow)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly the OLD orphan is deleted; the fresh orphan is spared by the age guard.
	if res.Deleted != 1 || len(res.Orphans) != 1 || res.Orphans[0] != blobOld {
		t.Fatalf("expected only the OLD orphan %s deleted, got orphans=%v deleted=%d", blobOld, res.Orphans, res.Deleted)
	}
	if res.SkippedFresh != 1 {
		t.Errorf("expected 1 fresh orphan spared by the age guard, got SkippedFresh=%d", res.SkippedFresh)
	}
	// The fresh (in-flight) blob MUST still be present — deleting it would corrupt
	// the deploy that just uploaded it.
	if exists, _, _ := obj.HeadBlob(ctx, org, blobFresh); !exists {
		t.Error("FRESH orphan blob was wrongly GC'd — this is the time-of-check/time-of-use race that corrupts an in-flight deploy")
	}
	// The old orphan is gone; the current blob is untouched.
	if exists, _, _ := obj.HeadBlob(ctx, org, blobOld); exists {
		t.Error("OLD orphan blob should have been deleted")
	}
	if exists, _, _ := obj.HeadBlob(ctx, org, blobCurrent); !exists {
		t.Error("CURRENT version's blob was wrongly deleted")
	}
}

// TestGC_AgeGuard_DefaultMinAgeApplied asserts that leaving GCPolicy.MinAge zero
// still applies the SAFE DEFAULT (DefaultGCMinAge) rather than disabling the guard —
// a fresh orphan survives even when the caller didn't set MinAge explicitly.
func TestGC_AgeGuard_DefaultMinAgeApplied(t *testing.T) {
	ctx := context.Background()
	obj := storage.NewFake()
	const org = "org-1"
	const blobFresh = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	// Uploaded 5 minutes ago — younger than DefaultGCMinAge (presign TTL 15m + 1h).
	must(t, obj.PutBlobBytesAt(ctx, org, blobFresh, []byte("fresh"), gcNow.Add(-5*time.Minute)))

	// MinAge unset (zero) → must fall back to DefaultGCMinAge, not "delete everything".
	res, err := gcCollectAndDelete(ctx, obj, org, nil, nil, GCPolicy{}, gcNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 || len(res.Orphans) != 0 {
		t.Fatalf("a fresh orphan must NOT be deleted under the default MinAge; orphans=%v deleted=%d", res.Orphans, res.Deleted)
	}
	if res.SkippedFresh != 1 {
		t.Errorf("expected the fresh orphan spared by the default age guard, SkippedFresh=%d", res.SkippedFresh)
	}
	if exists, _, _ := obj.HeadBlob(ctx, org, blobFresh); !exists {
		t.Error("fresh orphan must survive the default age guard")
	}
}

// TestSelectRetained_KeepsCurrentEvenIfOld asserts the live version is always
// retained even after a rollback makes it older than the last-N newest versions.
func TestSelectRetained_KeepsCurrentEvenIfOld(t *testing.T) {
	// v5,v4,v3 are newest; current is v1 (rolled back). With keepLastN=2 we keep
	// v5,v4 (newest 2) PLUS v1 (current) = 3 retained.
	rows := []db.ListVersionsForGCRow{
		ver("v5", "s", 5, false),
		ver("v4", "s", 4, false),
		ver("v3", "s", 3, false),
		ver("v2", "s", 2, false),
		ver("v1", "s", 1, true), // current, but oldest
	}
	got := selectRetained(rows, 2, time.Now())
	keep := map[string]bool{}
	for _, v := range got {
		keep[v.VersionID] = true
	}
	if !keep["v1"] {
		t.Error("current (v1) must be retained even though it's the oldest")
	}
	if !keep["v5"] || !keep["v4"] {
		t.Error("newest 2 (v5,v4) must be retained")
	}
	if keep["v3"] || keep["v2"] {
		t.Error("v3/v2 should NOT be retained (beyond last-2 and not current)")
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 retained, got %d (%v)", len(got), keep)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
