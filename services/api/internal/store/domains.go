// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/projection"
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
		row, err := q.GetDomain(ctx, id)
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
		rows, err := q.ListDomainsForSite(ctx, siteID)
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
		existing, err := q.GetDomain(ctx, id)
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
			ID: id, VerifyStatus: verifyStatus, TlsStatus: tlsStatus,
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
			site, err := q.GetSite(ctx, row.SiteID)
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
				res.Route = projection.RouteValue{
					OrgID:         t.OrgID,
					SiteID:        row.SiteID,
					VersionID:     *site.CurrentVersionID,
					AccessMode:    site.AccessMode,
					SchemaVersion: projection.SchemaVersion,
				}
			}
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
