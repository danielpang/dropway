package handlers

import (
	"context"

	"github.com/danielpang/dropway/services/api/internal/store"
)

// This file extends the unit-test fakeStore (handlers_test.go) with the Phase-4
// SiteStore surface (audit logging). Audit rows are captured in the p2 sidecar
// state so a test can assert exactly what an action recorded.

// auditLog returns the captured audit entries for this fakeStore.
func (f *fakeStore) auditLog() []store.AuditEntry { return f.p2().audit }

func (f *fakeStore) WriteAudit(_ context.Context, t store.Tenant, rec store.AuditRecord) (store.AuditEntry, error) {
	f.lastTenant = t
	if f.p2().auditErr != nil {
		return store.AuditEntry{}, f.p2().auditErr
	}
	e := store.AuditEntry{
		ID:        "audit_" + string(rec.Action),
		OrgID:     t.OrgID,
		Action:    string(rec.Action),
		Target:    rec.Target,
		Metadata:  rec.Metadata,
		RequestID: rec.Ctx.RequestID,
		TraceID:   rec.Ctx.TraceID,
		IP:        rec.Ctx.IP,
	}
	if rec.Ctx.ActorUser != "" {
		u := rec.Ctx.ActorUser
		e.ActorUser = &u
	}
	if rec.Ctx.ActorToken != "" {
		tok := rec.Ctx.ActorToken
		e.ActorToken = &tok
	}
	f.p2().audit = append(f.p2().audit, e)
	return e, nil
}

func (f *fakeStore) ListAudit(_ context.Context, t store.Tenant, p store.ListAuditParams) ([]store.AuditEntry, error) {
	f.lastTenant = t
	if f.p2().auditErr != nil {
		return nil, f.p2().auditErr
	}
	// Newest-first: the handler relies on the store ordering; the fake appends in
	// chronological order, so reverse for the response.
	all := f.p2().audit
	out := make([]store.AuditEntry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, all[i])
	}
	// Apply limit/offset over the reversed slice.
	limit := int(p.Limit)
	if limit <= 0 {
		limit = 50
	}
	offset := int(p.Offset)
	if offset > len(out) {
		offset = len(out)
	}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
