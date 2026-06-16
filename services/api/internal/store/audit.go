// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// AuditEntry is the API-facing view of one app.audit_log row (GET /v1/audit).
type AuditEntry struct {
	ID         string
	OrgID      string
	ActorUser  *string
	ActorToken *string
	Action     string
	Target     string
	Metadata   map[string]any
	IP         string
	RequestID  string
	TraceID    string
	CreatedAt  time.Time
}

// AuditRecord is the input to WriteAudit: a sensitive action, its target, and
// freeform metadata. The actor + request provenance come from an audit.Context.
type AuditRecord struct {
	Action   audit.Action
	Target   string         // e.g. "site:<id>", "member:<id>", "org:<id>"
	Metadata map[string]any // freeform; nil → {}
	Ctx      audit.Context
}

// WriteAudit appends an audit row for the active tenant in its OWN RLS tenant tx.
// Prefer writeAuditTx (below) to record an action in the SAME tx as the mutation
// it describes; WriteAudit is the standalone entry point for actions whose store
// method does not already own a tx (e.g. a publish whose pointer flip committed
// in a prior call, or a revoke that only touches the edge denylist).
//
// The write is best-effort from the caller's perspective in the sense that the
// audit row is org-scoped by RLS (the per-tx GUC + the explicit org_id), so it can
// never land under the wrong tenant; a failure is returned so the handler can log
// it, but callers generally treat audit-write failure as non-fatal to the action
// (the action already succeeded and is authoritative).
func (s *Store) WriteAudit(ctx context.Context, t Tenant, rec AuditRecord) (AuditEntry, error) {
	var out AuditEntry
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		e, err := writeAuditTx(ctx, q, t.OrgID, rec)
		if err != nil {
			return err
		}
		out = e
		return nil
	})
	return out, err
}

// writeAuditTx writes an audit row using an existing *db.Queries already bound to
// a tx that has the RLS tenant context set. Store methods that mutate inside a tx
// call this so the audit row is committed atomically WITH the action (no window
// where the action lands but the audit row is lost).
func writeAuditTx(ctx context.Context, q *db.Queries, orgID string, rec AuditRecord) (AuditEntry, error) {
	meta := rec.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return AuditEntry{}, err
	}

	row, err := q.WriteAuditLog(ctx, db.WriteAuditLogParams{
		OrgID:      orgID,
		ActorUser:  nullableID(rec.Ctx.ActorUser),
		ActorToken: nullableID(rec.Ctx.ActorToken),
		Action:     string(rec.Action),
		Target:     pgtype.Text{String: rec.Target, Valid: rec.Target != ""},
		Metadata:   metaJSON,
		Ip:         parseIP(rec.Ctx.IP),
		RequestID:  pgtype.Text{String: rec.Ctx.RequestID, Valid: rec.Ctx.RequestID != ""},
		TraceID:    pgtype.Text{String: rec.Ctx.TraceID, Valid: rec.Ctx.TraceID != ""},
	})
	if err != nil {
		return AuditEntry{}, err
	}
	return auditEntryFromDB(row), nil
}

// ListAuditParams pages the audit log.
type ListAuditParams struct {
	Limit  int32
	Offset int32
}

// ListAudit returns the active org's audit log newest-first (RLS-scoped). Limit is
// clamped to [1,200]; a non-positive limit defaults to 50. The caller (the GET
// /v1/audit handler) gates this to admin/owner.
func (s *Store) ListAudit(ctx context.Context, t Tenant, p ListAuditParams) ([]AuditEntry, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}

	var out []AuditEntry
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListAuditLog(ctx, db.ListAuditLogParams{Limit: limit, Offset: offset})
		if err != nil {
			return err
		}
		out = make([]AuditEntry, len(rows))
		for i, r := range rows {
			out[i] = auditEntryFromDB(r)
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// conversions + helpers
// ---------------------------------------------------------------------------

func auditEntryFromDB(r db.AppAuditLog) AuditEntry {
	e := AuditEntry{
		ID:         r.ID,
		OrgID:      r.OrgID,
		ActorUser:  r.ActorUser,
		ActorToken: r.ActorToken,
		Action:     r.Action,
		CreatedAt:  r.CreatedAt,
	}
	if r.Target.Valid {
		e.Target = r.Target.String
	}
	if r.RequestID.Valid {
		e.RequestID = r.RequestID.String
	}
	if r.TraceID.Valid {
		e.TraceID = r.TraceID.String
	}
	if r.Ip != nil {
		e.IP = r.Ip.String()
	}
	m := map[string]any{}
	if len(r.Metadata) > 0 {
		_ = json.Unmarshal(r.Metadata, &m) // tolerate legacy/odd shapes → empty map
	}
	e.Metadata = m
	return e
}

// nullableID returns a *string for an optional uuid column: nil for an empty id
// (written as SQL NULL), so a deploy-token-only actor leaves actor_user NULL and a
// user-session actor leaves actor_token NULL.
func nullableID(id string) *string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return &id
}

// parseIP parses a client IP string (which may be "ip:port" from RemoteAddr) into
// the *netip.Addr the inet column maps to. An unparseable/empty value → nil (NULL).
func parseIP(s string) *netip.Addr {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// RemoteAddr is "host:port"; chi RealIP yields a bare host. Try both.
	if addr, err := netip.ParseAddr(s); err == nil {
		return &addr
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return &addr
		}
	}
	return nil
}
