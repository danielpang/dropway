# UI/UX design — "Sites" page: org-wide listing + one-click "Mine" filter

Stage: UI/UX design. Input: [`product-investigation.md`](./product-investigation.md).
Scope: specify the interface and interaction. **No product code changed in this stage** —
this document is the build spec for the implementation stage.

---

## Design intent

The page already shows the whole org. This design stops the heading from lying, then adds
the two things an honest org-wide list immediately needs: **whose site is this** (a byline
on every card) and **a way to see only mine** (one chip). Everything else is held back.

Three principles drove the choices below:

1. **The label matches the data.** Every string on the page describes what the query
   actually returns — including the rule that decides what's in it.
2. **Reuse before invention.** The filter is the Skills page's chip pattern; the byline is
   the Feed's `You` / name / `A teammate` ladder; the badges are the existing `Badge`. No
   new UI primitive is introduced, and no new dependency.
3. **The page stays a server component.** The filter is a URL, not client state — so it
   costs zero client JS, survives refresh, is shareable, and works with JS disabled.

Adopting the investigation's **reading B** for "org visibility": you see everything shared
to the org, plus everything of your own; admins see everything.

---

## Layout

```
┌───────────────────────────────────────────────────────────────────────┐
│  Sites                                                  [ New site ]  │  ← h1 + action
│  Every site in your org. Deploy a folder, get a live,                 │
│  access-controlled URL.                                               │
│                                                                       │
│  ( All · 12 )  ( Mine · 4 )                                           │  ← filter row (NEW)
│                                                                       │
│  ┌──────────────────────────────┐ ┌──────────────────────────────┐    │
│  │ ▣  marketing-site         →  │ │ ▣  design-docs            →  │    │
│  │    acme--marketing.dropwa…   │ │    acme--design.dropwayco…   │    │
│  │                              │ │                              │    │
│  │  ●Live  Org only     by You  │ │  Not deployed  Public        │    │
│  │                              │ │              by Ana Ruiz     │    │  ← byline (NEW)
│  └──────────────────────────────┘ └──────────────────────────────┘    │
│  ┌──────────────────────────────┐                                     │
│  │ ▣  scratch                →  │                                     │
│  │    acme--scratch.dropwayc…   │                                     │
│  │                              │                                     │
│  │  ●Live  Public  ⊘Hidden      │                                     │  ← Hidden badge (NEW)
│  │                     by You   │                                     │
│  └──────────────────────────────┘                                     │
│                                                                       │
│  Sites your teammates marked private are only shown to them and       │  ← rule footnote (NEW)
│  org admins.                                                          │
└───────────────────────────────────────────────────────────────────────┘
```

Container, grid, and card geometry are unchanged: `mx-auto max-w-5xl space-y-8`, grid
`grid-cols-1 gap-3 sm:grid-cols-2`, card `p-5`. The only new vertical band is the filter
row, which sits in the existing `space-y-8` rhythm between header and grid.

---

## 1. Rename

| Surface | File | From | To |
| --- | --- | --- | --- |
| Nav label | `components/main-nav.tsx:13` | `"My Sites"` | `"Sites"` |
| Page metadata | `dashboard/page.tsx:13` | `"My Sites"` | `"Sites"` |
| `<h1>` | `dashboard/page.tsx:59` | `"My Sites"` | `"Sites"` |
| Subheading | `dashboard/page.tsx:60-62` | `"Deploy a folder, get a live, access-controlled URL."` | `"Every site in your org. Deploy a folder, get a live, access-controlled URL."` |
| Skeleton `<h1>` | `dashboard/loading.tsx:18` | `"Sites"` (already) | unchanged — the flip is now fixed |
| Skeleton subheading | `dashboard/loading.tsx:19-21` | old copy | must be updated to match the new subheading verbatim |

The mobile menu derives from `NAV_LINKS`, so the nav change is one line. `isNavActive`
already maps `/sites/*` onto the `/dashboard` section and its comment already says "Sites".

**Route stays `/dashboard`.** Per the investigation: `/sites` is the better home, but it is
gated on an auth-redirect refactor touching ~15 call sites and three e2e specs. A copy fix
should not carry that. The `"All sites"` back link on `/sites/[id]` already reads correctly
and needs no change.

