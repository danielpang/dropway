<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Fix all sites copy — product investigation

Stage: Product investigation. No code changed; this is the write-up plus the
decisions needed before anything is built.

**Request as filed:** on the my-sites page the "You're seeing every site in the
org, including ones marked private." copy should be centred. Also private sites
should not appear in all sites — only those shared with the org or public.

**Headline finding:** the request bundles a one-line cosmetic change with a
behaviour change that reverses a deliberate decision made six days ago, and the
behaviour change as literally worded creates a trap door: a user who marks their
own site private would lose every route to it in the product UI, including the
settings page that would let them un-private it. The cosmetic half is safe. The
behavioural half should not ship as written, and for most users there is nothing
to fix at all. Details and the decisions I need are below.

---

## 1. What the page actually is, and where the copy lives

The "my sites" page is `/dashboard`. It was renamed from "My sites" to "Sites"
in `a5fd9a9` ("Change 'My sites' page to Sites and show all sites that have org
visibility. Add a one-click filter option…", #142) — the same commit that
introduced the behaviour this request wants changed.

- Page: `apps/dashboard/app/(app)/dashboard/page.tsx` (324 lines, RSC)
- Listing rule: `apps/dashboard/lib/sites-visibility.ts`
- Rule's tests: `apps/dashboard/test/sites-visibility.test.ts`
- Skeleton: `apps/dashboard/app/(app)/dashboard/loading.tsx`

The copy is a single occurrence at `page.tsx:157-163`, the admin branch of a
two-branch footnote rendered under the site grid:

```tsx
{degraded ? null : (
  <p className="text-xs text-muted-foreground">
    {viewer.canManage
      ? "You're seeing every site in the org, including ones marked private."
      : "Sites your teammates marked private are only shown to them and org admins."}
  </p>
)}
```

It is **left-aligned**, confirmed: the `<p>` carries only `text-xs
text-muted-foreground`; the page wrapper is `mx-auto max-w-5xl space-y-8`
(`page.tsx:91`) which centres the *block*, not the text; no ancestor sets text
alignment (`(app)/layout.tsx:102`, `app/globals.css`). For contrast, the sibling
empty-state and error cards in the same file *are* centred with explicit
`text-center` (`page.tsx:127`, `:268`, `:298`). So the footnote is the one
element under the grid that reads as a stray line rather than a caption. The
centring request is a reasonable and self-consistent ask.

`dropway-www` contains no sites page and no occurrence of this copy. **This
feature is dropway-only.** (`dropway-www` does say "private" in two places —
`content/changelog/2026-07-18-embed-sites.md:20`,
`app/mcp/page.tsx:50` — but both mean *access-gated*, which is the other axis;
see §2.)

## 2. The visibility model: two orthogonal axes, one overloaded word

This is the crux, and the request's wording sits across both axes.

| Axis | Field | Values | What it controls |
| --- | --- | --- | --- |
| Edge access control | `app.sites.access_mode` | `public`, `password`, `allowlist`, `org_only` | Who can load the served bytes |
| Discovery | `app.sites.feed_visible` | `true` (default) / `false` | Whether the site shows up in the org feed |

`db/migrations/app/0005_site_feed_visible.sql:9-15` states the separation
explicitly: `feed_visible` was modelled as a boolean rather than a fifth access
mode precisely so "private" never touches the edge projection or authz. A
private site "keeps whatever access mode it had, it's just hidden from the feed
listing."

In the dashboard, "marked private" means `feed_visible = false`. On the
marketing site and in the access UI, "private" means access-gated. **The word is
overloaded across two independent axes**, which is very likely part of why this
request was filed.

The filed wording — "only those that are shared with org or public" — names
`org_only` and `public`, which are *access modes*. Taken literally it would also
exclude `password` and `allowlist` sites, which are neither. I do not believe
that is the intent; I read it as "only sites that are `feed_visible`". **This
needs confirming (decision 2 in §8) — the two readings produce different
products.**

## 3. Who has this problem

I have no usage data, no user report, no support thread and no issue behind this
request. I am stating that plainly rather than dressing up inference as
evidence. What I can do is establish exactly who the current behaviour affects,
from the code.

The listing rule (`lib/sites-visibility.ts:35-42`):

```ts
export function isSiteVisibleTo(site, viewer): boolean {
  if (viewer.canManage) return true;              // owners/admins keep a full inventory
  if (site.feed_visible !== false) return true;   // shared to the org
  return Boolean(viewer.userId) && Boolean(site.owner_id) && site.owner_id === viewer.userId;
}
```

`canManage` = role `owner` or `admin` (`lib/org.ts:148-151`).

Three populations:

1. **Plain org members (the majority).** They already do not see teammates'
   private sites. Line 40 keeps org-shared sites; line 41 keeps only *their
   own*. For this group, the requested behaviour is the behaviour that already
   ships. **There is nothing to fix.**
2. **Org owners and admins.** The only viewers for whom private sites appear in
   All, via the `canManage` short-circuit on line 39. This is the entire
   behavioural surface of the request.
3. **Owners of a private site (any role).** They see their own private site in
   the list, badged "Hidden". `/dashboard` is their only unconditional route to
   it (see §5).

So this is an **admin-experience request, not a member-privacy problem**. That
does not make it invalid — an admin who opens All and sees a teammate's site
badged "Hidden" may reasonably feel the product is exposing something it
shouldn't. But the framing "private sites should not appear in all sites"
implies a leak that does not exist for ordinary members, and the fix follows
from which of those two things is actually wrong.

## 4. What people do today

- **Member** opens `/dashboard`: sees org-shared sites plus their own (including
  their own private ones), All/Mine chips with counts, and the footnote
  "Sites your teammates marked private are only shown to them and org admins."
- **Admin** opens `/dashboard`: sees every site in the org, teammates' private
  ones carrying a "Hidden" badge whose tooltip reads "Hidden from the org feed.
  Only its owner and org admins see it in this list." (`page.tsx:243`), plus the
  footnote in question explaining why the list is complete.
- **Someone making a site private** uses the toggle at
  `components/sites/feed-visibility-toggle.tsx:60-67`: "When on, this site
  appears in your organization's feed so teammates can discover it. Turn it off
  to keep the site private. It stays out of the feed, but its access settings
  are unchanged." Confirmation: "Hidden from the org feed. This site is
  private." Every string scopes the consequence to *the feed*.
- **Anyone wanting the full inventory** outside the dashboard already has it,
  unfiltered: `dropway sites --all` (`cli/internal/cmd/sites.go:56-64` filters on
  ownership only, never on `feed_visible`) and the MCP `list_sites` tool
  (`services/mcp/internal/tools/tools.go:437`).

Server side, nothing filters: `GET /v1/sites` takes no query params
(`services/api/internal/handlers/sites.go:172-208`) and
`ListSites` is `WHERE org_id = $1` with no visibility predicate
(`db/sqlc/query.sql:201-205`). By contrast the feed *does* filter in SQL:
`WHERE s.feed_visible AND s.org_id = …` (`db/sqlc/query.sql:220`). The dashboard
rule is a client-side discovery filter, documented as such
(`lib/sites-visibility.ts:7-13`) — **it is not a security boundary, and nothing
in this feature would make it one.**

## 5. The blocking problem with the literal request

If private sites are simply filtered out of the list, the owner of a private
site is stranded. `mine` is derived from the already-filtered list
(`page.tsx:82`):

```tsx
const allVisible = degraded ? (sites ?? []) : (sites ?? []).filter(s => isSiteVisibleTo(s, viewer));
const mine = allVisible.filter(s => Boolean(s.owner_id) && s.owner_id === viewer.userId);
```

Remove private sites from `allVisible` and they leave **both** All and Mine. I
checked every discovery surface in the dashboard for a fallback. There is none:

| Surface | Survives? |
| --- | --- |
| `/dashboard` site card (`page.tsx:204`) | No — this is what is being removed |
| Feed "Open site" (`components/feed/feed-post.tsx:340`) | No — `WHERE s.feed_visible` already excludes it, with no owner exemption |
| `/sites` index route | Does not exist (only `app/(app)/sites/[id]/`) |
| Global search / ⌘K | Does not exist anywhere in the dashboard |
| Billing usage card (`app/(app)/billing/page.tsx:106-117`) | Sums `storage_bytes` into two scalars; never renders site rows or links |
| Members storage column (`app/(app)/members/page.tsx:48-53`) | Keyed by `user_id`; no site identity survives |
| Audit log target (`components/audit/audit-table.tsx:125-129`) | Plain unlinked text, and admin-only |
| Chats attached-site link (`app/(app)/chats/page.tsx:127`) | Only if that site happens to have a chat log attached |
| Post-create redirect (`components/sites/new-site-dialog.tsx:119`) | One-shot at creation |

So: mark your site private, navigate away, and you can only reach it again by
remembering the raw `/sites/<uuid>` URL, or dropping to the CLI/MCP to recover
it. **Including the site's own settings page — the only place to turn private
back off.** And the toggle that got you there promised the change was
feed-only.

This is a trap door, not an edge case, and it is a precondition on any version
of the behaviour change. It is also the single most useful thing this
investigation produced: implemented naively from the ticket text, this feature
ships a bug that looks like data loss.

## 6. Options

**A — Drop the admin exception only.** Delete the `canManage` short-circuit;
the rule becomes "shared to the org, or mine". Admins stop seeing teammates'
private sites; members unaffected; owners keep their own private sites in both
All and Mine, so no trap door. Smallest change that satisfies the request's
intent. *Recommended, subject to §8.*

**B — Strict reading.** No private site appears in All, including your own;
your private sites appear only under Mine. Requires sourcing `mine` from the
unfiltered list, and then the chip counts stop reconciling (All 3 / Mine 4),
which needs a third chip or a redesign of the counts. More work, more confusion,
no clear gain over A.

**C — Copy only.** If the real complaint is "this surprised me / it reads like a
leak", then rewriting the footnote (and the Hidden tooltip) so the rule is
obvious, plus the centring, may be the whole fix — and it preserves the admin
inventory. Cheapest, and honestly the option to take if nobody can name a user
who is harmed by today's behaviour.

Under A or B the copy must change anyway: **all three of these statements become
false**, so the sentence this ticket asks to centre is a sentence that has to be
rewritten first.

- `page.tsx:160` "You're seeing every site in the org, including ones marked private."
- `page.tsx:161` "…only shown to them and org admins."
- `page.tsx:243` tooltip "Only its owner and org admins see it in this list."

Known cost of A/B that I am not going to hide: an admin doing storage cleanup or
offboarding a departing member loses the dashboard inventory of sites they are
still billed for. The billing page sums `storage_bytes` across **all** sites
including private ones (`billing/page.tsx:109-116`), so the usage meter would
report bytes the admin cannot attribute to any site they can see. And the
dashboard becomes the only filtered surface while CLI `--all` and MCP
`list_sites` stay unfiltered.

## 7. What the change should achieve, and how anyone would know

Outcomes, not implementation:

1. Nobody browsing the org's site list encounters a site a teammate
   deliberately marked private.
2. A user who marks their own site private can still find and manage it from the
   dashboard without knowing its URL.
3. The footnote, the "Hidden" badge tooltip, and the settings toggle description
   all describe the same rule, and that rule is true.
4. The footnote reads as a deliberate caption under the grid rather than a stray
   line.

Checks, in order of what they would actually catch:

- **Trap-door regression (the one that matters).** Mark a site private from its
  settings page, return to `/dashboard`, confirm you can still click through to
  it and back to its settings. This is the check that catches the §5 bug.
- **Unit.** `test/sites-visibility.test.ts` already pins the whole contract.
  "admin sees a teammate's hidden site" (L48-53) and "admin sees an unowned
  hidden site" (L54-59) must flip `true → false`; "member sees their OWN hidden
  site" (L42-47) must stay `true`. The mixed-list case at L98-114 (member
  `["a","c","d"]` / admin `["a","b","c","d"]`) should collapse to one
  expectation for both roles. If a change lands and those assertions did not
  need editing, the change did not do what the ticket asked.
