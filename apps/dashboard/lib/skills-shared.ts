/**
 * Client-safe skill constants. lib/api.ts is `server-only` (it mints the Better
 * Auth JWT via next/headers), so any VALUE a client component needs must live
 * here — importing a runtime binding from lib/api.ts into a "use client" file
 * breaks the build. Types can still be imported from lib/api.ts with
 * `import type` (erased at compile time).
 */

/** The uploader id that marks a Dropway-seeded preset skill (render as "Dropway"). */
export const SKILL_SEED_OWNER = "00000000-0000-0000-0000-000000000000";
