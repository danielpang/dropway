// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/slug"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// Vanity hosts: an optional, org-claimed bare `<label>.<ContentDomain>` for one
// site — a single DNS label, so the platform wildcard cert covers it and (unlike
// a custom domain) no Cloudflare-for-SaaS provisioning or DCV verification is
// involved. Labels are first come, first served: the host_routes PRIMARY KEY is
// the global arbiter, exactly as it is for canonical hosts.

// VanityRegisterResult carries what the handler needs after a claim: the host,
// and — when the site is live — the route value to project to the edge.
type VanityRegisterResult struct {
	Host string
	// Registered is true when Route should be projected (the site has a live
	// version). A claim on a never-published site records the row only; the
	// publish path projects every non-preview host, including this one.
	Registered bool
	Route      projection.RouteValue
}

// RegisterVanityHost claims `<label>.<ContentDomain>` for the site. The label
// obeys the site-slug grammar (single lowercase DNS label, no `--`) and the
// reserved-word blocklist — a vanity label occupies the same namespace as
// canonical hosts, so the same rules apply. Errors: ErrInvalidSlug,
// ErrReservedSlug, ErrNotFound (site), ErrVanityExists (site already has one),
// ErrHostTaken (label claimed by anyone, any org, any kind).
func (s *Store) RegisterVanityHost(ctx context.Context, t Tenant, siteID, label string) (VanityRegisterResult, error) {
	var res VanityRegisterResult
	if !slug.Valid(label) {
		return res, ErrInvalidSlug
	}
	if IsReservedSlug(label) {
		return res, ErrReservedSlug
	}
	host := label + "." + projection.ContentDomain
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		site, err := q.GetSite(ctx, db.GetSiteParams{ID: siteID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if err := q.InsertVanityHostRoute(ctx, db.InsertVanityHostRouteParams{
			Host: host, OrgID: t.OrgID, SiteID: siteID,
		}); err != nil {
			if uniqueViolation(err, "host_routes_one_vanity_per_site") {
				return ErrVanityExists
			}
			if uniqueViolation(err, "") {
				return ErrHostTaken
			}
			return err
		}
		res.Host = host
		if site.CurrentVersionID == nil {
			return nil
		}
		// The site is live: build the full route value (same fields as the
		// publish path — routeValue is the single constructor) so the new host
		// serves immediately with the right expiry/banner/chat behavior.
		var expiresAt string
		if pol, err := q.GetSiteAccessPolicy(ctx, db.GetSiteAccessPolicyParams{SiteID: siteID, OrgID: t.OrgID}); err == nil {
			expiresAt = routeExpiry(site.AccessMode, accessPolicyFromDB(pol))
		} else if !isNoRows(err) {
			return err
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}
		chatID, err := chatIDForSiteTx(ctx, q, t.OrgID, siteID)
		if err != nil {
			return err
		}
		res.Registered = true
		res.Route = routeValue(t.OrgID, siteID, *site.CurrentVersionID, site.AccessMode, expiresAt, planTier, chatID)
		return nil
	})
	if err != nil {
		return VanityRegisterResult{}, err
	}
	return res, nil
}

// ReleaseVanityHost removes the site's vanity host, returning the host so the
// handler can drop its edge route. ErrNotFound when the site has none (or is
// not visible to the tenant).
func (s *Store) ReleaseVanityHost(ctx context.Context, t Tenant, siteID string) (string, error) {
	var host string
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		h, err := q.DeleteVanityHostForSite(ctx, db.DeleteVanityHostForSiteParams{
			SiteID: siteID, OrgID: t.OrgID,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		host = h
		return nil
	})
	if err != nil {
		return "", err
	}
	return host, nil
}

// VanityHostsForOrg returns every vanity host in the active org as a
// siteID → host map — one batched read so site listings can prefer vanity
// hosts in live_url without an N+1.
func (s *Store) VanityHostsForOrg(ctx context.Context, t Tenant) (map[string]string, error) {
	out := map[string]string{}
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListVanityHostsForOrg(ctx, t.OrgID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out[r.SiteID] = r.Host
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