- **Manual.** As an admin in an org where a teammate has a private site: that
  site is absent from All, and the All chip count drops by exactly the number of
  such sites. As that teammate: unchanged.
- **Copy.** No branch of the footnote, and no badge tooltip, asserts that admins
  see private sites. The footnote renders centred under the grid at both one-
  and two-column widths, and `loading.tsx` still doesn't shift on swap-in (it
  currently reserves no space for the footnote, which is fine either way).
- **Known-failing consistency checks** that are *not* bugs in this change but
  must be recorded: `dropway sites --all` and MCP `list_sites` still return
  private sites; billing usage still counts their bytes.

On measurement, honestly: the dashboard emits no analytics beyond `$pageview`,
`site_created` and `error_page_viewed` (`lib/analytics-server.ts`,
`components/analytics/posthog-provider.tsx:67`). There is no event on the site
list or the All/Mine chips, so **we cannot measure whether anyone valued the
admin inventory before removing it.** One thing we *can* recover, because the
filter is URL-driven: `$pageview` with `$current_url` distinguishes `/dashboard`
from `/dashboard?owner=me`, so historical Mine-filter usage is already in
PostHog. If we want evidence about the admin inventory specifically, it has to
be instrumented *before* the change, not after.

## 8. Decisions I need (I am not guessing at these)