**Subheading rationale.** It stays org-name-free on purpose: `loading.tsx` renders the
heading block statically, and interpolating the org name would either force a skeleton there
or shift the layout on swap-in. "Every site in your org" delivers the orientation without a
network round-trip.

---

## 2. Visibility rule (reading B)

A site is listed when:

```ts
/** Reading B: org-shared sites, plus your own, plus everything for admins. */
export function isSiteVisibleTo(
  site: Pick<Site, "owner_id" | "feed_visible">,
  viewer: { userId: string | null; canManage: boolean },
): boolean {
  if (viewer.canManage) return true;                       // owner/admin keep a full inventory
  if (site.feed_visible !== false) return true;            // undefined ⇒ visible (API default is true)
  return Boolean(viewer.userId) && site.owner_id === viewer.userId;
}
```

Ship this as a **named export from a pure module** (`lib/sites-visibility.ts`) rather than an
inline `.filter()`. The investigation flagged that the sites list has no test at all, and
this is the first real logic the page has carried — it needs to be unit-testable without
rendering. `canManage(org.myRole)` already exists in `lib/org.ts`.

Three details that are easy to get wrong:

- **`feed_visible !== false`, not `=== true`.** All `Site` fields are optional in the
  generated schema; an absent flag must read as visible, matching the API default.
- **Filter before counting.** The chip counts are computed from the already-filtered array,
  so the numbers on the chips always equal the number of cards below them.
- **Nothing about access changes.** This is a discovery change only. A direct link to
  `/sites/[id]` still resolves for any org member — that page is RLS-org-scoped, not
  owner-scoped. Collaborators on a hidden site with `allow_member_edits` keep their access;
  they just have to be linked to it rather than browsing to it.

### The rule footnote

Rendered below the grid, `text-xs text-muted-foreground`, only when at least one card is
shown. Two variants, because telling an admin their teammates' sites are hidden from them
would be false:

- Member: *"Sites your teammates marked private are only shown to them and org admins."*
- Owner/admin: *"You're seeing every site in the org, including ones marked private."*

This is deliberately **countless**. A "3 sites are hidden from you" line would leak the
existence and volume of teammates' private work back to the people it was hidden from. The
static sentence does the whole job the investigation asked for — it stops a member from
filing a missing-site bug — without that leak.

---

## 3. The All / Mine filter

**Mechanics.** URL search param `?owner=me`, read by the server component
(`searchParams: Promise<{ owner?: string }>`), exactly like Skills' `?q=` / `?folder=`.
Absent or unrecognised ⇒ All. Filtering is client-of-the-API-free: `owner_id` is already on
every row and the list is capped at 100 (Pro), so it's an array filter, not a request.

**Chips are links, not buttons.** Skills uses `router.push` from a client component; this
page has no other reason to be a client component, so the chips render as
`<Button asChild variant={…} size="sm"><Link href={…}>…</Link></Button>`. Same pixels, and
in exchange: no client bundle, no hydration, middle-click and "open in new tab" work, and
the browser back button moves between filters for free.

```
Chip           href                        variant when active   variant when inactive
All            /dashboard                  secondary             ghost
Mine · 4       /dashboard?owner=me         secondary             ghost
```

