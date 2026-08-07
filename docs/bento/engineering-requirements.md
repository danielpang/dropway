<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Fix all sites copy — engineering requirements

Stage: Engineering requirements / technical design. Built on
`docs/bento/product-investigation.md`. **No design.md was committed by the
design stage**, so the decisions that stage was meant to make are resolved
here, each with its reason (§2). Everything below is stated against the code
as it exists on this branch.

## 1. Scope

Two changes, both confined to `apps/dashboard` in the `dropway` repo:

1. Centre the footnote under the site grid on `/dashboard`.
2. Stop showing teammates' private sites to org owners/admins in the All
   list, and rewrite the three copy strings that currently promise the
   opposite.

`dropway-www` is untouched — the investigation confirmed the page, the rule,
and the copy exist only in the dashboard app. **No server, API, database, CLI,
MCP, or SDK change.** No migration. No new stored data. `GET /v1/sites`
continues to accept nothing and return every org row; the change is entirely
in the client-side discovery filter and the JSX around it.

## 2. Decisions (made here, with reasons)

| # | Decision | Choice | Reason |
| --- | --- | --- | --- |
| 1 | Which option from the investigation (A/B/C) | **A — drop the admin exception** | The feature description explicitly orders the behaviour change ("private sites should not appear in all sites"), so C (copy-only) is off the table. B (strict: your own private sites leave All too) breaks the All/Mine chip-count reconciliation and reopens the §5 trap door from the investigation for no privacy gain — your own site is not a leak to you. A is the smallest change that makes "nobody sees a teammate's private site" true. |
| 2 | What "private" means | **`feed_visible = false`**, not `access_mode` | Every dashboard string that says "private" ("marked private", the toggle's "keep the site private") refers to the feed toggle. The literal access-mode reading would also evict `password` and `allowlist` sites their owners deliberately shared to the feed, and would make the dashboard's All list disagree with the feed's own SQL rule (`WHERE s.feed_visible`, `db/sqlc/query.sql:220`). |
| 3 | Does the owner keep their own private site in All | **Yes** | Removing it strands the owner: the investigation verified `/dashboard` is the only unconditional route to a private site, including its settings page where the toggle lives. Keeping it also keeps `mine ⊆ allVisible`, so chip counts stay consistent. |
| 4 | Centre the rewritten footnote | **Yes — `text-center` on the `<p>`** | The explicit ask, and it matches the sibling empty-state/error cards which already use `text-center` (`page.tsx:127`, `:268`, `:298`). The sentence gets shorter after the rewrite, but a centred caption under a centred grid is still the right form. |
| 5 | Remove `canManage` from `SiteViewer` | **Yes, delete the field** | After change 1 the rule never reads it. Leaving a dead field on the exported interface would suggest role still matters; deleting it lets the compiler find every stale usage (the page and the tests are the only consumers — verified by grep). |
| 6 | Admin escape hatch for the lost inventory | **None in the dashboard; document CLI/MCP** | `dropway sites --all` and the MCP `list_sites` tool remain unfiltered by design (they are inventory tools, not discovery surfaces). No new admin UI in this feature; if offboarding/storage-cleanup workflows surface as real needs, that's a follow-up with its own design. |

## 3. The change, file by file

All paths relative to `apps/dashboard/`.

### 3.1 `lib/sites-visibility.ts` — the rule

- Delete line 39, `if (viewer.canManage) return true;`. The rule becomes,
  for every role: *shared to the org (`feed_visible !== false`), or owned by
  the viewer*.
- Delete `canManage` from the `SiteViewer` interface (decision 5).
- Update the module doc comment and the `isSiteVisibleTo` JSDoc: remove
  "plus everything for owners/admins". Keep the "DISCOVERY rule, not access
  control" paragraph verbatim — it is still true and is the load-bearing
  caveat.
- Do **not** touch the `feed_visible !== false` comparison or the
  `owner_id`/null-viewer guards; the optional-field footguns they handle are
  unchanged.

### 3.2 `app/(app)/dashboard/page.tsx` — the page

- **Viewer construction (line 73-76):** drop the `canManage` property; the
  `viewer` object becomes `{ userId: org?.myUserId ?? null }`. Remove the
  now-unused `canManage` import from `@/lib/org` (line 12 — `loadActiveOrg`
  stays).
- **Footnote (lines 157-163):** the two-branch ternary collapses to one
  sentence — the rule is now identical for every role, so a role branch would
  be dead code. Add `text-center` to the `<p>`:

  ```tsx
  {degraded ? null : (
    <p className="text-center text-xs text-muted-foreground">
      Sites marked private are only shown to their owner.
    </p>
  )}
  ```

  The exact sentence is a product wording call; the engineering invariant is
  that it must (a) be true under the new rule, (b) not claim admins see
  private sites, (c) scope itself to this list (admins can still *toggle* a
  private site's visibility from its settings page — that permission,
  `lib/api.ts:617`, is server-side and unchanged).
- **Hidden badge tooltip (line 243):** `"Hidden from the org feed. Only its
  owner and org admins see it in this list."` → `"Hidden from the org feed.
  Only its owner sees it in this list."` Post-change the only viewer who ever
  sees this badge in a filtered list *is* the owner. (In degraded mode the
  list is unfiltered and a non-owner could see the badge; acceptable — the
  first sentence is unconditionally true and degraded is a transient
  fail-open.)
- **Page JSDoc (lines 20-27):** remove "plus everything for owners/admins".
- **Nothing else moves.** The `degraded` fail-open (show everything, drop
  footnote and filters) is unchanged and still correct: it exists so a
  transient auth hiccup can't hide the viewer's own sites, and it never
  depended on role. `loading.tsx` reserves no footnote space today and needs
  no change.

### 3.3 `test/sites-visibility.test.ts` — the contract

These edits are the proof the change did what was asked; if a diff lands
without them, it didn't.

- `"admin sees a teammate's hidden site"` (L48-53): flip `visible: true →
  false`, rename to `"admin does NOT see a teammate's hidden site"`.
- `"admin sees an unowned hidden site"` (L54-59): flip to `false`, rename
  accordingly.
- **Add** one case the suite doesn't have and the trap door demands:
  `"admin sees their OWN hidden site"` — `{ owner_id: ME, feed_visible:
  false }`, viewer `admin`, `visible: true`. This pins that the owner
  fallback works when the owner happens to be an admin.
- Mixed-list test (L98-114): the admin expectation `["a","b","c","d"]`
  becomes `["a","c","d"]` — identical to the member's. Keep both assertions
  rather than collapsing them; the point is now that role *doesn't* matter.
- Update the `admin`/`member`/`anonymous` viewer fixtures to drop
  `canManage` (the field no longer exists on `SiteViewer`). Keep the `admin`
  fixture as a named viewer so the flipped cases still read as "the admin
  role changed behaviour here on purpose".
- Update the file-header comment's description of the rule.

### 3.4 Files explicitly not changed

- `components/sites/feed-visibility-toggle.tsx` — all its strings scope the
  effect to the feed and remain true; the "owner or org admin may toggle"
  permission it reflects is server-side and out of scope.
- `app/(app)/feed/page.tsx:104` ("…unless it's marked private") — still true.
- `services/api/*`, `db/*`, `cli/*`, `services/mcp/*`, `packages/sdk/*` —
  see §5.

## 4. Behavioural consequences to accept (not bugs)

- **An admin whose org contains only teammates' private sites now sees the
  "No sites yet" empty state** even though the org has sites. This is exactly
  what a plain member in that org sees today; the states are now consistent
  across roles. No new empty-state variant.
- **The All chip count drops for admins** by the number of teammates'
  private sites. Chip counts still reconcile (`mine ⊆ allVisible`).
- **The billing usage meter can exceed the sum of sites an admin can see**
  (`billing/page.tsx:109-116` sums all sites' bytes). Pre-existing for
  members; now also true for admins. Recorded, not fixed here.
- **CLI `dropway sites --all` and MCP `list_sites` still return private
  sites.** Deliberate (decision 6): they are the inventory escape hatch.

## 5. Risks, reversibility, and what can't be undone

- **Nothing here is irreversible.** No migration, no stored data, no API
  shape change. Rollback is a git revert of one dashboard commit.
- **Highest-risk item: this reverses the deliberate admin-inventory decision
  from `a5fd9a9` (#142)**, six days old, with its rationale written into the
  code and pinned by tests. The feature description authorises the reversal;
  the commit message must say so explicitly (reference #142) so the history
  reads as a decision, not an accident.
- **The trap-door regression is the failure mode to guard**: any
  implementation that filters the owner's own private site out of the list
  ships a bug that looks like data loss (private site becomes unreachable in
  the UI, including the settings page that would un-private it). The new
  "admin sees their OWN hidden site" test plus the existing "member sees
  their OWN hidden site" test are the automated guard; the manual check in §6
  is the end-to-end one.
- **This remains a discovery filter, not a security boundary.** The API
  returns every org row to every member and `/sites/<id>` stays resolvable by
  direct link. If a hard boundary is ever required, that is an API/RLS
  feature of a different size — do not let this ticket's language imply it
  shipped one.
- **No analytics exist on the site list**, so we cannot measure lost admin
  usage after the fact. Accepted; instrumenting first was ruled out by the
  feature being ordered as-is.

## 6. Verification

Automated (run in `apps/dashboard/`):

- `pnpm typecheck` — also proves the `canManage` removal found every usage.
- `pnpm test` — `test/sites-visibility.test.ts` must pass with the §3.3
  edits and fail without the rule change.
- `pnpm lint`.

Manual, in order of what they'd catch:

1. **Trap door:** as any role, mark your own site private from its settings
   page, return to `/dashboard`: the site is still in All (badged "Hidden")
   and in Mine; click through to it and back into settings; toggle private
   off again.
2. **The ask:** as an org admin where a teammate has a private site: that
   site is absent from All, and the All count dropped by exactly the number
   of such sites. As that teammate: no change.
3. **Copy:** footnote renders centred under the grid at one- and two-column
   widths; no string on the page claims admins see private sites; the Hidden
   badge tooltip matches the new rule.
4. **Degraded mode:** stop the Go API/org resolution locally; the page still
   lists everything with no footnote and no filter chips (unchanged
   behaviour).

## 7. Build order

Single PR, one repo (`dropway`), commits in this order so each step is green:

1. Rule + interface change in `lib/sites-visibility.ts` **with** the test
   edits (they are one logical change; the tests won't pass separately).
2. Page changes: viewer construction, footnote collapse + `text-center`,
   tooltip, doc comments.

Estimated size: ~40 lines across three files. No feature flag — the change
is small, reversible, and a flag would leave the false copy live for one of
the two states.
