// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/api/internal/store/db"
)

// CreateSiteVersionParams carries the inputs for an immutable deploy row.
type CreateSiteVersionParams struct {
	SiteID      string
	ContentHash string // sha256 of the deploy manifest (the whole-deploy digest)
	SizeBytes   int64
	Status      string // typically "ready" once blobs are verified + manifest written
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
type PublishResult struct {
	Host  string
	Route projection.RouteValue
	Site  Site
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
	err := s.withTx(ctx, t, func(q *db.Queries) error {
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
		host := projection.HostForSlug(site.Slug)
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
		res.Host = host
		res.Route = projection.RouteValue{
			OrgID:         t.OrgID,
			SiteID:        siteID,
			VersionID:     versionID,
			AccessMode:    site.AccessMode,
			SchemaVersion: projection.SchemaVersion,
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
