# Implementation — "Sites" page: org-wide listing + "Mine" filter

Stage: Implementation. Built to [`engineering-requirements.md`](./engineering-requirements.md)
and [`design.md`](./design.md). Frontend-only, as scoped: nothing in `services/api`,
`db/sqlc`, `cli/`, or `services/mcp` changed, and the route stays `/dashboard`.

## What shipped

| File | Change |
| --- | --- |
| `apps/dashboard/components/main-nav.tsx` | `NAV_LINKS[0].label`: `"My Sites"` → `"Sites"` |
| `apps/dashboard/lib/sites-visibility.ts` *(new)* | `isSiteVisibleTo` + `ownerLabel`, both pure |
| `apps/dashboard/app/(app)/dashboard/page.tsx` | copy, `searchParams`, visibility rule, filter row, card byline + Hidden badge, footnote, new empty state, degraded mode |
| `apps/dashboard/app/(app)/dashboard/loading.tsx` | new subheading copy + skeleton chip row |
| `apps/dashboard/test/sites-visibility.test.ts` *(new)* | 18 unit tests over both helpers |

**FR-1 Rename.** Nav label, `metadata.title`, and the `<h1>` all read "Sites"; the
subheading in `page.tsx` and `loading.tsx` is the same string verbatim ("Every site in your
org. Deploy a folder, get a live, access-controlled URL."). No `"My Sites"` remains outside
`docs/bento/*`.

**FR-2 Visibility rule.** `lib/sites-visibility.ts` is framework-free (no `server-only`, no
React) so vitest imports it directly. `isSiteVisibleTo` implements reading B: `canManage` ⇒
true; `feed_visible !== false` ⇒ true (absent flag reads as visible — the API default);
otherwise `owner_id` must be present *and* equal a non-null viewer id. `ownerLabel` is the
Feed's ladder (`You` → roster `name ?? email` → `A teammate`), extracted so it's testable
without a render. The module's doc comment states plainly that this is discovery, not access
control, and names the surfaces (API, CLI, MCP) that still list everything — per §2 of the
requirements, nothing in the code frames it as a privacy guarantee.

**FR-3 All/Mine filter.** `searchParams` is awaited (Next 15 Promise prop) and joined with
the existing `Promise.allSettled` under one `Promise.all`, so the param resolution adds no
serial wait. Chips are `<Button asChild size="sm"><Link/></Button>` inside
`<nav aria-label="Filter sites by owner">`; active gets `variant="secondary"` +
`aria-current="page"`; an active "Mine" hrefs back to `/dashboard` (toggle-off). Counts come
off the already-filtered arrays, so chip numbers always equal rendered cards. The page stays
a server component — no new `"use client"`, zero added client JS.

**FR-4 Card.** Badge row is now `flex flex-wrap items-center gap-2`; order Live/Not-deployed
→ `AccessModeBadge` → Hidden → `ml-auto` byline. Hidden is `<Badge variant="outline">` with
lucide `EyeOff` (`size-3`) and the design's tooltip, rendered only on
`feed_visible === false`. The byline is real text with `min-w-0 truncate` so a long teammate
name can't widen the card. Still one `Link`, no nested interactive elements.

**FR-5 Empty states.** Precedence is `loadError` → `allVisible.length === 0` ("No sites yet")
→ `shown.length === 0` (new `NoSitesOfYourOwn`: "You haven't created a site yet" +
`NewSiteDialog` + outline "Browse all {n} sites" → `/dashboard`) → grid. Org-empty is checked
first, so "Browse all 0 sites" can't render. Both empty cards reuse the existing
`border-dashed p-12` shell; the count is singularised ("1 site").

**FR-6 Footnote.** `text-xs text-muted-foreground` below the grid, only when ≥1 card renders
and not degraded; admin/owner copy differs from member copy. No counts.

**FR-7 Degraded mode.** `degraded = org === null` covers both a rejected `loadActiveOrg()`
and a null return. It fails open: all sites render unfiltered, `?owner=me` is ignored, and
the filter row, bylines, and footnote are suppressed. Hidden badges still render (they depend
only on `feed_visible`).

**FR-8 Skeleton.** `loading.tsx` gained the two-chip band (`h-9 w-20` / `h-9 w-24`,
`rounded-md`) in the `space-y-8` rhythm and the new subheading, so neither the heading block
nor the grid moves on swap-in.

## Verification

- `pnpm test` (apps/dashboard) — **163 tests across 20 files pass**, including the 18 new
  ones in `test/sites-visibility.test.ts`:
  the member/own-hidden and member/teammate-hidden split, admin sees everything,
  `feed_visible: undefined` visible, `userId: null` never matching an `owner_id` (present or
  absent), a mixed-list filter, and the full `ownerLabel` ladder incl. blank roster rows.
- `pnpm lint` — clean.
- `pnpm typecheck` — no errors in changed files. One **pre-existing, unrelated** failure
  remains: `e2e/sdk-deploy.spec.ts` can't resolve `@dropway/sdk` because the workspace
  package isn't built in this checkout. Confirmed identical on a clean tree (`git stash`).
- `pnpm build` — compiles and type-checks the app successfully; it then stops at page-data
  collection with `BETTER_AUTH_DATABASE_URL / DATABASE_URL is not set`, an environment
  limitation of this sandbox, not a code failure.
- E2E specs unchanged, as the requirements predicted — none asserts the page heading.

## For the PR / product sign-off

1. **Reading B is a live product decision.** A plain member can no longer *browse* to a
   teammate's site marked private, including one they could edit under `allow_member_edits`.
   Access is unchanged: `/sites/[id]` still resolves by direct link, and the API, CLI
   (`dropway sites list`) and MCP `list_sites` still return every row. Fallback if product
   rejects it: drop the `isSiteVisibleTo` call site and keep the Hidden badge.
2. **Not a security boundary.** Making `feed_visible` a server-enforced listing rule is the
   deferred server-side `?owner=me` work; nothing here should be read as enforcement.

## Deviations from the specs

None. Two judgement calls inside the specified behaviour: `ownerLabel` also returns
"A teammate" for a roster row whose name *and* email are blank (a defensive path the spec
didn't name), and the "Browse all {n} sites" label singularises at n=1.

Out-of-scope items are unchanged from the design doc: route move to `/sites`, `?q=` search,
sort, quota line, server-side filtering/pagination, CLI `--mine`, filter analytics, filter
persistence, and aligning the settings page's "private" wording with "Hidden".
