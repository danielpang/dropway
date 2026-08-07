<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Fix all sites copy — implementation

Stage: Implementation. Built to `docs/bento/engineering-requirements.md`, which
resolved the open decisions from `docs/bento/product-investigation.md` (no
`design.md` was committed). The plan held up in the code; nothing in it turned
out to be wrong, and nothing was built beyond it.

## What shipped

Three files, all in `apps/dashboard`, all in the `dropway` repo. `dropway-www`
was not touched — the investigation confirmed the page, the rule, and the copy
exist only in the dashboard app, and reading the code confirmed it again. No
server, API, database, CLI, MCP, or SDK change; no migration.

### `lib/sites-visibility.ts` — the rule

Deleted the `if (viewer.canManage) return true;` short-circuit. The listing rule
is now the same for every role: *shared to the org (`feed_visible !== false`),
or owned by the viewer*. Deleted `canManage` from the `SiteViewer` interface so
the compiler would find every stale usage — it found the page and the tests,
which grep had already identified as the only two consumers.

The `feed_visible !== false` comparison and the `owner_id`/null-viewer guards
are untouched, and the "DISCOVERY rule, not access control" paragraph in the
module doc is kept verbatim: it is still the load-bearing caveat. The
`isSiteVisibleTo` JSDoc now records that the reversal of #142 is deliberate.

### `app/(app)/dashboard/page.tsx` — the page

- `viewer` is now `{ userId: org?.myUserId ?? null }`; the `canManage` import
  from `@/lib/org` is gone (`loadActiveOrg` stays).
- The footnote's two-branch ternary collapsed to one sentence — with a
  role-blind rule, a role branch would be dead code — and the `<p>` gained
  `text-center`, matching the sibling empty-state and error cards:

  ```tsx
  {degraded ? null : (
    <p className="text-center text-xs text-muted-foreground">
      Sites marked private are only shown to their owner.
    </p>
  )}
  ```

- Hidden badge tooltip: "…Only its owner and org admins see it in this list." →
  "…Only its owner sees it in this list."
- Page JSDoc: dropped "plus everything for owners/admins".
- Nothing else moved. The `degraded` fail-open never depended on role and is
  unchanged; `loading.tsx` reserves no footnote space and needed none.

### `test/sites-visibility.test.ts` — the contract

- `"admin sees a teammate's hidden site"` and `"admin sees an unowned hidden
  site"` flipped to `false` and were renamed to `does NOT see`.
- Added `"admin sees their OWN hidden site"` — the automated guard against the
  trap door the investigation identified (§5), pinning that the owner fallback
  still works when the owner happens to be an admin. It sits alongside the
  existing `"member sees their OWN hidden site"`.
- The mixed-list test's admin expectation went from `["a","b","c","d"]` to
  `["a","c","d"]`, identical to the member's. Both assertions were kept: the
  point is now that role *doesn't* matter.
- Viewer fixtures dropped `canManage`. `admin` stays a named viewer — with an
  explanatory comment, since it is now structurally identical to `member` — so
  the flipped cases still read as "the admin role changed behaviour here on
  purpose" rather than looking like a stray duplicate.
- Updated the file-header comment's description of the rule.

## Verification

Run in `apps/dashboard/`:

| Check | Result |
| --- | --- |
| `pnpm test` | 179 tests / 21 files passed |
| `pnpm typecheck` | clean |
| `pnpm lint` | clean — no ESLint warnings or errors |

Two environment notes, neither a code problem:

- `pnpm` was not on `PATH`; it was activated from the corepack cache
  (`pnpm@9.12.0`, as pinned by the root `packageManager`) and `pnpm install
  --frozen-lockfile` run first.
- The first `pnpm typecheck` failed on `e2e/sdk-deploy.spec.ts` with
  `Cannot find module '@dropway/sdk'` — a pre-existing unbuilt workspace
  dependency, unrelated to this change. Building it (`pnpm build` in
  `packages/sdk`) cleared it; typecheck is clean.

The manual checks in the requirements (§6) need a running stack and were not
performed here. The trap-door case and the ask itself are both covered by unit
tests; the copy centring and degraded mode are visual and remain to be eyeballed
in review.

## Consequences carried forward, as accepted in the requirements

Not bugs, and not fixed here:

- An admin whose org contains only teammates' private sites now sees the "No
  sites yet" empty state — the same state a plain member in that org already
  sees. No new empty-state variant.
- The All chip count drops for admins by the number of teammates' private
  sites. Counts still reconcile, since `mine ⊆ allVisible`.
- The billing usage meter can exceed the sum of sites an admin can see. This
  was already true for members.
- `dropway sites --all` and MCP `list_sites` still return private sites — the
  deliberate inventory escape hatch (decision 6).
- This is still a discovery filter, not a security boundary. `GET /v1/sites`
  returns every org row to every member and `/sites/<id>` stays resolvable by
  direct link. Nothing in this change makes it a boundary; the ticket's language
  should not be read as implying one shipped.
