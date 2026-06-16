// Package store is the Go API's data-access layer over the Go-owned `app` schema
// (docs/ARCHITECTURE.md §5/§8). It wraps the sqlc-generated queries (services/
// api/internal/store/db) in a Store that, for every call, BEGINs a pgx
// transaction, runs the SET LOCAL RLS tenant context from the verified claims
// (the same set_config semantics as internal/middleware/rlstx), executes the
// query, and commits.
//
// Why tx-per-call: RLS is the isolation backstop and depends entirely on the
// per-tx GUCs. SET LOCAL is transaction-scoped, so it is safe under Supavisor
// transaction-mode pooling — the GUCs cannot leak across pooled connections. The
// Go API connects as the non-BYPASSRLS `dropway_app` role; every tenant table is
// FORCE RLS. The Go API is the PRIMARY authz layer and RLS is the backstop, so
// sensitive writes here also re-derive a resource's org and assert it matches the
// active tenant (the confused-deputy guard, §5/§10).
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// Tenant carries the verified identifiers a request is scoped to. Constructed
// from *auth.Claims by the caller; the store sets these as the RLS GUCs.
type Tenant struct {
	OrgID  string
	UserID string
}

// Store is the tx-per-call data layer.
type Store struct {
	pool  *pgxpool.Pool
	quota quota.Provider
}

// New wraps a pgx pool with the open-core quota policy. The pool MUST connect as
// the non-BYPASSRLS dropway_app role (the runtime DATABASE_URL), never a
// superuser/bypass connection on a request path (§8 CI lint). quota is the pure
// policy (Unlimited in OSS, the cloud hard-caps under -tags cloud); the Store
// owns the race-safe mechanics (advisory lock + COUNT inside the create tx).
func New(pool *pgxpool.Pool, q quota.Provider) *Store {
	if q == nil {
		q = quota.Unlimited{}
	}
	return &Store{pool: pool, quota: q}
}

// Pool exposes the underlying pool for lifecycle management (Close) and the
// rebuild path, which reads cross-org under an explicit system context.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// withTx runs fn inside a transaction that first establishes the RLS tenant
// context for t, committing on success and rolling back on any error. fn
// receives a *db.Queries bound to the tx.
func (s *Store) withTx(ctx context.Context, t Tenant, fn func(q *db.Queries) error) error {
	return s.withTxRaw(ctx, t, func(_ pgx.Tx, q *db.Queries) error { return fn(q) })
}

// withTxRaw is withTx for callers that also need the raw pgx.Tx (e.g. to read the
// auth schema, which sqlc doesn't type — see orgSlugTx). fn receives both the tx
// and a *db.Queries bound to it, under the same RLS tenant context.
func (s *Store) withTxRaw(ctx context.Context, t Tenant, fn func(tx pgx.Tx, q *db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	// Roll back unless we explicitly commit. A no-op after commit.
	defer func() { _ = tx.Rollback(ctx) }()

	// The SET LOCAL tenant GUCs MUST be the first statements in the tx (rlstx
	// helper). Fail closed if claims lack org/user — RLS would otherwise deny
	// everything anyway, but we surface a clear error. txAdapter bridges pgx.Tx
	// (Exec → pgconn.CommandTag) to the middleware's pgx-agnostic TenantTx
	// (Exec → any) so we reuse the audited set_config semantics verbatim.
	if err := middleware.SetTenantContext(ctx, txAdapter{tx}, t.UserID, t.OrgID); err != nil {
		return err
	}

	q := db.New(tx)
	if err := fn(tx, q); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sentinel errors. Handlers map these onto httpx sentinels for the right status.
// ---------------------------------------------------------------------------

var (
	// ErrReservedSlug is returned when a requested slug is on the reserved
	// blocklist (§10 reserved-slug blocklist).
	ErrReservedSlug = errors.New("store: reserved slug")
	// ErrSlugTaken is returned when (org, slug) already exists.
	ErrSlugTaken = errors.New("store: slug already in use for this org")
	// ErrNotFound is returned when a row is absent (or invisible under RLS).
	ErrNotFound = errors.New("store: not found")
	// ErrVersionMismatch is returned when a version doesn't belong to the site
	// (the confused-deputy guard on publish).
	ErrVersionMismatch = errors.New("store: version does not belong to site")
	// ErrHostTaken is returned when publishing to a host already owned by a
	// different site (the global host registry guard; see projection.HostForSite).
	ErrHostTaken = errors.New("store: host already owned by another site")
	// ErrExternalSharingDisabled is returned when an action would create external/
	// public sharing while the org's allow_external_sharing policy is false — the
	// DB external-sharing trigger (migration 0004) rejected it in depth (§5.4/§10).
	// A brand-new org is fully internal until an admin opts in.
	ErrExternalSharingDisabled = errors.New("store: external sharing disabled for this org")
)

// isNoRows reports a pgx "no rows" miss (also what RLS-filtered reads look like).
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// txAdapter bridges pgx.Tx to internal/middleware.TenantTx. The middleware models
// only a row-less Exec returning `any` so it takes no hard pgx dependency; pgx's
// Exec returns a typed pgconn.CommandTag. This thin shim discards the tag value
// (the SET LOCAL helper ignores it anyway), letting the store reuse the audited
// set_config semantics rather than re-implementing them.
type txAdapter struct{ tx pgx.Tx }

func (a txAdapter) Exec(ctx context.Context, sql string, args ...any) (any, error) {
	return a.tx.Exec(ctx, sql, args...)
}
