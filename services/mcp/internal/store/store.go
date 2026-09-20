// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package store is the MCP service's thin, org-scoped data layer. It is now
// deliberately minimal: the MCP server reads no site/skill/chat data from the
// database — every tool goes through the Go API over the caller's forwarded
// token. The ONLY database access this process makes is the per-request
// org mcp_enabled kill-switch check (and the /healthz reachability ping).
//
// Those queries run as the non-BYPASSRLS `dropway_app` role inside a transaction
// that first sets the per-request tenant context (SET LOCAL app.current_org_id /
// _user_id), so a token for org A can only ever see org A's rows, and they also
// carry an explicit org_id predicate: RLS is the backstop, not the only filter
// (a BYPASSRLS runtime role silently disables RLS — the July 2026 prod leak).
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

// ErrNotFound is returned when the org has no app.org_meta row yet (used by the
// gate to distinguish "not provisioned / default-enabled" from a genuine error),
// and surfaced by the tools when a slug doesn't resolve against the API.
var ErrNotFound = errors.New("mcp/store: not found")

// Store wraps the pgx pool (connected as dropway_app).
type Store struct{ pool *pgxpool.Pool }

// New builds a Store over an existing pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping verifies the database is reachable (acquires a connection and round-trips).
// Used by /healthz so a misconfigured or unreachable DATABASE_URL fails the health
// check — and thus the deploy — instead of silently 403ing every kill-switch check
// (the exact failure mode that hid a wrong DATABASE_URL in production).
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

// MCPEnabled reports the org's mcp_enabled switch (the admin/owner kill-switch),
// re-checked per request in the gate so a disable takes effect immediately. This
// is the one and only database read the MCP tool surface depends on.
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
