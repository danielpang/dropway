// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// DefaultGCMinAge is the default minimum age an orphan blob must reach before the
// GC may delete it (FIX: R2 GC time-of-check/time-of-use race). Blobs are uploaded
// via a presigned PUT (TTL 15m, handlers.presignTTL) BEFORE the version row is
// finalized, so a GC overlapping an in-flight deploy would otherwise delete that
// deploy's just-uploaded, not-yet-referenced blobs and corrupt it. We require an
// orphan to be older than the presign TTL PLUS a 1h safety margin (covers clock
// skew + a slow upload/commit) before it is eligible for deletion.
const DefaultGCMinAge = 15*time.Minute + time.Hour

// GCPolicy configures the R2 version GC retention.
type GCPolicy struct {
	// KeepLastN is how many MOST-RECENT versions per site to retain in addition to
	// the live (current) version. A version's blobs are kept if it is the current
	// version OR among the last N by version_no. KeepLastN <= 0 keeps only the
	// current version (plus any version sharing its blobs). The default the CLI
	// passes is 5.
	KeepLastN int
	// MinAge is the AGE GUARD: an orphan blob is only deleted if it is OLDER than
	// MinAge (i.e. last-modified before now-MinAge). This prevents the GC from
	// deleting an in-flight deploy's just-uploaded blob in the window between the
	// presigned PUT and the version row being finalized. A zero/negative value uses
	// DefaultGCMinAge (presign TTL + 1h); set it explicitly (e.g. for a test) to
	// override. The age guard is enforced even on a non-DryRun run.
	MinAge time.Duration
	// DryRun, when true, reports what WOULD be deleted without deleting anything.
	DryRun bool
}

// minAge returns the effective age guard: the configured MinAge, or
// DefaultGCMinAge when it is unset (<= 0). A safe default means a caller can never
// accidentally disable the guard by leaving the field zero.
func (p GCPolicy) minAge() time.Duration {
	if p.MinAge <= 0 {
		return DefaultGCMinAge
	}
	return p.MinAge
}

// GCResult summarizes one org's GC pass.
type GCResult struct {
	OrgID            string
	RetainedVersions int      // versions whose manifests were read for referenced blobs
	ReferencedBlobs  int      // distinct blob shas referenced by retained versions
	ScannedBlobs     int      // blobs that exist under the org prefix
	Orphans          []string // unreferenced + OLD-ENOUGH blob shas that were (or would be, on DryRun) deleted
	SkippedFresh     int      // unreferenced blobs SPARED by the age guard (younger than MinAge — likely an in-flight deploy)
	Deleted          int      // blobs actually deleted (0 on DryRun)
}

// gcManifest is the minimal shape the GC parses out of a stored deploy manifest
// (manifests/<org>/<site>/<version>.json). It mirrors the handler's storedManifest
// — we only need the per-file sha to learn which blobs a version references.
type gcManifest struct {
	Files map[string]struct {
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

// GCOrg runs the R2 version GC for ONE org: it determines the versions to retain
// (current + last N per site), reads each retained version's manifest to collect
// the referenced blob shas, lists every blob under the org's prefix, and deletes
// the blobs referenced by NO retained version.
//
// Safety:
//   - The CURRENT (live) version's blobs are NEVER deleted — it is always retained.
//   - A blob referenced by ANY retained version is kept even if another (collected)
//     version also references it (the referenced set is a union).
//   - An unreferenced blob YOUNGER than pol.MinAge (presign TTL + safety margin) is
//     SPARED: it is likely an in-flight deploy's just-uploaded blob whose version
//     row hasn't been finalized yet, so deleting it would corrupt that deploy.
//   - DryRun reports orphans without deleting.
//   - It runs under the org's own RLS tenant context for the DB read (no BYPASSRLS),
//     and the blob list/delete is scoped to the org's prefix, so it can never touch
//     another tenant's blobs.
func (s *Store) GCOrg(ctx context.Context, obj storage.Store, orgID string, pol GCPolicy) (GCResult, error) {
	t := Tenant{OrgID: orgID, UserID: orgID} // user id unused by these reads; reuse org for a valid GUC

	// 1. Pick the versions to retain per site (current + last N) under the org's RLS
	//    tenant context, plus every skill's CURRENT version — skills share the
	//    per-org blob namespace with deploys, so a site-only reference set would
	//    make all skill content look orphaned and delete it.
	var retained []db.ListVersionsForGCRow
	var skillRetained []db.ListCurrentSkillVersionsForGCRow
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListVersionsForGC(ctx)
		if err != nil {
			return err
		}
		retained = selectRetained(rows, pol.KeepLastN)
		skillRetained, err = q.ListCurrentSkillVersionsForGC(ctx)
		if isUndefinedTable(err) {
			// The skills tables (migration 0008) aren't applied yet — deploy ran
			// before migrate. Treat as "no skills to protect" so the site GC still
			// runs, rather than aborting the whole pass for every org.
			skillRetained, err = nil, nil
		}
		return err
	})
	if err != nil {
		return GCResult{OrgID: orgID}, err
	}

	// 2–4. Collect referenced blobs from the retained manifests and delete orphans.
	//      Extracted so the blob bookkeeping is unit-testable with the in-memory fake
	//      (no live DB) — the only DB-dependent step is the version selection above.
	res, err := gcCollectAndDelete(ctx, obj, orgID, retained, skillRetained, pol, time.Now())
	if err != nil {
		return res, err
	}

	// 5. Reconcile the storage ledger with the R2 deletes: drop
	//    each deleted blob's org_blobs row and decrement the org's running total. The
	//    R2 objects are already gone; a crash here leaves the counter slightly HIGH
	//    until RecomputeOrgStorage reconciles — the safe direction (never under-counts
	//    a tenant). Skipped on DryRun (nothing was deleted).
	if !pol.DryRun && len(res.Orphans) > 0 {
		if err := s.releaseStorageForBlobs(ctx, orgID, res.Orphans); err != nil {
			return res, fmt.Errorf("gc: release storage for %s: %w", orgID, err)
		}
	}
	return res, nil
}

