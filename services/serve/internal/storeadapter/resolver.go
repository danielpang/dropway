// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package storeadapter bridges Postgres + the revocation denylist to the serving
// plane's interfaces. The host resolver issues the SECURITY DEFINER
// app.resolve_host directly over a non-BYPASSRLS dropway_app pgxpool — the exact
// raw-pgx pattern used by services/api/internal/store/authz.go resolveHost (which
// is internal to services/api and so cannot be imported here). The spec explicitly
// permits issuing this SQL directly from services/serve.
package storeadapter

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/services/serve/internal/serve"
)

// isMissingColumn reports whether err is a Postgres undefined-column error
// (SQLSTATE 42703) — what selecting preview_expires_at from a pre-0010,
// 6-column app.resolve_host raises. Used to latch the resolver to the legacy
// query so a migration-lag deploy doesn't error every request.
func isMissingColumn(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42703"
}

// RouteResolver resolves a content host to its routing identity via Postgres. It
// connects as the non-BYPASSRLS dropway_app role and uses ONLY the narrow,
// read-only, secret-free SECURITY DEFINER app.resolve_host for the cross-org
// lookup; the public/unlisted link-expiry is read from app.site_access_policy
// under the resolved org's own RLS tenant context.
type RouteResolver struct {
	pool *pgxpool.Pool
	// legacyResolveHost is set once (atomically) if app.resolve_host still has the
	// pre-migration-0010 6-column signature (no preview_expires_at). It makes the
	// resolver tolerant of a deploy/rollback where the serve code is newer than the
	// applied schema: instead of erroring on EVERY request (taking all sites down),
	// it falls back to the 6-column query. Previews then rely on the separate
	// host_routes lookup below until the migration lands. 0 = unknown/new, 1 = legacy.
	legacyResolveHost atomic.Int32
}

// NewRouteResolver builds a RouteResolver over a dropway_app pgxpool.
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
		// previewExpires is set ONLY for a preview host (kind='preview'); it is the
		// preview deadline, returned by resolve_host so the hot path needs no second
		// query against host_routes on the 99% of traffic that isn't a preview.
		previewExpires pgtype.Timestamptz
	)
	if a.legacyResolveHost.Load() == 1 {
		// Pre-0010 schema: 6-column resolve_host (no preview_expires_at).
		row := tx.QueryRow(ctx,
			`SELECT host, site_id, org_id, slug, access_mode, version_id FROM app.resolve_host($1)`,
			normalizedHost)
		if err := row.Scan(&host, &siteID, &orgID, &slug, &accessMode, &versionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return serve.Route{}, serve.ErrHostNotFound
			}
			return serve.Route{}, err
		}
	} else {
		row := tx.QueryRow(ctx,
			`SELECT host, site_id, org_id, slug, access_mode, version_id, preview_expires_at FROM app.resolve_host($1)`,
			normalizedHost)
		if err := row.Scan(&host, &siteID, &orgID, &slug, &accessMode, &versionID, &previewExpires); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return serve.Route{}, serve.ErrHostNotFound
			}
			// If resolve_host lacks the preview column (migration 0010 not applied
			// yet — a deploy/rollback ordering gap), latch to the legacy query so we
			// don't error on every request. The current request retries below via a
			// fresh tx path; the aborted tx is rolled back by the deferred Rollback.
			if isMissingColumn(err) {
				a.legacyResolveHost.Store(1)
				return a.Resolve(ctx, normalizedHost)
			}
			return serve.Route{}, err
		}
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
	if previewExpires.Valid {
		t := previewExpires.Time
		out.ExpiresAt = &t
	}

	// Read public/unlisted link-expiry under the SITE's own tenant context (RLS-scoped
	// to the resolved org). A missing policy is fine (no expiry); only a real read
	// error is fatal. The edge enforces the EARLIER of the policy expiry and the
	// preview deadline set above.
	if err := setTenant(ctx, tx, "", orgID); err != nil {
		return serve.Route{}, err
	}
	var expiresAt pgtype.Timestamptz
	row := tx.QueryRow(ctx,
		`SELECT expires_at FROM app.site_access_policy WHERE site_id = $1`, siteID)
	switch err := row.Scan(&expiresAt); {
	case err == nil:
		if expiresAt.Valid {
			t := expiresAt.Time
			if out.ExpiresAt == nil || t.Before(*out.ExpiresAt) {
				out.ExpiresAt = &t
			}
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
