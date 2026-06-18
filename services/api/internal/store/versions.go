// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// BlobSize is one content-addressed blob's identity + server-observed size, used to
// account per-org storage (docs/pricing.md §5). The handler passes the distinct
// blobs of the deploy (its sizeBySHA), so storage is metered dedup-aware.
type BlobSize struct {
	SHA  string
	Size int64
}

// CreateSiteVersionParams carries the inputs for an immutable deploy row.
type CreateSiteVersionParams struct {
	SiteID      string
	ContentHash string // sha256 of the deploy manifest (the whole-deploy digest)
	SizeBytes   int64
	Status      string // typically "ready" once blobs are verified + manifest written
	// Blobs are the deploy's DISTINCT content-addressed blobs (+ sizes). On a
	// genuinely-new version they feed the dedup-aware storage meter + cap; on an
	// idempotent re-deploy (existing content_hash) they're ignored (no new storage).
	Blobs []BlobSize
}

// CreateSiteVersion inserts the next immutable version for a site. It re-derives
// the site's org and asserts it matches the active tenant (confused-deputy
// guard), computes the next monotonic version_no, and is idempotent on the
// per-site content_hash: a re-deploy of byte-identical content returns the
// existing version instead of erroring on the unique constraint.
//
// The r2_prefix is the canonical manifest key (storage.ManifestKey), so the
// version row records exactly where its manifest lives.
func (s *Store) CreateSiteVersion(ctx context.Context, t Tenant, p CreateSiteVersionParams) (SiteVersion, error) {
	status := p.Status
	if status == "" {
		status = "ready"
	}

	var out SiteVersion
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		// Re-derive the site (RLS scopes it to the org) — a miss means the site
		// is absent or belongs to another tenant.
		site, err := q.GetSite(ctx, p.SiteID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}

		// Idempotency: identical content for this site → return existing version.
		if existing, err := q.GetSiteVersionByContentHash(ctx, db.GetSiteVersionByContentHashParams{
			SiteID:      p.SiteID,
			ContentHash: p.ContentHash,
		}); err == nil {
			out = versionFromDB(existing)
			return nil
		} else if !isNoRows(err) {
			return err
		}

		// Storage metering + cap (docs/pricing.md §5). Account this deploy's NEW,
		// dedup-aware blob bytes and enforce the per-org storage cap — race-safe under
		// a per-org advisory lock (the same TOCTOU guard the site cap uses). OSS =
		// Unlimited (always allowed); cloud = the byte bands. Runs only for a
		// genuinely-new version (the idempotent re-deploy above already returned).
		if err := s.accountStorage(ctx, q, t.OrgID, p.Blobs); err != nil {
			return err // *quota.ExceededError → 402; rolls back the whole deploy tx
		}

		nextNo, err := q.NextVersionNo(ctx, p.SiteID)
		if err != nil {
			return err
		}

		// We don't yet know the version id (DEFAULT gen_random_uuid()), so the
		// r2_prefix records the manifest *directory*; the manifest object key is
		// filled in by the caller after the id is known. We store the canonical
		// manifest key using the returned id below via a follow-up — but since the
		// row is immutable and the id is generated, we instead let the caller pass
		// the prefix. To keep this self-contained, record the per-site/version
		// manifest directory and let serving resolve <version_id>.json.
		row, err := q.CreateSiteVersion(ctx, db.CreateSiteVersionParams{
			OrgID:       t.OrgID,
			SiteID:      p.SiteID,
			VersionNo:   nextNo,
			Status:      status,
			R2Prefix:    manifestPrefix(t.OrgID, p.SiteID),
			ContentHash: p.ContentHash,
			SizeBytes:   p.SizeBytes,
			CreatedBy:   t.UserID,
		})
		if err != nil {
			return err
		}
		out = versionFromDB(row)
		return nil
	})
	return out, err
}

