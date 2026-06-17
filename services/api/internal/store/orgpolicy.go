// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// OrgPolicy is the API-facing view of an org's sharing policy.
type OrgPolicy struct {
	OrgID                string
	AllowExternalSharing bool
	// MCPEnabled is whether the Dropway MCP server may serve this org. Default true
	// (org_meta.mcp_enabled); an admin/owner can disable it (SetMcpEnabled).
	MCPEnabled bool
}

// GetOrgPolicy returns the active org's sharing policy.
func (s *Store) GetOrgPolicy(ctx context.Context, t Tenant) (OrgPolicy, error) {
	var out OrgPolicy
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		meta, err := q.GetOrgMeta(ctx, t.OrgID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = OrgPolicy{
			OrgID:                meta.ID,
			AllowExternalSharing: meta.AllowExternalSharing,
			MCPEnabled:           meta.McpEnabled,
		}
		return nil
	})
	return out, err
}

// SetMcpEnabled toggles whether the Dropway MCP server may serve this org
// (admin/owner only — the caller re-checks the role against the member table) and
// returns the resulting policy. The MCP resource server ALSO re-checks
// org_meta.mcp_enabled per request, so disabling takes effect immediately even for
// already-issued OAuth access tokens.
func (s *Store) SetMcpEnabled(ctx context.Context, t Tenant, enabled bool) (OrgPolicy, error) {
	var out OrgPolicy
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.SetMcpEnabled(ctx, db.SetMcpEnabledParams{
			ID: t.OrgID, McpEnabled: enabled,
		}); err != nil {
			return err
		}
		meta, err := q.GetOrgMeta(ctx, t.OrgID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = OrgPolicy{
			OrgID:                meta.ID,
			AllowExternalSharing: meta.AllowExternalSharing,
			MCPEnabled:           meta.McpEnabled,
		}
		return nil
	})
	return out, err
}

// ReconcileResult reports the routes the caller must rewrite at the edge after a
// policy change. Downgraded carries the new (org_only) route for EACH host of
// every site whose access_mode was downgraded from public — the canonical
// <org>--<slug>.dropwaycontent.com host AND every verified custom-domain host,
// each of which has its own route:<host> KV entry. The caller PUTs each one (revoking
// the public/external grant by flipping the projected mode on all hosts).
type ReconcileResult struct {
	AllowExternalSharing bool
	Downgraded           []RouteUpdate
}

// RouteUpdate is a single content host whose route:<host> KV value must be
// rewritten (host + the new RouteValue). One per host_routes row.
type RouteUpdate struct {
	Host  string
	Route projection.RouteValue
}

// DowngradedRoute is the historical name for RouteUpdate, kept as an alias so
// existing callers/tests keep compiling.
type DowngradedRoute = RouteUpdate

// SetAllowExternalSharing toggles the org's allow_external_sharing policy
// (admin/owner only — the caller re-checks the role against the member table).
// When DISABLING (enabled=false) it RECONCILES in the same transaction:
//   - every public site is downgraded to org_only (revoking public visibility);
//   - every external-email allowlist grant in the org is deleted (revoking external
//     access).
//
// It returns the downgraded sites' new route values so the caller rewrites the KV
// projection within the propagation window (ARCHITECTURE.md §5.4/§6). Enabling the
// policy needs no reconcile (it only widens what is permitted).
func (s *Store) SetAllowExternalSharing(ctx context.Context, t Tenant, enabled bool) (ReconcileResult, error) {
	var res ReconcileResult
	res.AllowExternalSharing = enabled

	err := s.withTx(ctx, t, func(q *db.Queries) error {
		// Flip the policy first so the external-sharing trigger sees the new value
		// for any subsequent write in this tx.
		if err := q.SetAllowExternalSharing(ctx, db.SetAllowExternalSharingParams{
			ID: t.OrgID, AllowExternalSharing: enabled,
		}); err != nil {
			return err
		}

		if enabled {
			return nil // widening: no reconcile needed
		}

		// --- Disabling: downgrade public sites + drop external grants. ---
		publicSites, err := q.ListPublicSitesForOrg(ctx)
		if err != nil {
			return err
		}
		for _, site := range publicSites {
			if err := q.SetSiteAccessMode(ctx, db.SetSiteAccessModeParams{
				ID: site.ID, AccessMode: projection.AccessOrgOnly,
			}); err != nil {
				return err
			}
			// Mirror the mode into the policy row if one exists (keep them in sync).
			if _, err := q.GetSiteAccessPolicy(ctx, site.ID); err == nil {
				if _, err := q.UpsertSiteAccessPolicy(ctx, db.UpsertSiteAccessPolicyParams{
					SiteID: site.ID, OrgID: t.OrgID, Mode: projection.AccessOrgOnly,
				}); err != nil {
					return err
				}
			} else if !isNoRows(err) {
				return err
			}
			// Only sites with a live version have an edge route to rewrite.
			if site.CurrentVersionID == nil {
				continue
			}
			// Rewrite EVERY host of the site (canonical + verified custom domains),
			// not just the canonical one — each has its own route:<host> KV entry, and
			// a custom host left at 'public' keeps serving publicly after the policy
			// tightened (FIX 1 / ARCHITECTURE.md §6 revocation).
			hostRoutes, err := q.ListHostRoutesForSite(ctx, site.ID)
			if err != nil {
				return err
			}
			for _, hr := range hostRoutes {
				res.Downgraded = append(res.Downgraded, RouteUpdate{
					Host: hr.Host,
					Route: projection.RouteValue{
						OrgID:         t.OrgID,
						SiteID:        site.ID,
						VersionID:     *site.CurrentVersionID,
						AccessMode:    projection.AccessOrgOnly,
						SchemaVersion: projection.SchemaVersion,
					},
				})
			}
		}

		// Revoke every external-email allowlist grant in the org.
		if err := q.DeleteExternalAllowlistEntriesForOrg(ctx); err != nil {
			return err
		}
		return nil
	})
	return res, err
}
