import "server-only";

import { api, ApiError, type AuditEvent } from "@/lib/api";

/**
 * Server-side read of the org's audit log (Phase 4).
 *
 * The audit viewer (app/(app)/audit) is owner/admin only and shows recent
 * security-relevant events from app.audit_log (member removals, unshares,
 * external-sharing toggles, access-mode changes, revocations …). The Go API is
 * the system of record and RLS-scopes every row to the active org; this loader
 * is a thin, fail-soft wrapper.
 *
 * Graceful degradation (mirrors lib/billing-server.ts):
 *   - 404 → the /v1/audit endpoint isn't on this build yet (the Go agent's
 *     Phase-4 work may not have landed, or self-host opted out) → `available:false`.
 *   - 403 → the caller isn't owner/admin → `available:false, forbidden:true`
 *     (the page also gates on role before ever calling this, so this is defense
 *     in depth, not the primary gate).
 *   - any other error → surface `error` so the page can show a retry affordance
 *     instead of a blank table.
 */

export const AUDIT_PAGE_SIZE = 25;

export interface AuditLoad {
  /** True when the /v1/audit endpoint exists and returned data. */
  available: boolean;
  /** True specifically when the API rejected the caller's role (403). */
  forbidden: boolean;
  /** A human-readable error for a transient failure (not 404/403). */
  error: string | null;
  events: AuditEvent[];
  /** Zero-based page index that was loaded. */
  page: number;
  pageSize: number;
  /** Total rows when the API reports it (drives "Page X of Y"). */
  total: number | null;
  /** True when another page likely exists (total-based or full-page heuristic). */
  hasNext: boolean;
  hasPrev: boolean;
}

/** Load one page (zero-based) of audit events for the active org. */
export async function loadAuditPage(page = 0): Promise<AuditLoad> {
  const safePage = Number.isFinite(page) && page > 0 ? Math.floor(page) : 0;
  const pageSize = AUDIT_PAGE_SIZE;

  try {
    const res = await api.listAudit({
      limit: pageSize,
      offset: safePage * pageSize,
    });
    const total = typeof res.total === "number" ? res.total : null;
    const hasNext =
      total != null
        ? (safePage + 1) * pageSize < total
        : // No total reported → a full page implies there may be more.
          res.events.length === pageSize;

    return {
      available: true,
      forbidden: false,
      error: null,
      events: res.events,
      page: safePage,
      pageSize,
      total,
      hasNext,
      hasPrev: safePage > 0,
    };
  } catch (err) {
    const status = err instanceof ApiError ? err.status : 0;
    // 404 → endpoint not on this build; 403 → not owner/admin. Both render an
    // explanatory empty state rather than an error.
    const available = status !== 404;
    const forbidden = status === 403;
    return {
      available: available && !forbidden,
      forbidden,
      error:
        status === 404 || status === 403
          ? null
          : "Couldn't load the audit log. Try again.",
      events: [],
      page: safePage,
      pageSize,
      total: null,
      hasNext: false,
      hasPrev: safePage > 0,
    };
  }
}

// The pure highlight matcher lives in the client-safe lib/audit-actions.ts (a
// "use client" component imports it, so it can't live in this "server-only"
// module). Re-export it here for server-side callers.
export { isSecurityAction } from "@/lib/audit-actions";
