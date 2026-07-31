# Product investigation — "My sites" → "Sites", org-visible listing + a one-click "Mine" filter

Stage: Product investigation. Scope: understand the problem space around the sites list
and generate feature ideas. No product code changed in this stage.

---

## TL;DR

The page is already org-wide. Only the label says otherwise.

`GET /v1/sites` has always returned **every site in the org**, not the caller's sites
(`db/sqlc/query.sql:201` — `WHERE org_id = $1`, no owner predicate). The June 2026 commit
`ddac4a2` renamed the page "Sites → My Sites" and described it as "the per-user list", but
never scoped the query. So for ~6 weeks the header has been making a promise the data
doesn't keep: a member with 3 sites in a 10-person org sees all ~40 sites under a heading
that says they're theirs.

That makes this feature mostly a **truth-in-labelling fix plus the affordance the rename was
trying to provide** (a way to see just your own), rather than a data-model change. The one
real design decision is what "org visibility" should mean, because the codebase already has
two orthogonal visibility axes and the sites list currently honours neither.

---

## How it works today

| Concern | Where | Behaviour |
| --- | --- | --- |
| Nav label | `apps/dashboard/components/main-nav.tsx:13` | `"My Sites"` → `/dashboard` |
| Page title / `<h1>` | `apps/dashboard/app/(app)/dashboard/page.tsx:13,59` | `"My Sites"` |
| Loading skeleton `<h1>` | `apps/dashboard/app/(app)/dashboard/loading.tsx:18` | `"Sites"` — **already inconsistent**; the title visibly flips on load |
| Back link from a site | `apps/dashboard/app/(app)/sites/[id]/page.tsx:165` | `"All sites"` — already assumes the org-wide reading |
| Listing query | `db/sqlc/query.sql:201-205`, `services/api/internal/store/sites.go:411` | All org sites, newest first, RLS-scoped to the tenant. No owner filter, no feed filter, no pagination, no query params |
| Site payload | `services/api/internal/handlers/sites.go:26-46` | Includes `owner_id`, `access_mode`, `feed_visible`, `title`, `description`, `storage_bytes`, `allow_member_edits`, `current_version_id` |
| Card contents | `dashboard/page.tsx:88-133` | Slug, live URL, Live/Not-deployed badge, access-mode badge. **No owner, no date, no size** |
| Viewer identity | `apps/dashboard/lib/org.ts` (`loadActiveOrg`) | Already fetched by the page; gives `myUserId`, `myRole`, and the full member roster (names/emails) |
| CLI / MCP | `cli/internal/cmd/sites.go:44`, `services/mcp/internal/store/store.go:97` | Also list all org sites — consistent with the dashboard |

**Two visibility axes exist, and they are deliberately orthogonal:**

- `access_mode` (`public` / `password` / `allowlist` / `org_only`) — who can load the
  *served bytes* at the edge. New sites inherit the org's `default_visibility`, which is
  `org_only` for a fresh org (`services/api/internal/store/sites.go:282-294`).
- `feed_visible` (default `true`) — the *discovery* axis: whether the site shows in the org
  Feed. The owner flips it off "to keep the site private"
  (`apps/dashboard/components/sites/feed-visibility-toggle.tsx`).

