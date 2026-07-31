# Engineering requirements — "Sites" page: org-wide listing + "Mine" filter

Stage: Engineering requirements. Inputs: [`product-investigation.md`](./product-investigation.md),
[`design.md`](./design.md). This stage verified every code-level claim in those documents
against the repo and defines the technical requirements and system design for the build
stage. **No product code changed in this stage.**

Verified against the working tree: `dashboard/page.tsx`, `dashboard/loading.tsx`,
`main-nav.tsx`, `lib/org.ts`, `lib/api.ts` + generated `Site` schema, the Skills chip
pattern (`skills-view.tsx:204-220`), the Feed owner ladder (`feed/page.tsx:52-56`),
`ui/button.tsx` (`asChild` supported), `ui/badge.tsx` (`outline` variant exists), the
vitest test suite layout, and all three e2e specs (they wait on the `/dashboard` URL only —
none asserts the "My Sites" string). The design doc is accurate; requirements below adopt
it and add the engineering decisions it left open.

---

## 1. Scope

**Frontend-only.** All changes live in `apps/dashboard`. Confirmed no changes to
`services/api`, `db/sqlc`, `cli/`, or `services/mcp`: `GET /v1/sites` already returns
`owner_id` and `feed_visible` on every row (generated schema `Site`, `schema.ts:1463-1492`),
and the list is bounded by the org site cap (Free 10 / Pro 100). Route stays `/dashboard`.

