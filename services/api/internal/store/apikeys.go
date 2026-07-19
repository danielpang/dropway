// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// APIKey is the management-facing view of an app.api_keys row (never carries the
// hash or the secret). It backs GET /v1/api-keys and the create response's
// metadata (the create response additionally carries the one-time secret, which
// the store returns separately and never persists in plaintext).
type APIKey struct {
	ID         string
	OrgID      string
	CreatedBy  string
	Name       string
	KeyPrefix  string
	Scopes     []string
	SiteID     *string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	CreatedAt  time.Time
	RevokedAt  *time.Time
	RevokedBy  *string
}

// APIKeyPrincipal is what the auth boundary resolves a presented key secret to
// (via the SECURITY DEFINER app.resolve_api_key, which bypasses RLS because no
// tenant context exists yet). It carries the raw liveness fields + the org
// governance fields; the auth path applies the fail-closed policy and maps every
// failure to a uniform 401.
type APIKeyPrincipal struct {
	ID             string
	OrgID          string
	CreatedBy      string
	ExpiresAt      *time.Time
	RevokedAt      *time.Time
	OrgStatus      string
	APIKeysEnabled bool
}

// CreateAPIKeyParams is the input to CreateAPIKey. KeyHash is the sha256 of the
// (already-discarded) full secret; KeyPrefix is the non-secret display handle.
type CreateAPIKeyParams struct {
	Name      string
	KeyHash   string
	KeyPrefix string
	ExpiresAt *time.Time // nil = non-expiring (the v1 default)
	Ctx       audit.Context
}

// ResolveAPIKey looks up a key by its sha256 hash at the auth boundary. It runs on
// the pool with NO tenant context (the whole point is to discover the org), using
// the SECURITY DEFINER app.resolve_api_key() to bypass RLS — the same pattern as
// resolveHost for the password/mint path. A miss (unknown hash) returns
// ErrNotFound; the caller maps that AND every liveness failure to a uniform 401 so
// the boundary is not an existence oracle.
func (s *Store) ResolveAPIKey(ctx context.Context, keyHash string) (APIKeyPrincipal, error) {
	var p APIKeyPrincipal
	var expiresAt, revokedAt *time.Time
	row := s.pool.QueryRow(ctx,
		`SELECT id, org_id, created_by, expires_at, revoked_at, org_status, api_keys_enabled
		   FROM app.resolve_api_key($1)`, keyHash)
	if err := row.Scan(&p.ID, &p.OrgID, &p.CreatedBy, &expiresAt, &revokedAt,
		&p.OrgStatus, &p.APIKeysEnabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKeyPrincipal{}, ErrNotFound
		}
		return APIKeyPrincipal{}, err
	}
	p.ExpiresAt = expiresAt
	p.RevokedAt = revokedAt
	return p, nil
}

// TouchAPIKeyLastUsed best-effort stamps last_used_at for a key, throttled in SQL
// to at most once per 5 minutes so a keyed request doesn't become a write every
// time. Runs under the resolved org's tenant context (RLS-scoped). Errors are the
// caller's to swallow — a failed hygiene stamp must never fail a request.
func (s *Store) TouchAPIKeyLastUsed(ctx context.Context, orgID, userID, keyID string) error {
	return s.withTx(ctx, Tenant{OrgID: orgID, UserID: userID}, func(q *db.Queries) error {
		return q.TouchAPIKeyLastUsed(ctx, db.TouchAPIKeyLastUsedParams{ID: keyID, OrgID: orgID})
	})
}

// CreateAPIKey mints a key for the active org, attributed to t.UserID (created_by),
// and records an api_key.create audit row in the SAME tx. It returns the management
// view; the caller pairs it with the one-time secret it generated. Key management
// is session-only (a key can't mint a key — enforced by the handler's admin gate),
// so the audit actor is the session user, not a token.
func (s *Store) CreateAPIKey(ctx context.Context, t Tenant, p CreateAPIKeyParams) (APIKey, error) {
	var out APIKey
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.CreateAPIKey(ctx, db.CreateAPIKeyParams{
			OrgID:     t.OrgID,
			CreatedBy: t.UserID,
			Name:      p.Name,
			KeyHash:   p.KeyHash,
			KeyPrefix: p.KeyPrefix,
			ExpiresAt: timestamptzFromPtr(p.ExpiresAt),
		})
		if err != nil {
			return err
		}
		out = apiKeyFromCreateRow(row)
		if _, err := writeAuditTx(ctx, q, t.OrgID, AuditRecord{
			Action:   audit.ActionAPIKeyCreate,
			Target:   "api_key:" + out.ID,
			Metadata: map[string]any{"name": out.Name, "key_prefix": out.KeyPrefix},
			Ctx:      p.Ctx,
		}); err != nil {
			return err
		}
		return nil
	})
	return out, err
}