**Scale context:** site caps are per-org and pooled across members — Free 10, Pro 100,
Enterprise unlimited (`cloud/quota/quota.go:51-54,134`), with free seats ("pay for sites,
not seats"). So the expected steady state is *many members, many sites, one flat unpaginated
list*. Filtering isn't a nice-to-have at Pro scale; a 100-row grid with no owner column and
no search is close to unusable.

---

## Problems found

**P1 — The heading lies.** "My Sites" over an org-wide list. Cheapest, highest-confidence
fix, and the direct ask.

**P2 — There is no way to see just your own sites.** Not on this page, not in the Feed, not
in the CLI. The 2026-06 rename was an attempt to satisfy this need with a label instead of a
filter. This is the actual user job the feature exists to serve.

**P3 — "Private" sites are not private on this page.** A site with `feed_visible = false`
is described in the settings UI as "private" and is filtered out of the Feed
(`ListFeedSites`), but it **still appears on every teammate's sites list**, because
`ListSites` ignores the flag. Renaming the page to "Sites" makes this more visible, not
less: the page now openly claims to be the org's shared inventory while including things
their owners marked private. This is the one genuine product decision in the feature.

**P4 — No ownership attribution on the cards.** Once the page is honestly org-wide, "whose
site is this?" becomes the first question every row raises. The Feed and Skills pages both
already solve it (`"You"` / member name / `"A teammate"` — `feed/page.tsx:50-56`,
`skills-view.tsx:110-115`) and the roster is already loaded on this page.

**P5 — Sites vs Feed now overlap.** If Sites shows everyone's sites, what is the Feed for?
The honest split is: **Sites = the operational inventory** (URL, live status, access mode,
deploy, rollback, domains) and **Feed = social discovery** (title/description, votes,
comments, newest-first). Worth stating explicitly in the page copy so the two surfaces read
as complementary rather than duplicated.

---

## The decision this feature turns on

> "show all sites that have org visibility"

Three defensible readings:

| # | Reading | Rule | Assessment |
| --- | --- | --- | --- |
| A | Every site in the org (status quo data) | no filter | Simplest, zero backend work — but keeps P3: teammates' "private" sites stay listed, contradicting the settings copy |
| B | Sites shared to the org, plus all of your own | `feed_visible \|\| owner_id === me` (admins see all) | **Recommended.** Matches the phrase literally, honours the existing privacy toggle, and never hides your own work from you |
| C | `access_mode === "org_only"` | access axis | Almost certainly not intended: it would hide `public` sites, and a fresh org's default is `org_only` anyway so the filter would be a near-no-op today |

Recommendation: **B**, with the "Mine" filter as the one-click escape hatch and an
admin/owner bypass so admins keep a complete inventory (mirrors how the feed-visibility and
collab toggles already gate on owner-or-admin). If B is chosen, the empty/edge copy needs to
name the rule ("Private sites are only shown to their owner and org admins"), otherwise
members will report missing sites as a bug.

If the team prefers A for launch, ship it as A **but** add a "Private" badge on any
`feed_visible === false` card so the inconsistency is at least legible, and treat B as a
fast follow.

---

## Feature ideas

### Core (the ask)

1. **Rename to "Sites"** — nav (`NAV_LINKS`, one place; the mobile menu derives from it),
   `metadata.title`, `<h1>`, and the subheading copy. Fixes the loading-skeleton flip for
   free. Sub-decision: **keep the route at `/dashboard` or move the list to `/sites`?**
   `/sites/[id]` already exists, so `/sites` is the natural home — but `/dashboard` is the
   post-auth landing referenced in ~15 places (`sign-in`, `sign-up`, `onboarding`,
   `oauth/consent`, `accept-invitation`, `error`, `not-found`, every `revalidatePath`) and
   three e2e specs wait on `/\/dashboard/`. Recommendation: **rename the label now, keep the
   route**; move the route in a separate, mechanical change so a copy fix isn't gated on an
   auth-redirect refactor.

2. **One-click "All / Mine" filter.** Reuse the Skills page's chip pattern verbatim
   (`skills-view.tsx:203-228`: `Button` with `variant={active ? "secondary" : "ghost"}`,
   `size="sm"`) — no new UI primitive needed, and the app has no Tabs/ToggleGroup component.
   Put the count on the chip (`Mine · 4`); it's free from the array. Drive it from a URL
   search param (`?owner=me`) like Skills does, so the view is shareable, survives refresh,
   and works without client state. Filtering itself can be client-side — `owner_id` is
   already on every row and the list is bounded at 100.

3. **Owner attribution on each card** — `"You"` / member name / `"A teammate"`, resolved
   from the already-loaded roster. Without it the filter has nothing to filter *by* visually.

4. **Filter-aware empty states.** Three distinct cases, currently one: org has no sites
   (existing "No sites yet" + create CTA); *I* have no sites but the org does ("You haven't
   created a site yet — here's how, or browse your org's"); nothing matches a search.

### Supporting

5. **"Private" badge** on `feed_visible === false` cards (visible to owner/admins), so the
   Feed toggle's effect is legible from the list. Pairs with either reading A or B.
6. **Search box** — slug/title substring. At Pro's 100-site cap, chips alone won't cut it.
   Same URL-param mechanics as Skills' `?q=`.
7. **Sort control** — newest (current default) / recently deployed / A–Z. "Recently
   deployed" is the most useful for an operational inventory but needs a `last_deployed_at`
   on the payload (derivable from the current version) — flag as a small API addition.
8. **Storage + deploy metadata on the card** — `storage_bytes` is already in the payload and
   currently unused by the list. Cheap density win.
9. **Quota context in the header** — "12 of 100 sites". The cap is org-pooled, so an
   org-wide list is exactly the right place to surface it; today the user only discovers the
   cap by hitting a 402 in the create dialog.

### Stretch / later

10. **Server-side filter parameters** on `GET /v1/sites` (`?owner=me`, `?q=`) so the CLI,
    MCP `list_sites`, and the dashboard share one definition of "mine" and the list can
    paginate past the Enterprise (uncapped) case. Not needed for launch at ≤100 rows.
11. **`dropway sites list --mine`** — same job, terminal-side, once #10 exists.
12. **Group-by-owner view** for admins (the `/v1/storage` per-user endpoint already models
    this shape for storage).
13. **Analytics on the filter** — the app has PostHog wired (`lib/analytics-shared.ts`). A
    `sites_filter_used` event with the chosen chip answers "was per-user scoping the real
    need, or did people actually want search?" and would settle whether #6/#7 are worth it.

---

## Risks and open questions

- **Does reading B hide a site someone expects to see?** Owners always keep their own, and
  admins keep everything, so the only loss is: a plain member can no longer *browse* to a
  teammate's site that its owner marked private — including one they were collaborating on
  under `allow_member_edits` (which defaults to true and is independent of `feed_visible`).
  Mitigation: nothing about access changes — a direct link to `/sites/[id]` still resolves,
  since the detail page is RLS-org-scoped, not owner-scoped. It's a discovery change only.
  Worth confirming that's the intended trade, and consider whether `feed_visible = false`
  should also imply restricting edits.
- **Route rename blast radius** — quantified above; recommend deferring.
- **Filter default** — "All" on first load (matches the new name and the "All sites" back
  link). Consider remembering the last choice per user later; don't in v1.
- **Terminology** — the app uses "private" for `feed_visible = false` and "Org only" for
  `access_mode = org_only`. Two different things, one word each, both on the same card once
  a Private badge lands. The copy needs one careful pass, ideally: "Hidden" (feed) vs "Org
  only" (access).
- **Test coverage gap** — there is currently **no unit or e2e test for the sites list page**
  (`apps/dashboard/test/` has no sites/nav test; e2e only waits on the `/dashboard` URL).
  Whatever the filter rule ends up being, it should ship with a test for the
  owner/visibility predicate, since it's the first piece of real logic this page has had.

---

## Recommended scope for the next stage

Ship 1–4 as the feature (rename, All/Mine chips via `?owner=me`, owner labels, filter-aware
empty states) on reading **B** with an admin bypass, plus 5 (Private badge) since it's a
handful of lines and closes P3 legibly. Hold 6–9 for a follow-up informed by 13. Keep the
route at `/dashboard`; treat `/sites` as a separate mechanical change.
