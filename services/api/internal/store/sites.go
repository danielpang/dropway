// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/services/api/internal/store/db"
)

// reservedSlugs are subdomain labels that may not be used as a site slug — they
// collide with platform hosts or are confusingly authoritative-looking
// (ARCHITECTURE.md §10 "reserved-slug blocklist"). Checked at the API + here.
var reservedSlugs = map[string]struct{}{
	"www": {}, "app": {}, "api": {}, "admin": {}, "dashboard": {}, "static": {},
	"assets": {}, "status": {}, "blog": {}, "docs": {}, "help": {}, "support": {},
	"login": {}, "logout": {}, "auth": {}, "billing": {}, "internal": {},
	"cdn": {}, "mail": {},
}

// IsReservedSlug reports whether slug is on the reserved blocklist.
func IsReservedSlug(slug string) bool {
	_, ok := reservedSlugs[slug]
	return ok
}

// uniqueViolation reports whether err is a Postgres unique-constraint violation
// (SQLSTATE 23505), optionally on a named constraint.
func uniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// checkViolation reports whether err is a Postgres CHECK / trigger raise
// (SQLSTATE 23514). The external-sharing trigger (migration 0004) raises this to
// reject public/external grants under a false org policy.
func checkViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// Org is the app-side org anchor + its usage counters.
type Org struct {
	ID                   string
	PlanTier             string
	AllowExternalSharing bool
	DefaultVisibility    string
	SitesCount           int32
	MembersCount         int32
}

// Site is a shareable static site.
type Site struct {
	ID               string
	OrgID            string
	Slug             string
	OwnerUserID      string
	AccessMode       string
	CurrentVersionID *string
	CreatedAt        time.Time
}

// SiteVersion is an immutable, content-addressed deploy.
type SiteVersion struct {
	ID          string
	OrgID       string
	SiteID      string
	VersionNo   int32
	Status      string
	R2Prefix    string
	ContentHash string
	SizeBytes   int64
	CreatedBy   string
	CreatedAt   time.Time
}

// EnsureOrgProvisioned idempotently creates the org_meta + org_usage rows for the
// active tenant (the ensure-org-provisioned middleware runs this after auth). It
// is a no-op if the rows already exist. RLS scopes both upserts to the tenant.
func (s *Store) EnsureOrgProvisioned(ctx context.Context, t Tenant) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.EnsureOrgMeta(ctx, t.OrgID); err != nil {
			return err
		}
		return q.EnsureOrgUsage(ctx, t.OrgID)
	})
}

// CreateSite inserts a site for the active tenant after a reserved-slug check,
// bumping the org's sites_count in the same tx. The (org, slug) unique constraint
// is the race-safe guard against duplicate slugs.
//
// quota is checked by the caller (the handler) BEFORE this, via quota.Provider —
// the store stays cloud-free. accessMode defaults to "public".
func (s *Store) CreateSite(ctx context.Context, t Tenant, slug, accessMode string) (Site, error) {
	if IsReservedSlug(slug) {
		return Site{}, ErrReservedSlug
	}
	if accessMode == "" {
		accessMode = projection.AccessPublic
	}

	host := projection.HostForSlug(slug)

	var out Site
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.CreateSite(ctx, db.CreateSiteParams{
			OrgID:       t.OrgID,
			Slug:        slug,
			OwnerUserID: t.UserID,
			AccessMode:  accessMode,
		})
		if err != nil {
			if uniqueViolation(err, "sites_org_slug_key") {
				return ErrSlugTaken
			}
			if checkViolation(err) {
				// The external-sharing trigger rejected a public site for an org
				// whose allow_external_sharing policy is false (defense in depth).
				return ErrExternalSharingDisabled
			}
			return err
		}

		// Reserve the GLOBAL host transactionally (the cross-tenant hijack guard).
		// host_routes.host is a PRIMARY KEY, so a host already owned by ANY org —
		// even one this tenant can't see under RLS — raises 23505. We surface that
		// as ErrHostTaken so the WHOLE tx rolls back and the site is never created;
		// per-org slug uniqueness alone can't catch a cross-org collision because
		// the edge route:<host> namespace is global (projection.HostForSlug).
		if err := q.InsertHostRoute(ctx, db.InsertHostRouteParams{
			Host:   host,
			OrgID:  t.OrgID,
			SiteID: row.ID,
		}); err != nil {
			if uniqueViolation(err, "") {
				return ErrHostTaken
			}
			return err
		}

		if _, err := q.IncSiteCount(ctx, t.OrgID); err != nil {
			return err
		}
		out = siteFromDB(row)
		return nil
	})
	return out, err
}

// HostRoute is one row of the GLOBAL host registry (app.host_routes): a content
// host mapped to its owning (org, site). A site has at least its canonical
// <slug>.shippedusercontent.com host and, once a custom domain verifies, one row
// per verified custom hostname.
type HostRoute struct {
	Host   string
	OrgID  string
	SiteID string
}

// ListHostRoutesForSite returns EVERY host registered for a site in the global
// registry — the canonical <slug>.shippedusercontent.com host AND any verified
// custom-domain host. RLS scopes the read to the active org (a site the tenant
// doesn't own resolves to an empty list).
//
// Access-mode / policy changes MUST rewrite every one of these routes, not just
// the canonical one: a verified custom host has its own route:<host> KV entry,
// and leaving it at the old access_mode keeps the Worker serving the custom host
// under the OLD tier after the policy tightened (FIX 1 / ARCHITECTURE.md §6).
func (s *Store) ListHostRoutesForSite(ctx context.Context, t Tenant, siteID string) ([]HostRoute, error) {
	var out []HostRoute
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListHostRoutesForSite(ctx, siteID)
		if err != nil {
			return err
		}
		out = make([]HostRoute, len(rows))
		for i, r := range rows {
			out[i] = HostRoute{Host: r.Host, OrgID: r.OrgID, SiteID: r.SiteID}
		}
		return nil
	})
	return out, err
}

// ListSites returns the active tenant's sites (RLS scopes the query to the org).
func (s *Store) ListSites(ctx context.Context, t Tenant) ([]Site, error) {
	var out []Site
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListSites(ctx)
		if err != nil {
			return err
		}
		out = make([]Site, len(rows))
		for i, r := range rows {
			out[i] = siteFromDB(r)
		}
		return nil
	})
	return out, err
}

// GetSite returns one site by id (RLS makes other orgs' sites invisible → a miss
// surfaces as ErrNotFound, never a cross-tenant leak).
func (s *Store) GetSite(ctx context.Context, t Tenant, id string) (Site, error) {
	var out Site
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetSite(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = siteFromDB(row)
		return nil
	})
	return out, err
}

func siteFromDB(r db.AppSite) Site {
	return Site{
		ID:               r.ID,
		OrgID:            r.OrgID,
		Slug:             r.Slug,
		OwnerUserID:      r.OwnerUserID,
		AccessMode:       r.AccessMode,
		CurrentVersionID: r.CurrentVersionID,
		CreatedAt:        r.CreatedAt,
	}
}

func versionFromDB(r db.AppSiteVersion) SiteVersion {
	return SiteVersion{
		ID:          r.ID,
		OrgID:       r.OrgID,
		SiteID:      r.SiteID,
		VersionNo:   r.VersionNo,
		Status:      r.Status,
		R2Prefix:    r.R2Prefix,
		ContentHash: r.ContentHash,
		SizeBytes:   r.SizeBytes,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
	}
}