// releaseStorageForBlobs drops the ledger rows for blobs GC deleted from R2 and
// decrements the org's running storage total by their summed sizes, in one
// org-scoped tx. A blob absent from the ledger (e.g. uploaded before metering
// existed) frees nothing — DeleteOrgBlob returns no row.
func (s *Store) releaseStorageForBlobs(ctx context.Context, orgID string, shas []string) error {
	t := Tenant{OrgID: orgID, UserID: orgID} // user id unused by these writes; reuse org for a valid GUC
	return s.withTx(ctx, t, func(q *db.Queries) error {
		var freed int64
		for _, sha := range shas {
			n, err := q.DeleteOrgBlob(ctx, db.DeleteOrgBlobParams{OrgID: orgID, ContentHash: sha})
			if err != nil {
				if isNoRows(err) {
					continue // not in the ledger → nothing to free
				}
				return err
			}
			freed += n
		}
		if freed > 0 {
			return q.SubOrgStorage(ctx, db.SubOrgStorageParams{Delta: freed, OrgID: orgID})
		}
		return nil
	})
}

// gcCollectAndDelete reads each retained version's manifest, unions the referenced
// blob shas, lists the org's blobs, and deletes the orphans (unless DryRun). An
// unreferenced blob is only an ORPHAN if it is also OLDER than pol.MinAge (relative
// to now); a younger unreferenced blob is SPARED (counted in SkippedFresh) because
// it is likely an in-flight deploy's just-uploaded, not-yet-referenced blob. It is
// pure over the storage.Store + the retained version list + now, so it is exercised
// directly by the unit test with the in-memory fake.
func gcCollectAndDelete(ctx context.Context, obj storage.Store, orgID string, retained []db.ListVersionsForGCRow, skillRetained []db.ListCurrentSkillVersionsForGCRow, pol GCPolicy, now time.Time) (GCResult, error) {
	res := GCResult{OrgID: orgID, RetainedVersions: len(retained) + len(skillRetained)}

	referenced := map[string]struct{}{}
	addRefs := func(body []byte, what string) error {
		var m gcManifest
		if err := json.Unmarshal(body, &m); err != nil {
			return fmt.Errorf("gc: parse manifest %s: %w", what, err)
		}
		for _, f := range m.Files {
			if f.SHA256 != "" {
				referenced[f.SHA256] = struct{}{}
			}
		}
		return nil
	}
	for _, v := range retained {
		body, err := obj.GetManifest(ctx, orgID, v.SiteID, v.VersionID)
		if err != nil {
			if err == storage.ErrNotFound {
				// A retained version with no manifest object (e.g. a pending/failed
				// deploy) references no known blobs; skip it. We must NOT treat this as
				// "delete everything" — fail safe by simply not adding refs.
				continue
			}
			return res, fmt.Errorf("gc: read manifest %s/%s: %w", v.SiteID, v.VersionID, err)
		}
		if err := addRefs(body, v.SiteID+"/"+v.VersionID); err != nil {
			return res, err
		}
	}
	// Union in each skill's CURRENT version refs (skill manifests share the
	// gcManifest {files:{path:{sha256}}} shape with deploy manifests). Same
	// fail-safe on a missing manifest object.
	for _, sv := range skillRetained {
		if sv.VersionID == nil {
			continue
		}
		body, err := obj.GetSkillManifest(ctx, orgID, sv.SkillID, *sv.VersionID)
		if err != nil {
			if err == storage.ErrNotFound {
				continue
			}
			return res, fmt.Errorf("gc: read skill manifest %s/%s: %w", sv.SkillID, *sv.VersionID, err)
		}
		if err := addRefs(body, "skill "+sv.SkillID+"/"+*sv.VersionID); err != nil {
			return res, err
		}
	}
	res.ReferencedBlobs = len(referenced)

	infos, err := obj.ListBlobInfos(ctx, orgID)
	if err != nil {
		return res, fmt.Errorf("gc: list blobs: %w", err)
	}
	res.ScannedBlobs = len(infos)

	// Age guard: an orphan is only eligible for deletion once it is older than
	// MinAge (presign TTL + safety margin). Anything modified at/after this cutoff
	// is spared — it may be an in-flight deploy's blob whose version row isn't
	// finalized yet, so deleting it would corrupt the deploy.
	cutoff := now.Add(-pol.minAge())
	for _, info := range infos {
		if _, keep := referenced[info.SHA]; keep {
			continue
		}
		// Unreferenced. Spare it if it is too new (last-modified at/after the cutoff).
		// A zero last-modified (store didn't report one) is treated as old/eligible.
		if !info.LastModified.IsZero() && !info.LastModified.Before(cutoff) {
			res.SkippedFresh++
			continue
		}
		res.Orphans = append(res.Orphans, info.SHA)
	}
	sort.Strings(res.Orphans) // deterministic output for logs/tests

	if pol.DryRun {
		return res, nil
	}
	for _, sha := range res.Orphans {
		if err := obj.DeleteBlob(ctx, orgID, sha); err != nil {
			return res, fmt.Errorf("gc: delete blob %s: %w", sha, err)
		}
		res.Deleted++
	}
	return res, nil
}

