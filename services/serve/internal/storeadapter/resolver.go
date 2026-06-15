// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package storeadapter bridges Postgres + the revocation denylist to the serving
// plane's interfaces. The host resolver issues the SECURITY DEFINER
// app.resolve_host directly over a non-BYPASSRLS shipped_app pgxpool — the exact
// raw-pgx pattern used by services/api/internal/store/authz.go resolveHost (which
// is internal to services/api and so cannot be imported here). The spec explicitly
// permits issuing this SQL directly from services/serve.
package storeadapter

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/shipped/services/serve/internal/serve"
)

// RouteResolver resolves a content host to its routing identity via Postgres. It
// connects as the non-BYPASSRLS shipped_app role and uses ONLY the narrow,
// read-only, secret-free SECURITY DEFINER app.resolve_host for the cross-org
// lookup; the public/unlisted link-expiry is read from app.site_access_policy
// under the resolved org's own RLS tenant context.
type RouteResolver struct {
	pool *pgxpool.Pool
}

// NewRouteResolver builds a RouteResolver over a shipped_app pgxpool.
func NewRouteResolver(pool *pgxpool.Pool) *RouteResolver {
	return &RouteResolver{pool: pool}
}

// Resolve implements serve.RouteResolver. An unknown host OR a host with no live
// version (NULL current_version_id) maps to serve.ErrHostNotFound (fail closed ⇒
// 404). Any other error is surfaced (the handler also 404s on it, never a 5xx leak).
func (a *RouteResolver) Resolve(ctx context.Context, normalizedHost string) (serve.Route, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return serve.Route{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Default-deny the tenant GUCs before the definer call (resolve_host ignores RLS;
	// this avoids leaking any ambient tenant context into the follow-up read).
	if err := setTenant(ctx, tx, "", ""); err != nil {
		return serve.Route{}, err
	}

	var (
		host, siteID, orgID, slug, accessMode string
		versionID                             *string
	)
	row := tx.QueryRow(ctx,
		`SELECT host, site_id, org_id, slug, access_mode, version_id FROM app.resolve_host($1)`,
		normalizedHost)
	if err := row.Scan(&host, &siteID, &orgID, &slug, &accessMode, &versionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return serve.Route{}, serve.ErrHostNotFound
		}
		return serve.Route{}, err
	}
	if versionID == nil {
		// No live version → nothing to serve (mirror authz.go's NULL-version → 404).
		return serve.Route{}, serve.ErrHostNotFound
	}

	out := serve.Route{
		OrgID:      orgID,
		SiteID:     siteID,
		VersionID:  *versionID,
		AccessMode: accessMode,
	}

	// Read public/unlisted link-expiry under the SITE's own tenant context (RLS-scoped
	// to the resolved org). A missing policy is fine (no expiry); only a real read
	// error is fatal.
	if err := setTenant(ctx, tx, "", orgID); err != nil {
		return serve.Route{}, err
	}
	var expiresAt pgtype.Timestamptz
	row = tx.QueryRow(ctx,
		`SELECT expires_at FROM app.site_access_policy WHERE site_id = $1`, siteID)
	switch err := row.Scan(&expiresAt); {
	case err == nil:
		if expiresAt.Valid {
			t := expiresAt.Time
			out.ExpiresAt = &t
		}
	case errors.Is(err, pgx.ErrNoRows):
		// No access policy row → no edge expiry.
	default:
		return serve.Route{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return serve.Route{}, err
	}
	return out, nil
}

// setTenant sets the per-tx RLS GUCs (the same set_config semantics the API store
// uses). Empty values are a default-deny context — safe before a definer call.
func setTenant(ctx context.Context, tx pgx.Tx, userID, orgID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_user_id', $1, true)`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', $1, true)`, orgID); err != nil {
		return err
	}
	return nil
}
