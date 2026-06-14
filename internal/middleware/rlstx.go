package middleware

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielpang/shipped/internal/auth"
)

// The RLS tenant-context helper (docs/ARCHITECTURE.md §5, §8).
//
// The Go API connects to Postgres as the non-BYPASSRLS `shipped_app` role, with
// every tenant table under FORCE ROW LEVEL SECURITY. Isolation therefore depends
// on each request running inside a transaction that first sets the tenant GUCs:
//
//	SET LOCAL app.current_user_id = $1;
//	SET LOCAL app.current_org_id  = $2;
//
// The RLS policies are subquery-free and read these via
// current_setting('app.current_org_id', true). SET LOCAL is transaction-scoped,
// which is what makes this safe under Supavisor transaction-mode pooling — the
// GUCs are reset when the tx ends and cannot leak onto the next borrower of the
// pooled connection.
//
// We model only the tiny slice of pgx we need (a row-less Exec) behind the
// TenantTx interface so the helper is real, idiomatic, and unit-testable with a
// fake — no live database required to exercise the SET LOCAL contract. A
// *pgxpool.Tx satisfies TenantTx directly.

// TenantTx is the minimal transaction surface the helper needs. pgx's Tx (and
// pgxpool.Tx) satisfy it: Exec(ctx, sql, args...) (pgconn.CommandTag, error).
// We return `any` for the command tag so this package doesn't take a hard pgx
// dependency just for a value we discard.
type TenantTx interface {
	Exec(ctx context.Context, sql string, args ...any) (any, error)
}

// ErrMissingTenant is returned when the claims lack the identifiers required to
// scope a transaction. Authorizing with an empty org/user would let RLS fall
// open, so we fail closed instead.
var ErrMissingTenant = errors.New("middleware: claims missing user_id/org_id for RLS context")

// SetTenantContext runs the SET LOCAL statements that establish the per-request
// RLS tenant context inside an already-open transaction. It MUST be the first
// thing executed in the tx, before any tenant-scoped query.
//
// We bind the identifiers as parameters (not string-interpolated SQL) so a
// hostile claim value cannot inject SQL. `set_config(name, value, is_local=true)`
// is the parameterizable equivalent of `SET LOCAL name = value`; plain SET LOCAL
// does not accept bind parameters.
func SetTenantContext(ctx context.Context, tx TenantTx, userID, orgID string) error {
	if userID == "" || orgID == "" {
		return ErrMissingTenant
	}
	// Two set_config calls, both transaction-local (true).
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_user_id', $1, true)`, userID); err != nil {
		return fmt.Errorf("set app.current_user_id: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_org_id', $1, true)`, orgID); err != nil {
		return fmt.Errorf("set app.current_org_id: %w", err)
	}
	return nil
}

// SetTenantContextFromClaims is a convenience wrapper that derives the tenant
// identifiers from verified JWT claims. Claims are a fast hint; sensitive writes
// still re-check live tables (the confused-deputy guard in §5).
func SetTenantContextFromClaims(ctx context.Context, tx TenantTx, c *auth.Claims) error {
	if c == nil {
		return ErrMissingTenant
	}
	return SetTenantContext(ctx, tx, c.UserID(), c.OrgID)
}