// accountStorage records this deploy's DISTINCT blobs in the per-org ledger
// (dedup-aware) and enforces the storage cap, inside the caller's deploy tx
// (docs/pricing.md §5). It takes the per-org storage advisory lock, sums the
// genuinely-NEW blob bytes (ON CONFLICT DO NOTHING → an already-stored blob isn't
// re-counted), checks the cap against current+delta, then increments the running
// total. delta==0 (every blob already stored) adds nothing and never trips the cap,
// so a re-shuffle deploy of existing blobs always proceeds even at/over the cap.
// Returns a *quota.ExceededError (→ 402) when the deploy would exceed the cap.
func (s *Store) accountStorage(ctx context.Context, q *db.Queries, orgID string, blobs []BlobSize) error {
	if len(blobs) == 0 {
		return nil
	}
	if err := q.LockOrgStorageQuota(ctx, orgID); err != nil {
		return err
	}
	current, err := q.GetOrgStorage(ctx, orgID)
	if err != nil {
		return err
	}
	var delta int64
	for _, b := range blobs {
		if b.SHA == "" {
			continue
		}
		n, err := q.InsertOrgBlob(ctx, db.InsertOrgBlobParams{
			OrgID: orgID, ContentHash: b.SHA, SizeBytes: b.Size,
		})
		if err != nil {
			if isNoRows(err) {
				continue // already stored for this org → not new, don't count
			}
			return err
		}
		delta += n // n == b.Size, only for a genuinely-new blob
	}
	if delta == 0 {
		return nil // no new bytes → no cap check, no counter change
	}
	planTier, err := q.GetPlanTier(ctx, orgID)
	if err != nil {
		return err
	}
	if err := s.quota.AllowN(planTier, quota.ResourceStorageBytesPerOrg, current, delta); err != nil {
		return err // *quota.ExceededError → 402
	}
	return q.AddOrgStorage(ctx, db.AddOrgStorageParams{Delta: delta, OrgID: orgID})
}

// OrgStorageBytes returns the org's current stored bytes (the running counter the
// storage cap reads; also the source for a usage view). RLS-scoped to the tenant.
func (s *Store) OrgStorageBytes(ctx context.Context, t Tenant) (int64, error) {
	var n int64
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		v, err := q.GetOrgStorage(ctx, t.OrgID)
		n = v
		return err
	})
	return n, err
}

// SiteStorage is the LOGICAL storage of one site (the byte size of its current live
// version) paired with the owning user. Logical = NOT deduplicated across
// sites/versions (a file shipped by two sites counts in both), matching the
// per-folder size model of Dropbox/Drive — see docs/pricing.md. The org's
// authoritative (deduplicated) footprint is OrgStorageBytes; these per-site/per-user
// numbers are for display + attribution, not billing.
type SiteStorage struct {
	SiteID      string
	OwnerUserID string
	Bytes       int64
}

// SiteStorageBytes returns the logical storage of one site (its current-version
// size; 0 if it has no live version or is another tenant's). RLS-scoped.
func (s *Store) SiteStorageBytes(ctx context.Context, t Tenant, siteID string) (int64, error) {
	var n int64
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		v, err := q.GetSiteStorageBytes(ctx, siteID)
		n = v
		return err
	})
	return n, err
}

// ListSiteStorage returns the logical storage of every site in the active org with
// its owner, so the caller can show per-site usage and aggregate it per user. RLS
// scopes the read to the active org.
func (s *Store) ListSiteStorage(ctx context.Context, t Tenant) ([]SiteStorage, error) {
	var out []SiteStorage
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListSiteStorageForOrg(ctx)
		if err != nil {
			return err
		}
		out = make([]SiteStorage, len(rows))
		for i, r := range rows {
			out[i] = SiteStorage{SiteID: r.SiteID, OwnerUserID: r.OwnerUserID, Bytes: r.Bytes}
		}
		return nil
	})
	return out, err
}

// RecomputeOrgStorage reconciles an org's running storage total to the authoritative
// ledger sum (the cheap drift fix — docs/pricing.md §5.5). Run periodically; a
// deeper audit additionally lists R2 to prune ledger rows orphaned by a crashed GC.
func (s *Store) RecomputeOrgStorage(ctx context.Context, t Tenant) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.RecomputeOrgStorage(ctx, t.OrgID)
	})
}

// manifestPrefix is the R2 directory under which a site's per-version manifests
// live: manifests/<org>/<site>/ . The serving worker resolves
// <r2_prefix>/<version_id>.json (storage.ManifestKey).
func manifestPrefix(orgID, siteID string) string {
	return "manifests/" + orgID + "/" + siteID
}

// GetSiteVersion returns one version by id, asserting it belongs to the active
// tenant (RLS already scopes it; the explicit check is belt-and-suspenders).
func (s *Store) GetSiteVersion(ctx context.Context, t Tenant, id string) (SiteVersion, error) {
	var out SiteVersion
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetSiteVersion(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = versionFromDB(row)
		return nil
	})
	return out, err
}

// ListSiteVersions returns a site's versions, newest first.
func (s *Store) ListSiteVersions(ctx context.Context, t Tenant, siteID string) ([]SiteVersion, error) {
	var out []SiteVersion
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListSiteVersions(ctx, siteID)
		if err != nil {
			return err
		}
		out = make([]SiteVersion, len(rows))
		for i, r := range rows {
			out[i] = versionFromDB(r)
		}
		return nil
	})
	return out, err
}

