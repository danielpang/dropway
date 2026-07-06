/**
 * Client-safe skill constants. lib/api.ts is `server-only` (it mints the Better
 * Auth JWT via next/headers), so any VALUE a client component needs must live
 * here — importing a runtime binding from lib/api.ts into a "use client" file
 * breaks the build. Types can still be imported from lib/api.ts with
 * `import type` (erased at compile time).
 */

/** The uploader id that marks a Dropway-seeded preset skill (render as "Dropway"). */
export const SKILL_SEED_OWNER = "00000000-0000-0000-0000-000000000000";

/**
 * Mirror of the server's internal/skillspec.CleanPath: a safe skill-relative
 * POSIX path is non-empty, relative, forward-slash only, with no empty / "." /
 * ".." segments and no NUL or backslash. Notably a filename that merely CONTAINS
 * ".." (e.g. "api..reference.md") is SAFE — only a whole ".." segment is not — so
 * a substring check would wrongly drop server-valid files.
 */
export function isSafeSkillPath(p: string): boolean {
  if (!p || p.length > 512 || p.includes("\0") || p.includes("\\")) return false;
  if (p.startsWith("/") || p.includes("//")) return false;
  return p.split("/").every((seg) => seg !== "" && seg !== "." && seg !== "..");
}
