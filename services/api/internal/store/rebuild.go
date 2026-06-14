// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/services/api/internal/store/db"
)

// CollectRoutesForOrg returns every live (published) route for one org as a
// host→RouteValue map. It runs under the org's RLS tenant context (no BYPASSRLS),
// so it is safe to call on the request-path connection. This is the building
// block of the "KV is rebuildable from Postgres" invariant (ARCHITECTURE.md §13
// row 8): a caller that knows the set of org ids can wipe the edge projection and
// replay these maps to fully restore serving.
//
// Cross-org rebuild is a system job; ARCHITECTURE.md §8 reserves true BYPASSRLS
// for it. To keep the request-path connection non-bypass, the rebuild driver
// iterates org ids and calls this per org under that org's tenant context.
func (s *Store) CollectRoutesForOrg(ctx context.Context, orgID string) (map[string]projection.RouteValue, error) {
	t := Tenant{OrgID: orgID, UserID: orgID} // user id is unused by these reads; reuse org for a valid GUC
	routes := map[string]projection.RouteValue{}
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListPublishedSitesForRebuild(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.VersionID == nil {
				continue
			}
			host := projection.HostForSlug(r.Slug)
			// Defense in depth: only project a host this org/site actually OWNS in
			// the global registry. Under RLS, GetHostRoute returns a row only for a
			// host the active org owns; a missing or other-owned row means we must
			// NOT project route:<host> (it would clobber the rightful owner). This
			// keeps the rebuild faithful to the global host registry, not just the
			// per-org slug.
			hr, err := q.GetHostRoute(ctx, host)
			if err != nil {
				if isNoRows(err) {
					continue
				}
				return err
			}
			if hr.SiteID != r.SiteID || hr.OrgID != r.OrgID {
				continue
			}
			routes[host] = projection.RouteValue{
				OrgID:         r.OrgID,
				SiteID:        r.SiteID,
				VersionID:     *r.VersionID,
				AccessMode:    r.AccessMode,
				SchemaVersion: projection.SchemaVersion,
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return routes, nil
}

// RebuildProjection rebuilds the entire edge routing projection from Postgres for
// the supplied org ids and pushes it through w (the DR drill / drift reconciler).
// The caller supplies the org id set (in production, a system job enumerates
// app.org_meta; for Phase 1 / tests the set is passed in).
func (s *Store) RebuildProjection(ctx context.Context, w projection.Writer, orgIDs []string) error {
	all := map[string]projection.RouteValue{}
	for _, orgID := range orgIDs {
		routes, err := s.CollectRoutesForOrg(ctx, orgID)
		if err != nil {
			return err
		}
		for host, v := range routes {
			all[host] = v
		}
	}
	return w.RebuildFromDB(ctx, all)
}

// RebuildResult summarizes a DR rebuild: how many orgs were scanned and how many
// routes were re-pushed to the edge projection.
type RebuildResult struct {
	Orgs   int
	Routes int
}

// RebuildAllOrgs is the DR drill (ARCHITECTURE.md §13 row 8): enumerate EVERY org
// (via the SECURITY DEFINER app.all_org_ids(), not a BYPASSRLS pool), collect each
// org's live routes under its own RLS tenant context, and replay the whole set
// through w.RebuildFromDB — restoring serving after a KV/D1 wipe. Postgres is
// authoritative; the projection is a rebuildable cache.
func (s *Store) RebuildAllOrgs(ctx context.Context, w projection.Writer) (RebuildResult, error) {
	orgIDs, err := s.ListAllOrgIDs(ctx)
	if err != nil {
		return RebuildResult{}, err
	}
	all := map[string]projection.RouteValue{}
	for _, orgID := range orgIDs {
		routes, err := s.CollectRoutesForOrg(ctx, orgID)
		if err != nil {
			return RebuildResult{}, err
		}
		for host, v := range routes {
			all[host] = v
		}
	}
	if err := w.RebuildFromDB(ctx, all); err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Orgs: len(orgIDs), Routes: len(all)}, nil
}