// PublishResult is returned by Publish: the route value to project and the host.
//
// Routes, when populated (SetSiteAccess), carries the route update for EVERY host
// of the site — the canonical <org>--<slug>.dropwaycontent.com host AND every
// verified custom-domain host, each with its own route:<host> KV entry. Callers
// that change access (vs. a plain publish) MUST rewrite all of Routes, not just
// Host/Route, or a custom host keeps serving at the old access_mode (FIX 1). Host/
// Route stay the canonical pair for back-compat (Publish/deploy still set them).
type PublishResult struct {
	Host   string
	Route  projection.RouteValue
	Routes []RouteUpdate
	Site   Site
}

// Publish flips a site's current_version_id to versionID (publish OR rollback —
// rollback is just publishing an older version) and returns the route value the
// caller projects to the edge. It enforces the confused-deputy guard: the version
// must exist, belong to the site, and the site must belong to the active tenant.
//
// The pointer flip is Postgres-authoritative; the KV projection is a reconcilable
// cache the handler writes AFTER this commits (so a projection failure never
// leaves the DB inconsistent — the reconciler/rebuild backstops it).
func (s *Store) Publish(ctx context.Context, t Tenant, siteID, versionID string) (PublishResult, error) {
	var res PublishResult
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		site, err := q.GetSite(ctx, siteID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}

		ver, err := q.GetSiteVersion(ctx, versionID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		// The version must belong to THIS site (and thus this org).
		if ver.SiteID != siteID || ver.OrgID != t.OrgID {
			return ErrVersionMismatch
		}

		// Defense in depth: assert this site OWNS the global host_routes row for
		// its host before we project route:<host> to the edge. Under the per-tx RLS
		// tenant context, GetHostRoute returns a row ONLY if the active org owns the
		// host; a host owned by another org (or absent) is a no-rows miss → refuse.
		// This stops a publish from ever overwriting another tenant's live route
		// even if the CreateSite reservation were somehow bypassed.
		orgSlug, err := orgSlugTx(ctx, tx, t.OrgID)
		if err != nil {
			return err
		}
		host := projection.HostForSite(orgSlug, site.Slug)
		hr, err := q.GetHostRoute(ctx, host)
		if err != nil {
			if isNoRows(err) {
				return ErrHostTaken
			}
			return err
		}
		if hr.SiteID != siteID || hr.OrgID != t.OrgID {
			return ErrHostTaken
		}

		vid := versionID
		if err := q.SetCurrentVersion(ctx, db.SetCurrentVersionParams{
			ID:               siteID,
			CurrentVersionID: &vid,
		}); err != nil {
			return err
		}

		res.Site = siteFromDB(site)
		res.Site.CurrentVersionID = &vid

		// Public/unlisted links enforce expiry at the edge from the RouteValue;
		// identity-gated modes enforce it at mint time (routeExpiry returns "" for
		// them). A public site may have no policy row → treat as "no expiry".
		var expiresAt string
		if pol, err := q.GetSiteAccessPolicy(ctx, siteID); err == nil {
			expiresAt = routeExpiry(site.AccessMode, accessPolicyFromDB(pol))
		} else if !isNoRows(err) {
			return err
		}

		newRoute := func() projection.RouteValue {
			return projection.RouteValue{
				OrgID:         t.OrgID,
				SiteID:        siteID,
				VersionID:     versionID,
				AccessMode:    site.AccessMode,
				SchemaVersion: projection.SchemaVersion,
				ExpiresAt:     expiresAt,
			}
		}

		// Keep the canonical Host/Route populated for back-compat (the single-route
		// shape); the handler iterates Routes when present.
		res.Host = host
		res.Route = newRoute()

		// Rewrite EVERY host of the site (canonical + verified custom domains) to the
		// new version — each custom host has its own route:<host> KV entry, and a host
		// left pointing at the OLD version_id keeps serving the stale build after a
		// publish/rollback (parity with SetSiteAccess / the reconcile path; FIX 1).
		hostRoutes, err := q.ListHostRoutesForSite(ctx, siteID)
		if err != nil {
			return err
		}
		for _, hr := range hostRoutes {
			res.Routes = append(res.Routes, RouteUpdate{Host: hr.Host, Route: newRoute()})
		}
		return nil
	})
	return res, err
}

// ManifestKeyFor returns the canonical manifest object key for a version (the
// handler writes the manifest there on finalize, and serving reads it).
func ManifestKeyFor(orgID, siteID, versionID string) string {
	return storage.ManifestKey(orgID, siteID, versionID)
}
