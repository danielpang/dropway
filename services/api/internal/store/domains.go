// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// Domain is a custom hostname mapped to a site.
type Domain struct {
	ID           string
	OrgID        string
	SiteID       string
	Hostname     string
	VerifyStatus string
	TLSStatus    string
	CFHostnameID string
	DCVRecord    string
	CreatedAt    time.Time
}

// CreateDomainParams carries the inputs for registering a custom hostname row
// AFTER the Cloudflare custom hostname has been created (the handler calls the
// customdomains.Provider, then this with the returned id + DCV record).
type CreateDomainParams struct {
	SiteID       string
	Hostname     string
	CFHostnameID string
	DCVRecord    string
}

// PreflightCustomDomain is the server-side custom-domains ENTITLEMENT gate the
// AddDomain handler calls BEFORE creating the Cloudflare custom hostname, so a
// free-tier org is rejected (402, upgrade body) without ever provisioning a
// provider-side hostname. Custom domains are a PAID feature: the cloud provider
// caps the free tier at 0 and leaves every paid tier unlimited (OSS Unlimited
// always passes). It reads the org's live plan_tier under the tenant's RLS
// context and asks the pure policy whether creating one more is allowed.
//
// current is 0 because this is a BINARY entitlement, not a count band: the free
// cap is 0 (so the first add is always rejected) and paid is unlimited (always
// allowed), so there is no count/lock TOCTOU to guard — the plan tier is the only
// input and it doesn't change within the check.
func (s *Store) PreflightCustomDomain(ctx context.Context, t Tenant) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx, t.UserID, t.OrgID); err != nil {
		return err
	}
	q := db.New(tx)

	planTier, err := q.GetPlanTier(ctx, t.OrgID)
	if err != nil {
		return err
	}
	if err := s.quota.Allow(planTier, quota.ResourceCustomDomainPerOrg, 0); err != nil {
		return err // *quota.ExceededError → handler renders HTTP 402
	}
	return tx.Commit(ctx)
}

// CreateDomain inserts a pending custom-domain row for a site (confused-deputy
// guard: the site must belong to the active tenant). hostname is GLOBALLY UNIQUE,
// so a hostname already claimed by any org raises 23505 → ErrHostTaken.
func (s *Store) CreateDomain(ctx context.Context, t Tenant, p CreateDomainParams) (Domain, error) {
	hostname := normalizeHostname(p.Hostname)
	if hostname == "" {
		return Domain{}, ErrBadHostname
	}
	var out Domain
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		site, err := q.GetSite(ctx, db.GetSiteParams{ID: p.SiteID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}
		row, err := q.InsertDomain(ctx, db.InsertDomainParams{
			OrgID:        t.OrgID,
			SiteID:       p.SiteID,
			Hostname:     hostname,
			CfHostnameID: pgtype.Text{String: p.CFHostnameID, Valid: p.CFHostnameID != ""},
			DcvRecord:    pgtype.Text{String: p.DCVRecord, Valid: p.DCVRecord != ""},
		})
		if err != nil {
			if uniqueViolation(err, "") {
				return ErrHostTaken
			}
			return err
		}
		out = domainFromDB(row)
		return nil
	})
	return out, err
}

// GetDomain returns one custom-domain row by id (404 if absent/another tenant's).
func (s *Store) GetDomain(ctx context.Context, t Tenant, id string) (Domain, error) {
	var out Domain
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetDomain(ctx, db.GetDomainParams{ID: id, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = domainFromDB(row)
		return nil
	})
	return out, err
}

