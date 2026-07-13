// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"time"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// OrgPolicy is the API-facing view of an org's sharing policy.
type OrgPolicy struct {
	OrgID                string
	AllowExternalSharing bool
	// MCPEnabled is whether the Dropway MCP server may serve this org. Default true
	// (org_meta.mcp_enabled); an admin/owner can disable it (SetMcpEnabled).
	MCPEnabled bool
	// RequireMfa is whether every member must have two-factor authentication
	// enrolled before the dashboard serves them (org_meta.require_mfa, default
	// false). Admin/owner-set, business/enterprise only (SetRequireMfa).
	RequireMfa bool
}

// policyFromMeta maps the org_meta row to the API-facing policy view — the ONE
// place a new policy column gets picked up, so the three read/write paths below
// can't silently drift (a missed literal would compile fine and return a
// zero-valued field).
func policyFromMeta(meta db.GetOrgMetaRow) OrgPolicy {
	return OrgPolicy{
		OrgID:                meta.ID,
		AllowExternalSharing: meta.AllowExternalSharing,
		MCPEnabled:           meta.McpEnabled,
		RequireMfa:           meta.RequireMfa,
	}
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
		out = policyFromMeta(meta)
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
		out = policyFromMeta(meta)
		return nil
	})
	return out, err
}

// SetRequireMfa toggles org-wide MFA enforcement (admin/owner only — the caller
// re-checks the role against the member table) and returns the resulting policy.
//
// ENABLING is additionally gated on the org's plan tier via the quota provider
// (ResourceMfaEnforcement: business/enterprise only in the cloud build, always
// allowed on OSS) — same 0/unlimited entitlement shape as custom domains, checked
// in the same transaction as the write so the tier read is consistent. DISABLING
// is never gated: an org downgraded below business must always be able to turn
// enforcement off.
//
// Enforcement is next-request: the dashboard checks the flag per authenticated
// request and locks unenrolled members into the setup flow. No session
// revocation happens here.
func (s *Store) SetRequireMfa(ctx context.Context, t Tenant, enabled bool) (OrgPolicy, error) {
	var out OrgPolicy
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if enabled {
			planTier, err := q.GetPlanTier(ctx, t.OrgID)
			if err != nil {
				if isNoRows(err) {
					return ErrNotFound
				}
				return err
			}
			if err := s.quota.Allow(planTier, quota.ResourceMfaEnforcement, 0); err != nil {
				return err // *quota.ExceededError → handler renders HTTP 402
			}
		}
		if err := q.SetRequireMfa(ctx, db.SetRequireMfaParams{
			ID: t.OrgID, RequireMfa: enabled,
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
		out = policyFromMeta(meta)
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
// projection within the propagation window. Enabling the
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
			// Rewrite EVERY host of the site (canonical + verified custom domains),
			// not just the canonical one — each has its own route:<host> KV entry, and
			// a custom host left at 'public' keeps serving publicly after the policy
			// tightened (FIX 1). Preview hosts downgrade too (keeping their pinned
			// version + deadline) — they exist even for sites with no live version.
			hostRoutes, err := q.ListHostRoutesForSite(ctx, site.ID)
			if err != nil {
				return err
			}
			for _, hr := range hostRoutes {
				var rv projection.RouteValue
				switch {
				case hr.Kind == "preview" && hr.VersionID != nil:
					var expiresAt string
					if hr.ExpiresAt.Valid {
						expiresAt = hr.ExpiresAt.Time.UTC().Format(time.RFC3339)
					}
					rv = projection.RouteValue{
						OrgID:         t.OrgID,
						SiteID:        site.ID,
						VersionID:     *hr.VersionID,
						AccessMode:    projection.AccessOrgOnly,
						SchemaVersion: projection.SchemaVersion,
						ExpiresAt:     expiresAt,
					}
				case site.CurrentVersionID != nil:
					rv = projection.RouteValue{
						OrgID:         t.OrgID,
						SiteID:        site.ID,
						VersionID:     *site.CurrentVersionID,
						AccessMode:    projection.AccessOrgOnly,
						SchemaVersion: projection.SchemaVersion,
					}
				default:
					continue // no live version and not a preview → no route to rewrite
				}
				res.Downgraded = append(res.Downgraded, RouteUpdate{Host: hr.Host, Route: rv})
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