// GCAllOrgs runs GCOrg across every org (the system-wide GC the CLI invokes with no
// --org). It enumerates orgs via the SECURITY DEFINER app.all_org_ids() and returns
// one result per org.
func (s *Store) GCAllOrgs(ctx context.Context, obj storage.Store, pol GCPolicy) ([]GCResult, error) {
	orgIDs, err := s.ListAllOrgIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]GCResult, 0, len(orgIDs))
	for _, orgID := range orgIDs {
		r, err := s.GCOrg(ctx, obj, orgID, pol)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// selectRetained picks, per site, the set of versions whose blobs must be kept: the
// CURRENT (live) version plus the most-recent keepLastN by version_no. The input
// rows are ordered (site_id, version_no DESC) by the query, so the first keepLastN
// rows of each site are the newest. The current version is always included (it may
// be older than the newest N — e.g. after a rollback — and must never be GC'd).
func selectRetained(rows []db.ListVersionsForGCRow, keepLastN int) []db.ListVersionsForGCRow {
	if keepLastN < 0 {
		keepLastN = 0
	}
	var out []db.ListVersionsForGCRow
	perSiteKept := map[string]int{}
	seen := map[string]bool{} // version_id → already retained (avoid dupes)

	add := func(v db.ListVersionsForGCRow) {
		if seen[v.VersionID] {
			return
		}
		seen[v.VersionID] = true
		out = append(out, v)
	}

	// First pass: always retain the current version of each site.
	for _, v := range rows {
		if v.IsCurrent.Valid && v.IsCurrent.Bool {
			add(v)
		}
	}
	// Second pass: retain the top keepLastN newest per site (rows are version_no
	// DESC within a site).
	for _, v := range rows {
		if perSiteKept[v.SiteID] >= keepLastN {
			continue
		}
		perSiteKept[v.SiteID]++
		add(v)
	}
	return out
}