Adopted product decision: **reading B** — a site is listed when it is feed-visible, or owned
by the viewer, or the viewer is an org owner/admin. This is carried forward from the two
prior stages as the working decision; it remains the one product sign-off to flag in the PR
description (a member loses the ability to *browse* — not access — a teammate's hidden site).

## 2. Explicitly not a security boundary

The visibility rule runs in the server component over data the API already returns to
**every org member**. `GET /v1/sites` is RLS-org-scoped with no owner or `feed_visible`
predicate, and the CLI (`dropway sites list`) and MCP `list_sites` expose the same full
list. Reading B is therefore a **presentation/discovery filter, not access control** — a
member can still enumerate hidden sites via the API. This matches the product intent
("discovery change only"; `/sites/[id]` deliberately stays reachable by direct link), but
the implementation must not describe or comment the filter as a privacy guarantee, and the
PR should say so. Making `feed_visible` a server-enforced listing rule is the deferred
"server-side `?owner=me`" work item, out of scope here.

## 3. Functional requirements

FR-1 **Rename.** `NAV_LINKS[0].label` → `"Sites"` (`main-nav.tsx:13`); `metadata.title` and
the `<h1>` in `dashboard/page.tsx` → `"Sites"`; subheading in both `page.tsx` and
`loading.tsx` → `"Every site in your org. Deploy a folder, get a live, access-controlled
URL."` No other occurrence of "My Sites" exists in the repo (verified by grep). `isNavActive`
and the mobile menu need no change.

FR-2 **Visibility rule.** New pure module `apps/dashboard/lib/sites-visibility.ts`
exporting:

```ts
export function isSiteVisibleTo(
  site: Pick<Site, "owner_id" | "feed_visible">,
  viewer: { userId: string | null; canManage: boolean },
): boolean
```

Semantics: `canManage` ⇒ true; `feed_visible !== false` ⇒ true (all `Site` fields are
optional in the generated schema — absent must read as visible, matching the API default);
otherwise `owner_id` present and equal to a non-null `viewer.userId`. Also export the
owner-label helper (`"You"` / member name-or-email / `"A teammate"`), mirroring
`feed/page.tsx:52-56`, so both are unit-testable without rendering. No `"server-only"`
import in this module (it must be importable from vitest).

FR-3 **All/Mine filter.** `?owner=me` read from the page's `searchParams` prop — **in
Next 15 this prop is a `Promise` and must be awaited** (`searchParams: Promise<{ owner?:
string }>`). Any value other than the literal `"me"` is treated as All. Chips render as
`<Button asChild variant={active ? "secondary" : "ghost"} size="sm"><Link …/></Button>`
inside `<nav aria-label="Filter sites by owner">`; active chip gets `aria-current="page"`;
active "Mine" links back to `/dashboard` (toggle-off). Counts come from the
already-visibility-filtered array so chip numbers always equal rendered cards. The row
renders only when `visibleSites.length > 0 || owner === "me"`, and never in degraded mode
(FR-7). The page remains a server component; no `"use client"` anywhere new.

FR-4 **Card additions.** Badge row becomes `flex flex-wrap items-center gap-2`; order:
Live/Not-deployed → `AccessModeBadge` → Hidden badge → `ml-auto` byline. Hidden badge:
`<Badge variant="outline">` + lucide `EyeOff` (`size-3`), only when
`site.feed_visible === false`, with the tooltip title from the design. Byline: `by {label}`,
`text-xs text-muted-foreground truncate`, real text (not `title`). The card stays a single
`Link` with no nested interactive elements.

FR-5 **Empty states.** Precedence exactly as designed: load error → org-empty ("No sites
yet", existing) → mine-empty-under-`?owner=me` (new: "You haven't created a site yet" +
`NewSiteDialog` + outline button "Browse all {n} sites" → `/dashboard`) → grid. Org-empty
is checked before the filter so "Browse all 0 sites" can never render. All reuse the
existing `border-dashed p-12` card shell and the existing `NewSiteDialog` (which already
handles `readOnly`).

FR-6 **Rule footnote.** Below the grid, `text-xs text-muted-foreground`, only when ≥1 card
is rendered and not in degraded mode. Member copy vs owner/admin copy per the design; no
counts (deliberate — a count would leak hidden-site volume).

FR-7 **Degraded mode (new decision — not covered by the design).** The page loads sites,
billing, and org via `Promise.allSettled`, and `loadActiveOrg()` can reject or return
`null` independently of the sites fetch (Better Auth outage, no active org edge). Without
viewer identity, strict reading B would hide the viewer's **own** hidden sites — a
transient auth hiccup would present as data loss. Required behavior when `activeOrg` is
unavailable but sites loaded: **fail open to the status quo** — render all sites,
unfiltered, with no filter row, no bylines, no footnote, and ignore `?owner=me`. This is
safe precisely because of §2 (the filter is not a security boundary; the data is already
org-readable). Hidden badges may still render (they depend only on `feed_visible`).

FR-8 **Loading skeleton.** `loading.tsx` gains a two-chip skeleton filter row (`h-9 w-20` /
`h-9 w-24`, `rounded-md`) in the `space-y-8` rhythm and the new subheading copy, so neither
the heading block nor the grid position shifts on swap-in. Card skeleton unchanged.

## 4. System design / data flow

No new requests. `DashboardPage` already fetches everything needed in one
`Promise.allSettled`: sites (`api.listSites()`), billing, and `loadActiveOrg()` — which
supplies `myUserId`, `myRole` (→ `canManage(myRole)` from `lib/org.ts:149`), and the full
member roster for byline resolution. Pipeline inside the server component:

```
sites → [degraded? passthrough : filter(isSiteVisibleTo)] = allVisible
allVisible → filter(owner_id === myUserId) = mine
owner param → rendered list = (owner === "me" && !degraded) ? mine : allVisible
chip counts = allVisible.length / mine.length
```

Filtering is O(n) over ≤100 rows (Enterprise is uncapped but the page is already a flat
fetch; pagination is the deferred server-side work). Zero new client JS: chips are links,
so filter changes are full navigations under `dynamic = "force-dynamic"` — refetch per
click is accepted and matches how Skills' URL-param filters behave.

## 5. File-by-file plan

| File | Change |
| --- | --- |
| `components/main-nav.tsx` | 1-line label change (FR-1) |
| `lib/sites-visibility.ts` *(new)* | `isSiteVisibleTo` + owner-label helper, pure (FR-2) |
| `app/(app)/dashboard/page.tsx` | copy; `searchParams`; wire rule/degraded mode; filter row; card badge-row rework; footnote; new empty state (FR-1, 3–7) |
| `app/(app)/dashboard/loading.tsx` | copy + skeleton chip row (FR-8) |
| `test/sites-visibility.test.ts` *(new)* | predicate + label unit tests (§6) |

## 6. Testing requirements

Unit (vitest, `apps/dashboard/test/`, run with `pnpm test` in `apps/dashboard`) —
table-driven over `isSiteVisibleTo`:

- member sees own hidden site; member does **not** see a teammate's hidden site
- admin/owner (`canManage: true`) sees everything, including teammates' hidden sites
- `feed_visible: undefined` and `feed_visible: true` are visible to everyone
- `viewer.userId: null` never matches, including vs `owner_id: undefined` (both-undefined
  must not accidentally equal — this is why the predicate requires `owner_id` present)
- owner-label ladder: self → `"You"`; roster hit → name, falling back to email; miss
  (removed member) → `"A teammate"`

E2E: **no changes required** — verified that `deploy.spec.ts`, `sdk-deploy.spec.ts`, and
`auth-returning.spec.ts` only wait on `/\/dashboard/` and never assert the page heading.
Gates for the build stage: `pnpm typecheck`, `pnpm lint`, `pnpm test` in `apps/dashboard`.

Manual checks: 320px viewport with a long member name (byline truncates, card width
stable); chip middle-click opens `?owner=me` in a new tab; browser back moves between
filters.

## 7. Risks & mitigations

- **Perceived data loss under reading B** — a member stops seeing teammates' hidden sites.
  Mitigated by the footnote (FR-6) and by never hiding one's own sites; called out for
  product sign-off in the PR.
- **"Privacy" overclaim** — §2; the PR and code comments must frame the rule as discovery,
  not access control. CLI/MCP/API behavior is unchanged and still lists everything.
- **Auth-load flakiness hiding sites** — eliminated by FR-7's fail-open degraded mode.
- **Optional-field footguns** — `feed_visible !== false` (not `=== true`) and the
  `owner_id`-present requirement are both encoded in the unit tests.
- **Layout shift** — FR-8; the skeleton must mirror the new filter row or the fix
  reintroduces the exact jump `loading.tsx` exists to prevent.

## 8. Out of scope (unchanged from design)

Route move to `/sites`; search (`?q=`); sort (needs `last_deployed_at` API addition);
storage/quota surfacing; server-side `?owner=me` + pagination; CLI `--mine`; filter
analytics; per-user filter persistence; aligning the settings page's "private" copy with
the new "Hidden" term.
