// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Member is a row of the Better Auth organization `member` table, READ-ONLY to the
// Go API (Better Auth owns + migrates it; the Go API only reads role for authz and
// re-checks it). It lives in the `identity` schema.
type Member struct {
	UserID string
	OrgID  string
	Role   string
}

// Roles (Better Auth Organization plugin). owner ⊇ admin ⊇ member.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// ErrNoMembership is returned when the caller has no member row in the org (so they
// can't be authorized for an org-scoped action).
var ErrNoMembership = errors.New("store: no membership in org")

// IsAdminRole reports whether role is admin-or-above (owner counts).
func IsAdminRole(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

// MemberRole loads the caller's CURRENT role from the Better Auth member table
// (identity.member), NOT the JWT claim — the live re-check that gates admin-only
// actions (confused-deputy guard, [HIGH]). It runs under the
// active tenant context but reads the identity schema directly (raw pgx; the auth
// schema is outside sqlc's app scope).
//
// The Better Auth Organization plugin stores members as
// identity.member("organizationId","userId","role"). If the identity schema/table is not
// present (a self-host that hasn't run Better Auth yet, or a DB-less test), it
// returns ErrAuthSchemaUnavailable so the caller can decide how to degrade.
func (s *Store) MemberRole(ctx context.Context, orgID, userID string) (string, error) {
	if orgID == "" || userID == "" {
		return "", ErrNoMembership
	}
	// The identity tables are NOT under the app RLS policies; we filter explicitly by
	// (org, user). Read directly on the pool (no app tenant context needed).
	var role string
	row := s.pool.QueryRow(ctx,
		`SELECT "role" FROM identity.member WHERE "organizationId" = $1 AND "userId" = $2`,
		orgID, userID)
	if err := row.Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoMembership
		}
		if isUndefinedTable(err) {
			return "", ErrAuthSchemaUnavailable
		}
		return "", err
	}
	return role, nil
}

// requireLiveMembership confirms the viewer is a CURRENT member of orgID per the
// live identity.member table — the authoritative org_only mint check (FIX 2). A missing
// member row → ErrNotOrgMember (mapped to 403). If the Better Auth member table is
// unavailable, the mint is REFUSED (ErrNotOrgMember), not silently granted: an
// org_only token is an access grant, so we fail closed when membership can't be
// confirmed (the strict default mirrors the JWT-fallback policy, [HIGH]).
func (s *Store) requireLiveMembership(ctx context.Context, orgID, userID string) error {
	if _, err := s.MemberRole(ctx, orgID, userID); err != nil {
		if errors.Is(err, ErrNoMembership) || errors.Is(err, ErrAuthSchemaUnavailable) {
			return ErrNotOrgMember
		}
		return err
	}
	return nil
}

// ListMembers returns the org's members from the Better Auth member table. RLS does
// not apply to the identity schema, so we scope explicitly by organizationId (the
// caller is already authorized for orgID via its verified claim + this read is only
// of its own org).
func (s *Store) ListMembers(ctx context.Context, orgID string) ([]Member, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT "userId", "organizationId", "role" FROM identity.member WHERE "organizationId" = $1 ORDER BY "role", "userId"`,
		orgID)
	if err != nil {
		if isUndefinedTable(err) {
			return nil, ErrAuthSchemaUnavailable
		}
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.OrgID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ErrAuthSchemaUnavailable is returned when the Better Auth identity.member table is
// absent (Better Auth hasn't migrated yet / DB-less). Callers fall back to the JWT
// role claim with a warning rather than hard-failing.
var ErrAuthSchemaUnavailable = errors.New("store: identity.member table unavailable")

// isUndefinedTable reports a Postgres 42P01 (undefined_table / schema) error.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	// 42P01 undefined_table, 3F000 invalid_schema_name.
	return pgErr.Code == "42P01" || pgErr.Code == "3F000"
}