// ListDomainsForSite returns a site's custom domains.
func (s *Store) ListDomainsForSite(ctx context.Context, t Tenant, siteID string) ([]Domain, error) {
	var out []Domain
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		site, err := q.GetSite(ctx, db.GetSiteParams{ID: siteID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}
		rows, err := q.ListDomainsForSite(ctx, db.ListDomainsForSiteParams{SiteID: siteID, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		out = make([]Domain, len(rows))
		for i, r := range rows {
			out[i] = domainFromDB(r)
		}
		return nil
	})
	return out, err
}

// MarkDomainVerifiedResult reports the route the caller should write when a custom
// domain transitions to verified+TLS (the content host becomes the custom
// hostname). Route.Host is empty when the site has no live version yet.
type MarkDomainVerifiedResult struct {
	Domain Domain
	Host   string
	Route  projection.RouteValue
	// Registered reports whether host_routes was (re)written this call (i.e. the
	// transition to verified happened and the route should be projected).
	Registered bool
}

// UpdateDomainStatus advances a custom domain's verify/TLS status from a provider
// Status() poll. When it reaches verified (active) AND TLS is issued, it registers
// the custom hostname in the GLOBAL host_routes registry pointing at the site
// (asserting global uniqueness — a host already owned by another site/org →
// ErrHostTaken) and returns the route to project. Idempotent: a re-poll of an
// already-verified domain re-registers the same route (last-writer-wins for its
// own row) and returns Registered=true so the caller can re-push the projection.
func (s *Store) UpdateDomainStatus(ctx context.Context, t Tenant, id, verifyStatus, tlsStatus string) (MarkDomainVerifiedResult, error) {
	var res MarkDomainVerifiedResult
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		// Confirm ownership first (RLS already scopes, but be explicit).
		existing, err := q.GetDomain(ctx, db.GetDomainParams{ID: id, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if existing.OrgID != t.OrgID {
			return ErrNotFound
		}

		row, err := q.UpdateDomainStatus(ctx, db.UpdateDomainStatusParams{
			ID: id, VerifyStatus: verifyStatus, TlsStatus: tlsStatus, OrgID: t.OrgID,
		})
		if err != nil {
			if checkViolation(err) {
				return ErrInvalidDomainStatus
			}
			return err
		}
		res.Domain = domainFromDB(row)

		// On verified + TLS issued, write the global host route → site.
		if verifyStatus == DomainVerified && tlsStatus == TLSIssued {
			site, err := q.GetSite(ctx, db.GetSiteParams{ID: row.SiteID, OrgID: t.OrgID})
			if err != nil {
				if isNoRows(err) {
					return ErrNotFound
				}
				return err
			}
			if err := q.UpsertHostRoute(ctx, db.UpsertHostRouteParams{
				Host: row.Hostname, OrgID: t.OrgID, SiteID: row.SiteID,
			}); err != nil {
				if uniqueViolation(err, "") {
					return ErrHostTaken
				}
				return err
			}
			res.Host = row.Hostname
			res.Registered = true
			if site.CurrentVersionID != nil {
				// A newly-verified custom host serves the same chat surface as
				// the canonical one (v4 chat_id).
				chatID, err := chatIDForSiteTx(ctx, q, t.OrgID, row.SiteID)
				if err != nil {
					return err
				}
				res.Route = projection.RouteValue{
					OrgID:         t.OrgID,
					SiteID:        row.SiteID,
					VersionID:     *site.CurrentVersionID,
					AccessMode:    site.AccessMode,
					SchemaVersion: projection.SchemaVersion,
					ChatID:        chatID,
				}
			}
		}
		return nil
	})
	return res, err
}

// DeleteDomainResult carries the bits the handler needs to finish removal outside
// the DB: the hostname (to drop its edge route) and the Cloudflare custom-hostname
// id (to delete it at Cloudflare).
type DeleteDomainResult struct {
	Hostname     string
	CFHostnameID string
}

// DeleteDomain removes a custom domain for the active org: it deletes the app.domains
// row AND the global host_routes entry for that hostname in one tx, so serve/edge
// immediately stop resolving the host to the site. It returns the hostname +
// CF hostname id so the handler can also remove the edge route projection and the
// Cloudflare custom hostname. RLS scopes the writes to the active org; a domain the
// caller can't see is ErrNotFound.
func (s *Store) DeleteDomain(ctx context.Context, t Tenant, id string) (DeleteDomainResult, error) {
	var res DeleteDomainResult
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		// Confirm ownership first (RLS already scopes, but be explicit + give 404).
		existing, err := q.GetDomain(ctx, db.GetDomainParams{ID: id, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if existing.OrgID != t.OrgID {
			return ErrNotFound
		}
		row, err := q.DeleteDomain(ctx, db.DeleteDomainParams{ID: id, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		// Drop the global host route so the custom host stops serving immediately.
		if err := q.DeleteHostRoute(ctx, db.DeleteHostRouteParams{Host: row.Hostname, OrgID: t.OrgID}); err != nil {
			return err
		}
		res.Hostname = row.Hostname
		if row.CfHostnameID.Valid {
			res.CFHostnameID = row.CfHostnameID.String
		}
		return nil
	})
	return res, err
}

// ---------------------------------------------------------------------------
// constants, conversions, helpers
// ---------------------------------------------------------------------------

// Domain verify/TLS status values (mirror the app.domains CHECKs).
const (
	DomainPending   = "pending"
	DomainVerifying = "verifying"
	DomainVerified  = "verified"
	DomainFailed    = "failed"

	TLSPending = "pending"
	TLSIssued  = "issued"
	TLSFailed  = "failed"
)

var (
	// ErrBadHostname is returned for an empty/malformed custom hostname.
	ErrBadHostname = errors.New("store: invalid hostname")
	// ErrInvalidDomainStatus is returned when a status update violates the CHECK.
	ErrInvalidDomainStatus = errors.New("store: invalid domain status")
)

func domainFromDB(r db.AppDomain) Domain {
	d := Domain{
		ID:           r.ID,
		OrgID:        r.OrgID,
		SiteID:       r.SiteID,
		Hostname:     r.Hostname,
		VerifyStatus: r.VerifyStatus,
		TLSStatus:    r.TlsStatus,
		CreatedAt:    r.CreatedAt,
	}
	if r.CfHostnameID.Valid {
		d.CFHostnameID = r.CfHostnameID.String
	}
	if r.DcvRecord.Valid {
		d.DCVRecord = r.DcvRecord.String
	}
	return d
}

// normalizeHostname lowercases + trims a custom hostname.
func normalizeHostname(h string) string {
	return normalizeEmail(h) // same lowercase+trim; reuse the helper
}
