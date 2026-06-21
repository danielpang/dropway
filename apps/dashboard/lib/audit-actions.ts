// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// PURE, client-safe audit-action helpers (NO "server-only"): the highlight
// matcher is used by the client component components/audit/audit-table.tsx, so it
// must NOT live in lib/audit.ts (which is "server-only" because it calls the Go
// API). lib/audit.ts re-exports `isSecurityAction` for server callers.

/**
 * Security-relevant audit actions get a highlighted row in the viewer. We match
 * by a small set of prefixes/keywords so the highlight stays correct even as the
 * Go API adds new verbs in these families (access-mode flips, sharing-policy
 * changes, revocations, member/role changes, suspensions). Matching is on the
 * dotted `action` string; unknown actions render normally.
 */
const SECURITY_ACTION_PATTERNS: RegExp[] = [
  /revoke/i,
  // Membership changes: removals, role changes, AND additions (invites/joins) —
  // who can reach the org is security-relevant, so surface them all.
  /^member\.(removed|role|invite|join)/i,
  /unshare/i,
  /external[_.]sharing/i,
  // Matches the canonical Go actions site.access_change AND site.access_mode*
  // (the `_change` suffix has no word boundary after "access", so a plain
  // /\.access\b/ silently missed site.access_change, audit MEDIUM).
  /access[_.](mode|change)/i,
  /\.access[_.]/i,
  /allowlist/i,
  /suspend/i,
  /billing\.(suspended|past_due)/i,
  /token\.(revoked|issued)/i,
  /password/i,
];

/** True when an audit action is access-mode / security relevant → highlight it. */
export function isSecurityAction(action: string | null | undefined): boolean {
  if (!action) return false;
  return SECURITY_ACTION_PATTERNS.some((re) => re.test(action));
}
