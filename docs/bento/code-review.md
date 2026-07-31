# Code review — "Sites" page: org-wide listing + "Mine" filter

Stage: Code review. Reviewed implementation commit `d8fe3ec` against
[`engineering-requirements.md`](./engineering-requirements.md) and
[`design.md`](./design.md).

## Findings

### Low — Hidden badge copy is false in degraded mode

`apps/dashboard/app/(app)/dashboard/page.tsx:72-81,240-247`

When `loadActiveOrg()` is unavailable, degraded mode deliberately fails open and shows every
site to a plain member. `SiteRow` still renders the Hidden badge with the title, "Only its
owner and org admins see it in this list." In this state the current viewer is proof that the
claim is false. Suppress the badge's list-visibility sentence in degraded mode, suppress the
badge, or pass enough context to render accurate copy. The visible "Hidden" label can still
describe feed visibility without making a claim about this list.

### Low — Page-level filter orchestration has no automated coverage

`apps/dashboard/app/(app)/dashboard/page.tsx:38-165`
`apps/dashboard/test/sites-visibility.test.ts:20-155`

The 18 new tests thoroughly cover the two pure helpers, but none covers the page logic that
wires them together: `?owner=me` handling, All/Mine links and counts, empty-state precedence,
or degraded-mode suppression of filters/bylines/footnotes. A regression in those requirements
could pass the current suite. Add a small server-render/orchestration test after extracting
the page-state calculation, or add an integration test for the two dashboard URLs and the
degraded response.

## Requirement assessment

Aside from the degraded-mode copy issue, the changes satisfy the staged requirements: the
page and navigation are renamed to Sites; the default list applies the agreed discovery rule;
`?owner=me` provides a one-click, shareable Mine filter; ownership and hidden status are shown;
empty and loading states are updated; and missing org context fails open as specified. The
implementation is frontend-only and keeps the visibility predicate and owner-label logic in a
small framework-free module.

Product sign-off is still required for the agreed reading of org visibility: ordinary members
can no longer browse to teammates' feed-hidden sites, although direct-link, API, CLI, and MCP
access remains unchanged. This is discovery behavior, not an access-control boundary.

## Verification

- `pnpm test` in `apps/dashboard`: 163 tests passed across 20 files.
- `pnpm lint` in `apps/dashboard`: passed with no warnings or errors.
- `pnpm typecheck` in `apps/dashboard`: blocked by the pre-existing unresolved
  `@dropway/sdk` import in `e2e/sdk-deploy.spec.ts:12`; no feature-related type error was
  reported before that failure.
- `git diff --check`: passed.

## Verdict

Changes requested for the misleading degraded-mode copy. The missing page-level coverage is
non-blocking but should be addressed to meet the team's expectation that behavioral changes
are unit- or integration-tested.
