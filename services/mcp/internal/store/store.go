// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package store is the MCP service's thin, RLS-scoped data layer. It runs every
// query as the non-BYPASSRLS `dropway_app` role inside a transaction that first
// sets the per-request tenant context (SET LOCAL app.current_org_id / _user_id),
// so a token for org A can only ever see org A's rows — the same isolation the
// rest of the platform relies on. It can't import services/api/internal/store
// (Go internal-package rules), so it carries its own minimal queries.
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/middleware"
)

// Tenant is the authenticated caller's org + user (from the validated OAuth token).
type Tenant struct {
	OrgID  string
	UserID string
}

// Site is one of the org's deployed sites.
type Site struct {
	ID               string
	Slug             string
	AccessMode       string
	CurrentVersionID *string // nil until a version is published (site not live)
	Host             *string // a content/custom host for the site, if any
}

// siteCols is the shared SELECT list: site fields + one representative host.
const siteCols = `s.id, s.slug, s.access_mode, s.current_version_id,
	(SELECT hr.host FROM app.host_routes hr WHERE hr.site_id = s.id ORDER BY hr.host LIMIT 1)`

// ErrNotFound is returned when a site slug doesn't resolve under the tenant.
var ErrNotFound = errors.New("mcp/store: not found")

// Store wraps the pgx pool (connected as dropway_app).
type Store struct{ pool *pgxpool.Pool }

// New builds a Store over an existing pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping verifies the database is reachable (acquires a connection and round-trips).
// Used by /healthz so a misconfigured or unreachable DATABASE_URL fails the health
// check — and thus the deploy — instead of silently serving 403s on every DB-backed
// request (the exact failure mode that hid a wrong DATABASE_URL in production).
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// withTx runs fn inside a tx with the tenant RLS context set. Read-only here, so
// it always rolls back (no writes to commit) — RLS still applies to the reads.
func (s *Store) withTx(ctx context.Context, t Tenant, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Bridge pgx.Tx (Exec → pgconn.CommandTag) to the middleware's pgx-agnostic
	// TenantTx (Exec → any) so we reuse the audited set_config RLS semantics.
	if err := middleware.SetTenantContext(ctx, txAdapter{tx}, t.UserID, t.OrgID); err != nil {
		return err
	}
	return fn(tx)
}

// txAdapter bridges pgx.Tx to middleware.TenantTx.
type txAdapter struct{ tx pgx.Tx }

func (a txAdapter) Exec(ctx context.Context, sql string, args ...any) (any, error) {
	return a.tx.Exec(ctx, sql, args...)
}

// MCPEnabled reports the org's mcp_enabled switch (the admin/owner kill-switch).
func (s *Store) MCPEnabled(ctx context.Context, t Tenant) (bool, error) {
	var enabled bool
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT mcp_enabled FROM app.org_meta WHERE id = $1`, t.OrgID).Scan(&enabled)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return enabled, err
}

// ListSites returns the org's sites (RLS-filtered to the tenant).
func (s *Store) ListSites(ctx context.Context, t Tenant) ([]Site, error) {
	var sites []Site
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+siteCols+` FROM app.sites s ORDER BY s.slug`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var st Site
			if err := rows.Scan(&st.ID, &st.Slug, &st.AccessMode, &st.CurrentVersionID, &st.Host); err != nil {
				return err
			}
			sites = append(sites, st)
		}
		return rows.Err()
	})
	return sites, err
}

// SiteBySlug resolves one site by slug under the tenant, or ErrNotFound.
func (s *Store) SiteBySlug(ctx context.Context, t Tenant, slug string) (Site, error) {
	var st Site
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT `+siteCols+` FROM app.sites s WHERE s.slug = $1`, slug).
			Scan(&st.ID, &st.Slug, &st.AccessMode, &st.CurrentVersionID, &st.Host)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Site{}, ErrNotFound
	}
	return st, err
}
