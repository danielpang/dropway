// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// CollectRoutesForOrg returns every live (published) route for one org as a
// host→RouteValue map. It runs under the org's RLS tenant context (no BYPASSRLS),
// so it is safe to call on the request-path connection. This is the building
// block of the "KV is rebuildable from Postgres" invariant: a caller that knows
// the set of org ids can wipe the edge projection and
// replay these maps to fully restore serving.
//
// Cross-org rebuild is a system job that reserves true BYPASSRLS.
// To keep the request-path connection non-bypass, the rebuild driver
// iterates org ids and calls this per org under that org's tenant context.
func (s *Store) CollectRoutesForOrg(ctx context.Context, orgID string) (map[string]projection.RouteValue, error) {
	t := Tenant{OrgID: orgID, UserID: orgID} // user id is unused by these reads; reuse org for a valid GUC
	routes := map[string]projection.RouteValue{}
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListPublishedSitesForRebuild(ctx)
		if err != nil {
			return err
		}
		// The org's plan tier rides on every route value (v3) so the serving Worker
		// can gate the free-tier attribution banner. It is per-org (same for all of
		// this org's routes), so read it once; GetPlanTier fail-softs to "free".
		planTier, err := q.GetPlanTier(ctx, orgID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.VersionID == nil {
				continue
			}
			// Project EVERY host registered for the site — the canonical
			// <org>--<slug>.dropwaycontent.com host AND every verified custom-domain
			// host — not just HostForSite(org,slug). Omitting custom domains leaves them absent
			// from the rebuilt projection, so they stop serving after a KV wipe and the
			// reconciler can never repair a stale custom-domain route (H4).
			//
			// ListHostRoutesForSite runs under this org's RLS tenant context and
			// returns only rows the site owns, so each host is owned by this org/site
			// (the same global-registry defense-in-depth the old GetHostRoute gave us).
			hostRoutes, err := q.ListHostRoutesForSite(ctx, r.SiteID)
			if err != nil {
				return err
			}
			// Public/unlisted link-expiry is part of the route value the Worker enforces
			// at the edge, so it MUST survive a rebuild/reprojection (omitting it would
			// silently un-expire a shared link). Read it per site exactly as Publish does;
			// a missing policy row means "no expiry".
			var expiresAt string
			if pol, perr := q.GetSiteAccessPolicy(ctx, r.SiteID); perr == nil {
				expiresAt = routeExpiry(r.AccessMode, accessPolicyFromDB(pol))
			} else if !isNoRows(perr) {
				return perr
			}
			for _, hr := range hostRoutes {
				routes[hr.Host] = projection.RouteValue{
					OrgID:         r.OrgID,
					SiteID:        r.SiteID,
					VersionID:     *r.VersionID,
					AccessMode:    r.AccessMode,
					SchemaVersion: projection.SchemaVersion,
					ExpiresAt:     expiresAt,
					PlanTier:      planTier,
				}
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

// RebuildAllOrgs is the DR drill: enumerate EVERY org
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