1. **A, B or C?** Concretely: should an org owner/admin still be able to see
   teammates' private sites anywhere in the dashboard? I recommend **A**, and I
   recommend keeping an admin path only if someone can name the workflow that
   needs it (offboarding and storage cleanup are the two candidates I found).
2. **Confirm "private" = `feed_visible = false`**, not `access_mode`. If the ask
   is really about access modes, this is a different feature and I would push
   back on the scope.
3. **Must the owner keep seeing their own private site under All, or only under
   Mine?** A cannot ship without this. I recommend All, because Mine-only breaks
   the chip counts (§6 B).
4. **Should the footnote still be centred once it is rewritten?** The new
   sentence will be shorter and may not be the same visual problem. I would
   still centre it — the surrounding cards are centred — but flagging that the
   original justification changes.
5. **Who owned #142?** That commit chose "owners/admins keep a full inventory"
   deliberately, wrote the rationale into the code, and pinned it with a test.
   Reversing it is legitimate, but it should be an explicit decision by that
   owner, not a side effect of a `text-align` ticket.

## 9. Deliberately out of scope

Named so nobody assumes they are covered:

- **Server-side enforcement.** `GET /v1/sites` keeps returning every org row,
  and `/sites/<id>` stays resolvable by direct link for any org member. This
  remains a discovery filter. If the actual requirement is "members must not be
  able to *reach* teammates' private sites", that is an API/RLS change of a
  different size and should be its own feature.
