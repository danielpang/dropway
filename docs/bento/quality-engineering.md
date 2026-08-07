<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Fix all sites copy — quality engineering

Stage: Quality engineering. Verified `352748f` against the requirements
(`docs/bento/engineering-requirements.md`) and the code review, then added the
test level the change was missing. All checks pass; no defects found.

## What was run

In `apps/dashboard/` (pnpm invoked from the corepack cache; `packages/sdk`
already built):

| Check | Result |
| --- | --- |
| `pnpm test` (before my changes) | 179 tests / 21 files passed |
| `pnpm test` (after adding the page tests) | **186 tests / 22 files passed** |
| `pnpm typecheck` | clean (the new test file is inside tsconfig `include` and is typechecked) |
| `pnpm lint` | clean — no ESLint warnings or errors |
| `git diff --check` | clean |

Neighbouring suites, for confidence nothing else moved: `packages/sdk`
(27 tests) and `edge/serving-worker` (366 tests) both pass. Go services were
not run — the diff touches only `apps/dashboard` TypeScript. Playwright e2e
needs a running stack and was not run, consistent with the earlier stages.

`dropway-www` has no changes and needs none; a repo-wide grep for the removed
copy ("including ones marked private", "only shown to them and org admins")
and for `canManage` confirmed the old strings are gone everywhere and every
remaining `canManage` usage belongs to unrelated features (billing, members,
settings, the server-side toggle permission). All remaining "private" strings
in the dashboard describe the feed toggle or access modes and stay true under
the new rule.

## The gap the existing tests left, and what was added

`test/sites-visibility.test.ts` pins the pure rule thoroughly (including the
optional-field footguns), but nothing exercised the **page wiring** — and both
acceptance criteria live there: the centred footnote is a className on a `<p>`
the rule tests never see, and "private sites don't appear in All" only holds
if the page actually applies `isSiteVisibleTo` to the rows *and* both chip
counts.

Added `test/dashboard-page.test.ts` (7 tests). The page is an async RSC with
no DOM runner, so the tests await the component and walk the returned element
tree without rendering — host elements matched by tag/className, inner
components (`SiteRow`, `FilterChip`, `EmptyState`) by function name, props
read directly. Covered:

- **The ask:** an org admin's All list drops a teammate's private site from
  the rows and from both the All and Mine chip counts, while keeping the
  viewer's own private site (the trap-door guard, now pinned end-to-end at the
  page level, not just in the rule).
- **The copy:** the footnote is the new role-blind sentence, carries
  `text-center`, and the pre-#142-reversal admin wording appears nowhere in
  the tree.
- **`?owner=me`** shows only owned sites (an unowned-but-visible site is in
  All, never in Mine).
- **Accepted consequence pinned:** an admin whose org holds only teammates'
  private sites gets the "No sites yet" empty state, with footnote and chips
  suppressed.
- **Degraded fail-open:** when `loadActiveOrg` rejects, every site (including
  a teammate's private one) is listed, the footnote and chips are dropped, and
  `?owner=me` is ignored — a transient auth hiccup can't masquerade as data
  loss.
- **API failure:** `listSites` rejecting with an `ApiError` renders the inline
  error card with no rows, footnote, or chips.

Mutation-checked, not just green: reverting the visibility rule to
show-everything fails 2 of the new tests, and removing `text-center` from the
footnote fails the centring test. The suite would have caught either
regression.

One test-infrastructure change was required: `vitest.config.ts` now sets
`oxc: { jsx: { runtime: "automatic" } }`, because the app tsconfig's
`jsx: "preserve"` (for Next's compiler) otherwise makes Vite 8's oxc transform
pass `.tsx` through unparsed. This affects only the vitest pipeline; the
Next build is untouched.

## Still manual

Unchanged from the implementation notes: the visual look of the centred
footnote at one- and two-column widths, and degraded mode against a genuinely
stopped Go API, remain eyeball checks — the new tests pin the class name and
the fail-open logic, not the pixels.
