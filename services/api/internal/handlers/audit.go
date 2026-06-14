// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/danielpang/shipped/internal/audit"
	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/internal/logx"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// auditCtx builds the request-provenance audit.Context for the current request:
// the verified actor (claims.UserID), the client IP, and the request/trace
// correlation ids (request_id from the structured-log middleware, honoring an
// inbound X-Request-Id). actor_token is set when a deploy token drove the request
// (recorded by the deploy path); for a user-session request it stays empty.
func auditCtx(r *http.Request) audit.Context {
	reqID := logx.RequestIDFromContext(r.Context())
	c := audit.Context{
		IP:        clientIP(r),
		RequestID: reqID,
		TraceID:   reqID, // mirror; no external tracer wired (cheap end-to-end hook)
	}
	if claims, ok := middleware.ClaimsFromContext(r.Context()); ok {
		c.ActorUser = claims.UserID()
	}
	return c
}

// recordAudit writes an audit row for a sensitive mutation. It is BEST-EFFORT: the
// action it describes has already succeeded and is authoritative, so an audit-write
// failure is logged loudly but never fails the request (we must not turn a
// successful, committed mutation into a 5xx because the trail write hiccuped).
func (a *API) recordAudit(r *http.Request, t store.Tenant, action audit.Action, target string, metadata map[string]any) {
	if a.Store == nil {
		return
	}
	if _, err := a.Store.WriteAudit(r.Context(), t, store.AuditRecord{
		Action:   action,
		Target:   target,
		Metadata: metadata,
		Ctx:      auditCtx(r),
	}); err != nil {
		logger(r).Error("audit write failed",
			"action", string(action), "target", target, "org_id", t.OrgID, "err", err)
	}
}

// clientIP returns the best client IP for the audit row. chi's RealIP middleware
// rewrites RemoteAddr from a trusted X-Forwarded-For / X-Real-IP, so RemoteAddr is
// the resolved value; the store parses an "ip:port" or bare-ip form.
func clientIP(r *http.Request) string { return r.RemoteAddr }

// ---------------------------------------------------------------------------
// GET /v1/audit?limit=&offset=   (admin/owner only, RLS-scoped, newest first)
// ---------------------------------------------------------------------------

type auditEntryResponse struct {
	ID         string         `json:"id"`
	ActorUser  *string        `json:"actor_user,omitempty"`
	ActorToken *string        `json:"actor_token,omitempty"`
	Action     string         `json:"action"`
	Target     string         `json:"target,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	IP         string         `json:"ip,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// ListAudit returns the caller org's audit log, newest first, paginated. ADMIN/
// OWNER only — the audit trail can reveal members, targets, and IPs, so a plain
// member must not read it (the live-membership re-check, not the JWT claim). RLS
// additionally scopes every row to the active org.
func (a *API) ListAudit(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}

	limit := parseIntDefault(r.URL.Query().Get("limit"), 50)
	offset := parseIntDefault(r.URL.Query().Get("offset"), 0)

	entries, err := a.Store.ListAudit(r.Context(), t, store.ListAuditParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]auditEntryResponse, len(entries))
	for i, e := range entries {
		out[i] = auditEntryResponse{
			ID:         e.ID,
			ActorUser:  e.ActorUser,
			ActorToken: e.ActorToken,
			Action:     e.Action,
			Target:     e.Target,
			Metadata:   e.Metadata,
			IP:         e.IP,
			RequestID:  e.RequestID,
			CreatedAt:  e.CreatedAt,
		}
	}
	// `events` is the key the dashboard audit viewer reads; `limit`/`offset` echo the
	// page. There is no `next_cursor` (offset paging) — the dashboard tolerates that.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"events": out,
		"limit":  limit,
		"offset": offset,
	})
}

// parseIntDefault parses a non-negative int query param, falling back to def on an
// empty/invalid value.
func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	return n
}