- **CLI, MCP and SDK parity.** `dropway sites --all` and MCP `list_sites` stay
  unfiltered. This is the largest known inconsistency the change would create; I
  am excluding it rather than ignoring it, and recommend a follow-up decision.
- **`access_mode` semantics.** `password` and `allowlist` sites are untouched.
- **Renaming the overloaded word "private"** across the dashboard, the marketing
  site and the docs. Real problem (§2), separate piece of work.
- **The org feed, skills, and skill visibility** (`feed_visible` on
  `app.skills`), which mirror this model and would inherit any decision here.
- **Billing/usage attribution** for sites an admin can no longer see (§6).
- **A new "Private" filter chip** — only in play if option B is chosen.
- **`loading.tsx`** skeleton changes; it reserves no footnote space today and
  does not need to.

## 10. Where the evidence does not support building this

Stated plainly, as instructed:

- **For plain members, the requested behaviour already ships.** If this request
  came from an assumption that members can see teammates' private sites, the
  premise is factually wrong, and the right response is to close the behavioural
  half and fix only the copy that created the impression.
- **The behavioural half reverses a six-day-old deliberate decision** with a
  written rationale and a test pinning it. That is worth doing on purpose, and
  not worth doing as an incidental rider on a centring fix.
- **There is no user report, no support thread and no analytics behind it.** I
  found no data on how many sites are private, or on whether the admin
  inventory is used at all.
- **The centring change stands on its own** and is safe, one line, reversible.
  It should ship *after* the copy is settled, because under A or B the sentence
  being centred is false.

My recommendation: ship the centring, rewrite the three copy strings so the
rule is unambiguous, and treat the visibility change as a separate decision —
option A, gated on fixing the §5 trap door first. If nobody can name the harmed
user, option C is the honest answer.
