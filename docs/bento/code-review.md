<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Fix all sites copy — code review

## Result

**Approved — no blocking findings.** Reviewed `352748f` against the product investigation, engineering requirements, implementation notes, and `origin/main`. `dropway-www` correctly has no changes because the affected dashboard exists only in `dropway`.

## Requirements

The implementation does what the agreed requirements ask:

- `apps/dashboard/lib/sites-visibility.ts:34-40` now lists a site only when it is org-shared (`feed_visible !== false`) or owned by the viewer. Removing the admin short-circuit keeps teammates' private sites out of admins' All list while preserving the owner's route to their own private site.
- `apps/dashboard/app/(app)/dashboard/page.tsx:76-85` applies that rule before deriving both All and Mine, so displayed rows and counts use the same filtered set.
- `apps/dashboard/app/(app)/dashboard/page.tsx:154-158` replaces the now-false role-specific copy and adds `text-center` to the footnote.
- `apps/dashboard/app/(app)/dashboard/page.tsx:235-243` updates the Hidden badge tooltip to match the new rule.

This remains a discovery filter rather than access control, as intentionally documented at `apps/dashboard/lib/sites-visibility.ts:7-12`; API, CLI, and MCP behavior is outside this feature's agreed scope.

## Tests

Behavioral coverage is sound in `apps/dashboard/test/sites-visibility.test.ts`:

- Lines 41-68 cover hidden teammate, unowned, and viewer-owned sites for member/admin-labelled viewers.
- Lines 108-121 cover filtering a mixed list and prove both roles receive the same result while the viewer's own hidden site remains reachable.
- Lines 71-99 retain coverage for optional API fields and unknown viewer identity.

The centring is a presentational Tailwind class and has no DOM/screenshot test; that visual check remains manual, as recorded in the implementation notes. This is not blocking for the one-class change.

Fresh review runs in `apps/dashboard` all passed:

- `pnpm test`: 179 tests across 21 files
- `pnpm typecheck`
- `pnpm lint`: no warnings or errors
- `git diff --check`: clean

## Bugs and clarity

I found no functional regression in the changed paths. In particular, `apps/dashboard/lib/sites-visibility.ts:38-39` preserves both the API-default treatment of an absent `feed_visible` field and the owner fallback that avoids stranding private-site owners.

The code and comments are clear for the next maintainer. The role-blind rule, intentional reversal of #142, degraded-mode behavior, and discovery-versus-access-control boundary are all stated near the relevant code. No corrective code changes are recommended.
