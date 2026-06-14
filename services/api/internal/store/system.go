// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import "context"

// ListAllOrgIDs enumerates EVERY org id for the cross-org system jobs — the DR
// projection rebuild and the R2 version GC (ARCHITECTURE.md §12/§13). The runtime
// shipped_app role is non-BYPASSRLS, so a plain SELECT over app.org_meta would be
// tenant-scoped to nothing; we call the narrow SECURITY DEFINER app.all_org_ids()
// (migrations 0008/0009), which returns ONLY ids (no secrets), mirroring the
// resolve_host escalation pattern. The per-org route/blob reads driven from this
// list still run under each org's own RLS tenant context, so only the id
// ENUMERATION is elevated.
//
// OPS-MODE GATE (migration 0009): app.all_org_ids() RAISES unless the caller has set
// app.ops_mode='1' — so a normal request (which never sets it) can't enumerate all
// org ids even though it shares the shipped_app role's EXECUTE grant. This is the
// DEDICATED ops/DR escalation path: we open a tx, SET LOCAL app.ops_mode='1' (binds
// for this tx only, via set_config so the value is a bound parameter), then call the
// function. Only these operator maintenance jobs ever flip ops mode.
func (s *Store) ListAllOrgIDs(ctx context.Context) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Opt into ops mode for THIS transaction only (the gate app.all_org_ids() checks).
	if _, err := tx.Exec(ctx, `SELECT set_config('app.ops_mode', '1', true)`); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `SELECT id FROM app.all_org_ids()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}
