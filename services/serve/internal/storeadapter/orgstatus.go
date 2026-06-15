// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package storeadapter

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGOrgStatusReader reads app.org_meta.org_status for the resolved org, under that
// org's OWN RLS tenant context (org_meta's policy gates on id = current_org_id).
// It implements serve.OrgStatusReader: the serving lifecycle calls it BEFORE
// streaming any content and 503s on a blocking status. Self-host's abuse/takedown
// suspension lever (migration 0013); cloud mirrors billing onto the same column.
//
// FAIL OPEN (matching the Worker's org-status read): a missing row or any read
// error returns a non-blocking result so a DB hiccup never takes sites offline —
// billing/the API stay authoritative for real entitlement.
type PGOrgStatusReader struct {
	pool *pgxpool.Pool
}

// NewOrgStatusReader builds a PGOrgStatusReader over the shipped_app pgxpool.
func NewOrgStatusReader(pool *pgxpool.Pool) *PGOrgStatusReader {
	return &PGOrgStatusReader{pool: pool}
}

// OrgStatus returns the org's content status ("active"/"suspended"/"over_limit"),
// or "" (non-blocking) when there is no row. A real read error is returned so the
// caller can fail OPEN.
func (r *PGOrgStatusReader) OrgStatus(ctx context.Context, orgID string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read the org's own row under its tenant context (org_meta RLS gates on id).
	if err := setTenant(ctx, tx, "", orgID); err != nil {
		return "", err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT org_status FROM app.org_meta WHERE id = $1`, orgID).Scan(&status)
	switch {
	case err == nil:
		return status, nil
	case errors.Is(err, pgx.ErrNoRows):
		// No org_meta row ⇒ nothing to block on (fail open).
		return "", nil
	default:
		return "", err
	}
}
