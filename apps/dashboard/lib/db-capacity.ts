// Detecting + logging Postgres connection-capacity failures under ONE stable,
// alertable tag. The dashboard first hit Supabase's session-pooler cap as a generic
// Better Auth 500 (EMAXCONNSESSION buried in the message); these helpers surface that
// whole class of error explicitly so a log-based alert can watch for `[db-capacity]`
// instead of the error being swallowed or lost in noise.
//
// Pure + dependency-light on purpose: lib/auth.ts is also loaded under the jiti CLI
// loader at migrate time, so this file must not pull `server-only` or any Next-only
// module (see the note at the top of lib/auth.ts).

const DB_CAPACITY_TAG = "[db-capacity]";

/**
 * Returns a short machine-stable reason when `err` looks like a connection-capacity
 * exhaustion, else null. Walks the message AND the nested `cause` chain because pg
 * surfaces a SQLSTATE `code` while the Supavisor pooler carries the signal only in the
 * message text. Covers:
 *  - `pooler_session_exhausted` — Supabase/Supavisor session-mode cap (EMAXCONNSESSION)
 *  - `too_many_connections`     — Postgres 53300 / "too many clients" / no free slots
 *  - `pool_acquire_timeout`     — node-postgres connectionTimeoutMillis exceeded
 */
export function connectionCapacityReason(err: unknown): string | null {
  const parts: string[] = [];
  let cur: unknown = err;
  for (let depth = 0; cur && typeof cur === "object" && depth < 5; depth++) {
    const e = cur as { code?: unknown; message?: unknown; cause?: unknown };
    if (typeof e.code === "string") parts.push(`code=${e.code}`);
    if (typeof e.message === "string") parts.push(e.message);
    cur = e.cause;
  }
  if (typeof err === "string") parts.push(err);
  const hay = parts.join(" | ");

  if (/EMAXCONNSESSION|max clients reached in session mode/i.test(hay))
    return "pooler_session_exhausted";
  if (/\bcode=53300\b|too many clients|remaining connection slots/i.test(hay))
    return "too_many_connections";
  if (/timeout exceeded when trying to connect|connection terminated due to connection timeout/i.test(hay))
    return "pool_acquire_timeout";
  return null;
}

/**
 * Logs `err` with the stable `[db-capacity]` tag IFF it is a connection-capacity issue,
 * and returns whether it matched. `where` identifies the call site. Non-capacity errors
 * are left for the caller to handle, this only owns the capacity-exhaustion signal.
 */
export function logIfConnectionCapacity(where: string, err: unknown): boolean {
  const reason = connectionCapacityReason(err);
  if (!reason) return false;
  const message = err instanceof Error ? err.message : String(err);
  // eslint-disable-next-line no-console
  console.error(`${DB_CAPACITY_TAG} ${reason} at ${where}: ${message}`);
  return true;
}