- Row container: `<nav aria-label="Filter sites by owner" className="flex flex-wrap items-center gap-2">`.
- Active chip carries `aria-current="page"` (the codebase's existing convention in `main-nav.tsx`).
- Count renders as Skills does: `<span className="ml-1.5 text-xs text-muted-foreground">{n}</span>`.
- **"Mine" toggles off.** Clicking an active "Mine" links back to `/dashboard`. Matches the
  Skills folder chips, and means a user who taps the chip twice can't get stuck.
- **"Mine · 0" stays visible and enabled** when the viewer owns nothing. Hiding it would make
  the control appear only after you no longer need it; leaving it enabled routes a new member
  straight into the most useful empty state on the page (case C below).
- **The row is hidden entirely** when there is nothing to filter — render it when
  `visibleSites.length > 0 || owner === "me"`. (The second clause keeps the escape hatch on
  screen when "Mine" is empty but the org isn't.)

**Default is All**, on every load. It matches the new page name and the "All sites" back
link. Per-user persistence is explicitly out of scope for v1 — a remembered filter would
make the page name and the page contents disagree again, which is the exact bug this feature
exists to fix.

**Growth path.** The row is laid out as Skills' is — search input first, then chips — so a
later `?q=` search box drops into the left of this row with no re-layout, and a sort control
lands at its right. Neither ships now.

---

## 4. Card changes

The card keeps its structure (icon + slug, mono URL, badge row) and gains two things in the
badge row, which becomes `flex flex-wrap items-center gap-2`:

**Owner byline** — trailing, pushed right with `ml-auto`, `text-xs text-muted-foreground`:

```
by You  |  by Ana Ruiz  |  by A teammate
```

Resolved from the roster `loadActiveOrg()` already loads on this page — same ladder as
`feed/page.tsx:52-56` and `skills-view.tsx:114-119`: `You` if `owner_id === myUserId`, else
the member's `name ?? email`, else `A teammate` (covers a removed member). Keep the `by `
prefix for consistency with Skills; right-alignment gives the 2-up grid a clean byline
column to scan, and on a narrow card the wrap drops it to its own right-aligned line.

**"Hidden" badge** — `<Badge variant="outline">` with lucide `EyeOff` (`size-3`), rendered
only when `site.feed_visible === false`. Under reading B this is only ever seen by the site's
owner or an admin, so it reads as "here's what your toggle did" / "here's what you can see
that others can't", never as a leak.

Badge order, left to right: **Live / Not deployed** → **access mode** → **Hidden** → *(gap)* →
**byline**. Status first, because "is it up?" is the question an operational inventory
answers first.

### Terminology

The investigation flagged the collision: the app says "private" for `feed_visible = false`
and "Org only" for `access_mode = org_only`, and both now land on the same card. Resolution
for this surface:

| Concept | Field | Word on the card |
| --- | --- | --- |
| Discovery — off the feed | `feed_visible = false` | **Hidden** |
| Access — org members only | `access_mode = org_only` | **Org only** (unchanged) |

The `Hidden` badge carries `title="Hidden from the org feed. Only its owner and org admins see it in this list."`
so the two never have to be inferred from each other. The settings page keeps saying
"private" for now; aligning that copy is a follow-up, not a blocker — the badge tooltip and
the rule footnote both define the term at the point of use.

---

## 5. Empty states

Four cases where there is one today. `allVisible` = sites passing the visibility rule;
`mine` = those owned by the viewer.

| # | Condition | Icon | Heading | Body | Actions |
| --- | --- | --- | --- | --- | --- |
| A | Load error | — | *(existing)* | `{loadError} Start the API (api.dropway.dev) and reload.` | none |
| B | `allVisible.length === 0` (any filter) | `Rocket` | **No sites yet** | *(existing)* Create a site, then run `dropway deploy ./dist` to push your first deploy. | `NewSiteDialog` |
| C | `owner=me`, `mine.length === 0`, `allVisible.length > 0` | `Rocket` | **You haven't created a site yet** | Create one, then run `dropway deploy ./dist` to push your first deploy. | `NewSiteDialog` + `Button variant="outline" asChild` → **Browse all {n} sites** (`/dashboard`) |
| D | `owner=me`, `mine.length > 0` | — | *(the grid)* | — | — |

Case B **wins over case C** when the org is genuinely empty: offering "Browse all 0 sites"
would be a dead end. Concretely: check `allVisible.length === 0` first, then the filter.

All empty-state cards keep the existing shell — `Card` with `border-dashed p-12
text-center`, `size-12` icon tile — so the three read as one family. `NewSiteDialog` already
handles the `readOnly` billing case; nothing here needs to re-implement that.

---

## 6. Loading skeleton

`loading.tsx` must gain a skeleton filter row, or the grid will jump down ~52px when the real
page swaps in — the exact class of shift the file's own comment says it exists to prevent.

```tsx
<div className="flex flex-wrap items-center gap-2">
  <Skeleton className="h-9 w-20 rounded-md" />   {/* All · n  */}
  <Skeleton className="h-9 w-24 rounded-md" />   {/* Mine · n */}
</div>
```

The chips must be skeletons rather than static text because they carry counts. Also update
the skeleton's subheading to the new copy so the heading block no longer changes on load.
The card skeleton needs no change: the byline and Hidden badge live inside the existing
badge-row band, which is already represented by two pill skeletons.

---

## 7. Accessibility

- **Chips**: real links inside a labelled `<nav>`; `aria-current="page"` marks the active
  filter. Keyboard order is header → chips → cards, which matches reading order.
- **The list**: give the `<ul>` an `aria-label` reflecting the active filter — `"All sites"`
  or `"Your sites"` — so a screen-reader user landing on the list knows which set they're in
  without hunting back to the chips.
- **Filter changes are navigations**, so the page re-announces naturally; no live region and
  no focus management are needed. This is a direct benefit of the links-not-buttons choice.
- **The Hidden badge** conveys meaning by icon + text, never colour alone; `variant="outline"`
  is token-driven and passes contrast in both themes.
- **The byline is real text**, not a `title`, so it is available to assistive tech and to
  in-page search.
- The card remains one link with one accessible name; nothing added introduces a nested
  interactive element.

---

## 8. Responsive

- Header already wraps (`flex-wrap items-end`, `ml-auto` on "New site"); unchanged.
- Filter row wraps at `gap-2`. Two chips fit a 320px viewport comfortably; a future search
  input wraps above them.
- Grid stays `grid-cols-1 sm:grid-cols-2`.
- Inside a card, the badge row wraps; the `ml-auto` byline lands right-aligned on its own
  line when the badges fill the row. Verify at 320px with a long member name — the byline
  should truncate rather than push the card wide (`truncate` on the byline span).

---

## Build checklist

1. `components/main-nav.tsx` — `NAV_LINKS[0].label` → `"Sites"`.
2. `lib/sites-visibility.ts` *(new)* — `isSiteVisibleTo` + the owner-label helper, both pure.
3. `dashboard/page.tsx` — metadata/`<h1>`/subheading copy; accept `searchParams`; load the
   roster from the already-fetched `activeOrg`; apply the rule; compute counts; render the
   filter row, byline, Hidden badge, rule footnote, and the new empty state.
4. `dashboard/loading.tsx` — subheading copy + skeleton filter row.
5. `test/sites-visibility.test.ts` *(new)* — closes the coverage gap the investigation
   flagged. Table-drive the predicate: member sees own hidden site; member does **not** see a
   teammate's hidden site; admin sees both; `feed_visible: undefined` is visible; a null
   viewer id never matches an `owner_id`.

Nothing in `services/api`, `db/sqlc`, `cli/`, or `services/mcp` changes. The API already
returns `owner_id` and `feed_visible` on every row.

---

## Out of scope (and why)

| Deferred | Reason |
| --- | --- |
| Route move to `/sites` | ~15 redirect call sites + 3 e2e specs; mechanical, but shouldn't gate a copy fix |
| Search box (`?q=`) | The filter row is laid out to accept it; wait for evidence it's the real need |
| Sort control | Needs `last_deployed_at` on the payload — an API addition |
| Storage / deploy metadata on cards | Density change; decide it with search and sort, not before |
| Quota line ("12 of 100 sites") | Right page for it, but it's a separate billing-surface decision |
| Server-side `?owner=me` / pagination | Unnecessary at ≤100 rows; needed only when the CLI and MCP want to share the definition |
| `sites_filter_used` analytics | Server-rendered chips have no click handler, and firing on render would count refreshes. Needs a deliberate capture point — worth doing, but as its own change |
| Remembering the last filter per user | Would put the page name and its contents back in disagreement |
| Preserving `?owner=me` through the site detail back link | Minor; the back link's `"All sites"` label stays accurate |

## Open questions for the build stage

1. **Confirm reading B.** The one behaviour change a user could notice: a member can no
   longer *browse* to a teammate's hidden site, including one they can still edit under
   `allow_member_edits`. Access is unchanged and direct links still work — but this is the
   trade to sign off on. Fallback is reading A (no filter) shipping with the Hidden badge, at
   the cost of leaving the settings page's "private" copy contradicted.
2. **Should `feed_visible = false` also imply restricting content edits?** Raised by the
   investigation; genuinely separate from this feature. Recommend answering it on its own.