// ListAPIKeys returns the active org's keys, newest first (RLS-scoped). Metadata +
// prefix only — never the hash.
func (s *Store) ListAPIKeys(ctx context.Context, t Tenant) ([]APIKey, error) {
	var out []APIKey
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListAPIKeys(ctx, t.OrgID)
		if err != nil {
			return err
		}
		out = make([]APIKey, len(rows))
		for i, r := range rows {
			out[i] = apiKeyFromListRow(r)
		}
		return nil
	})
	return out, err
}

// RevokeAPIKey revokes a key by id in the active org (idempotent — re-revoking
// keeps the first revocation's timestamp/actor) and records an api_key.revoke audit
// row in the same tx. A missing/invisible key → ErrNotFound.
func (s *Store) RevokeAPIKey(ctx context.Context, t Tenant, id string, ctxProv audit.Context) (APIKey, error) {
	var out APIKey
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		revokedBy := t.UserID
		row, err := q.RevokeAPIKey(ctx, db.RevokeAPIKeyParams{
			ID: id, OrgID: t.OrgID, RevokedBy: &revokedBy,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = apiKeyFromRevokeRow(row)
		if _, err := writeAuditTx(ctx, q, t.OrgID, AuditRecord{
			Action:   audit.ActionAPIKeyRevoke,
			Target:   "api_key:" + out.ID,
			Metadata: map[string]any{"name": out.Name, "key_prefix": out.KeyPrefix},
			Ctx:      ctxProv,
		}); err != nil {
			return err
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// conversions
// ---------------------------------------------------------------------------

func apiKeyFromCreateRow(r db.CreateAPIKeyRow) APIKey {
	return APIKey{
		ID: r.ID, OrgID: r.OrgID, CreatedBy: r.CreatedBy, Name: r.Name,
		KeyPrefix: r.KeyPrefix, Scopes: r.Scopes, SiteID: r.SiteID,
		LastUsedAt: ptrFromTimestamptz(r.LastUsedAt), ExpiresAt: ptrFromTimestamptz(r.ExpiresAt),
		CreatedAt: r.CreatedAt, RevokedAt: ptrFromTimestamptz(r.RevokedAt), RevokedBy: r.RevokedBy,
	}
}

func apiKeyFromListRow(r db.ListAPIKeysRow) APIKey {
	return APIKey{
		ID: r.ID, OrgID: r.OrgID, CreatedBy: r.CreatedBy, Name: r.Name,
		KeyPrefix: r.KeyPrefix, Scopes: r.Scopes, SiteID: r.SiteID,
		LastUsedAt: ptrFromTimestamptz(r.LastUsedAt), ExpiresAt: ptrFromTimestamptz(r.ExpiresAt),
		CreatedAt: r.CreatedAt, RevokedAt: ptrFromTimestamptz(r.RevokedAt), RevokedBy: r.RevokedBy,
	}
}

func apiKeyFromRevokeRow(r db.RevokeAPIKeyRow) APIKey {
	return APIKey{
		ID: r.ID, OrgID: r.OrgID, CreatedBy: r.CreatedBy, Name: r.Name,
		KeyPrefix: r.KeyPrefix, Scopes: r.Scopes, SiteID: r.SiteID,
		LastUsedAt: ptrFromTimestamptz(r.LastUsedAt), ExpiresAt: ptrFromTimestamptz(r.ExpiresAt),
		CreatedAt: r.CreatedAt, RevokedAt: ptrFromTimestamptz(r.RevokedAt), RevokedBy: r.RevokedBy,
	}
}

// timestamptzFromPtr converts an optional time to the pgtype the generated params
// expect: nil → NULL (e.g. a non-expiring key).
func timestamptzFromPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// ptrFromTimestamptz converts a nullable timestamptz column back to an optional
// time for the API-facing view (invalid/NULL → nil).
func ptrFromTimestamptz(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}
