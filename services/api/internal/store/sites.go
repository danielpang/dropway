// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// PreflightMembers is the server-side members_per_org cap check the dashboard's
// invite path calls BEFORE adding a member (Better Auth inserts the member row in
// its own tx, so this is a preflight, not the insert itself). It returns a
// *quota.ExceededError (→ 402) when the org is at/over its member cap. Seats are
// currently FREE (docs/pricing.md): both OSS Unlimited AND the cloud provider
// return nil for ResourceMemberPerOrg, so this preflight always passes today. It
// stays wired as the enforcement seam so seat policy can be re-tightened in the
// cloud provider alone, with no handler/store change.
//
// Race-safe WITHIN our path: everything runs in ONE tx that takes the per-org
// advisory lock (LockOrgMemberQuota) first, so two concurrent preflights for the
// same org serialize (mirrors CreateSite's per-user lock) instead of both reading a
// stale count. The "current" usage counts live members PLUS pending invitations
// (reserved seats), so a burst of invites before any are accepted can't overshoot
// the cap. Both Better-Auth-owned tables are outside app RLS, so they're scoped
// explicitly by org and tolerate being absent (self-host pre-Better-Auth → 0).
func (s *Store) PreflightMembers(ctx context.Context, t Tenant) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx, t.UserID, t.OrgID); err != nil {
		return err
	}
	q := db.New(tx)

	// Serialize concurrent preflights for this org for the rest of the tx (wires the
	// previously-dead LockOrgMemberQuota advisory-lock query).
	if err := q.LockOrgMemberQuota(ctx, t.OrgID); err != nil {
		return err
	}
	planTier, err := q.GetPlanTier(ctx, t.OrgID)
	if err != nil {
		return err
	}

	current, err := countMembersAndPending(ctx, tx, t.OrgID)
	if err != nil {
		return err
	}
	if err := s.quota.Allow(planTier, quota.ResourceMemberPerOrg, current); err != nil {
		return err // *quota.ExceededError → handler renders HTTP 402
	}
	return tx.Commit(ctx)
}

// countMembersAndPending returns live members + pending invitations for an org
// (the "reserved seats" usage the member cap is measured against). Each Better-Auth
// table is counted on the supplied tx and tolerates a missing schema (self-host that
// hasn't migrated Better Auth → that count is 0).
func countMembersAndPending(ctx context.Context, tx pgx.Tx, orgID string) (int64, error) {
	count := func(query string) (int64, error) {
		var n int64
		if err := tx.QueryRow(ctx, query, orgID).Scan(&n); err != nil {
			if isUndefinedTable(err) {
				return 0, nil
			}
			return 0, err
		}
		return n, nil
	}
	members, err := count(`SELECT count(*) FROM identity.member WHERE "organizationId" = $1`)
	if err != nil {
		return 0, err
	}
	pending, err := count(`SELECT count(*) FROM identity.invitation WHERE "organizationId" = $1 AND status = 'pending'`)
	if err != nil {
		return 0, err
	}
	return members + pending, nil
}

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

// ErrOrgSlugNotFound is returned when an org has no identity.organization row to read
// a slug from — the canonical content host can't be formed, so the operation
// fails rather than emitting a malformed host.
var ErrOrgSlugNotFound = errors.New("store: org slug not found")

// OrgSlug returns the org's slug from identity.organization (the Better-Auth-owned
// identity table the dashboard writes; dropway_app has SELECT via migration 0012).
// It is the org half of the canonical content host (projection.HostForSite). The
// read runs inside the active tenant's tx context — identity.organization has no RLS,
// so the row resolves by id directly. A missing row is surfaced as
// ErrOrgSlugNotFound (the host can't be formed). It is raw pgx because the auth
// schema is outside the sqlc-typed app schema (mirrors resolveHost in authz.go).
func (s *Store) OrgSlug(ctx context.Context, t Tenant) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, t.UserID, t.OrgID); err != nil {
		return "", err
	}
	slug, err := orgSlugTx(ctx, tx, t.OrgID)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return slug, nil
}

// orgSlugTx reads identity.organization.slug for orgID on an already-open tx, so the
// canonical host can be formed inside the same tenant-context transaction a store
// write already runs in (no extra round-trip / second connection). A missing row
// → ErrOrgSlugNotFound.
func orgSlugTx(ctx context.Context, tx pgx.Tx, orgID string) (string, error) {
	var slug string
	err := tx.QueryRow(ctx, `SELECT slug FROM identity.organization WHERE id = $1`, orgID).Scan(&slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrOrgSlugNotFound
		}
		return "", err
	}
	if slug == "" {
		return "", ErrOrgSlugNotFound
	}
	return slug, nil
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
// the store stays cloud-free. An empty accessMode inherits the ORG's
// default_visibility (org_only for a fresh org — internal-by-default, §2.2/§5.4),
// NOT public: a brand-new org has allow_external_sharing=false, and a public site
// would be 403'd by the external-sharing trigger.
func (s *Store) CreateSite(ctx context.Context, t Tenant, slug, accessMode string) (Site, error) {
	if IsReservedSlug(slug) {
		return Site{}, ErrReservedSlug
	}

	var out Site
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		// The canonical content host is ORG-NAMESPACED: <orgSlug>--<slug>. Read the
		// org slug under the active tenant context (identity.organization, outside RLS) so
		// the global host registry reserves the org-scoped host, not a bare slug.
		orgSlug, err := orgSlugTx(ctx, tx, t.OrgID)
		if err != nil {
			return err
		}
		host := projection.HostForSite(orgSlug, slug)

		// Default a new site's visibility to the org's default_visibility (read under
		// the tenant context). Falls back to org_only if the org_meta row somehow has
		// no value — never public, so a fresh internal org can always create a site.
		if accessMode == "" {
			om, err := q.GetOrgMeta(ctx, t.OrgID)
			if err != nil {
				return err
			}
			accessMode = om.DefaultVisibility
			if accessMode == "" {
				accessMode = projection.AccessOrgOnly
			}
		}

		// Race-safe per-ORG site cap (docs/pricing.md "pay for sites, not seats"):
		// take a per-org advisory lock for the rest of the tx, then COUNT → policy →
		// INSERT as one critical section, so two concurrent creates anywhere in the
		// org can't both read current=N and both insert. The count is POOLED across
		// all members (seats are free). OSS = Unlimited (always nil); cloud = the
		// hard-cap bands (Free 10 → Pro 100 → Enterprise unlimited).
		if err := q.LockOrgSiteQuota(ctx, t.OrgID); err != nil {
			return err
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}
		current, err := q.CountSitesForOrg(ctx, t.OrgID)
		if err != nil {
			return err
		}
		if err := s.quota.Allow(planTier, quota.ResourceSitePerOrg, current); err != nil {
			return err // *quota.ExceededError → handler renders HTTP 402
		}

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
		// the edge route:<host> namespace is global (projection.HostForSite).
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
// <org>--<slug>.dropwaycontent.com host and, once a custom domain verifies,
// one row per verified custom hostname.
type HostRoute struct {
	Host   string
	OrgID  string
	SiteID string
}

// ListHostRoutesForSite returns EVERY host registered for a site in the global
// registry — the canonical <org>--<slug>.dropwaycontent.com host AND any
// verified custom-domain host. RLS scopes the read to the active org (a site the tenant
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
