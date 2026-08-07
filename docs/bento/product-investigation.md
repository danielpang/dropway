# Centre footer links — product investigation

Stage: Product investigation. No code was changed. This document records what
exists today, what the request would actually touch, what I think we should do,
and the two decisions I need from a human before anything is built.

Request as received: *"Can you centre the contact and changelog links. Remove
the all change logs except for the first one"*.

That is two unrelated changes: one cosmetic layout change, and one destructive
content change. They have very different risk profiles, so they are assessed
separately below.

## What actually exists

There is no single footer and no single changelog. There are two of each, in two
repos, with no shared code and no shared content.

| | Marketing site (`dropway-www`) | Dashboard (`dropway`) |
| --- | --- | --- |
| Footer | `components/site-footer.tsx` — 15 links in 4 headed columns, plus wordmark, CTA, copyright, Product Hunt badge, GitHub icon | `apps/dashboard/components/dashboard-footer.tsx` — copyright + exactly two controls: Changelog and Contact |
| Changelog link | `site-footer.tsx:21`, inside the **Resources** column | `dashboard-footer.tsx:30` |
| Contact link | `site-footer.tsx:30`, inside the **Company** column, points at a Google Form | `dashboard-footer.tsx:33` — not a link, a `<button>` that opens a dialog (`components/contact/contact-dialog.tsx:38`) |
| Changelog data | 4 markdown files in `content/changelog/`, loaded by `lib/changelog.ts` | 5 entries hardcoded in `apps/dashboard/lib/changelog.ts:34` |
| Changelog page | `app/changelog/page.tsx` — public, in `sitemap.ts:9` | `app/(app)/changelog/page.tsx` — behind auth |
| Analytics | none at all | PostHog, with `$pageview` on every route (`components/analytics/posthog-provider.tsx:57`) |
| Tests covering either | none | none |

Two things follow from that table, and they drive everything else:

1. **Only the dashboard footer has "the contact and changelog links" as a
   pair.** On the marketing site those two links live in different columns,
   nine links apart, among thirteen siblings. You cannot centre just those two
   there without making them inconsistent with every other link in the same
   columns. I am therefore reading the request as being about the **dashboard
   footer**, and I need that confirmed (Decision 1).
2. **Deleting changelog entries is a two-repo, two-format job** with different
   consequences in each: the dashboard changelog is private and permalinked, the
   marketing one is public and indexed.

### What the dashboard footer looks like today

`dashboard-footer.tsx:22` is the whole layout:

```
container flex flex-col items-center justify-between gap-3 py-6 text-sm sm:flex-row
```

- Below 640px: stacked column, `items-center` — the copyright and the two
  controls are **already centred**.
- At 640px and above: one row with `justify-between` — copyright pinned to the
  left edge of the container, Changelog + Contact pinned to the right edge.

So the thing being complained about only exists on tablet and desktop. This is
read from the CSS classes, not from a render: neither repo has `node_modules`
installed in this environment and I did not install them, so I have not seen the
footer in a browser during this investigation.

One wrinkle worth knowing before anyone picks a layout: **Contact is
conditional.** `layout.tsx:104` passes `contactEnabled={Boolean(supportEmail())}`
and `lib/env.ts:116` returns undefined when `SUPPORT_EMAIL` is unset, which is
the default for a self-host (`deploy/.env.example:271`). Any centring has to
look deliberate with one control as well as two.

## Ask 1: centre the contact and changelog links

**Who has this problem.** Unknown, and I could not find out from the repos. No
issue, ticket, test, or comment references footer alignment. The request arrived
without a reporter. My honest read is that this is an internal aesthetic
preference about the authenticated app shell, most likely from someone on the
team looking at the dashboard on a wide screen, rather than a reported user
problem. That is a fine reason to make the change — it is cheap — but we should
not dress it up as user-driven.

**What they do today.** Nothing. Nothing is broken: the links work, they are
keyboard-focusable with visible focus rings, and the mobile layout already
centres. On a wide screen the two controls sit far from the copyright with a lot
of empty space between them, which reads as unbalanced given there are only two
of them.

**What the change should achieve.** The footer of the authenticated app should
look intentional on a wide screen — a small, balanced block of two controls
rather than two items flung to opposite edges. This is a polish change to the
app shell, nothing more.

**How anybody would know it worked.** Honestly: by looking at it, at 375px,
768px and 1440px, with and without `SUPPORT_EMAIL` set, in light and dark theme.
There is no metric that will move. Anyone who claims a measurable outcome for a
footer alignment change on a two-item nav is guessing. What we *can* verify
objectively is that we did not break anything while doing it: the links still
resolve, focus rings still show, the row still stacks on mobile, and nothing
overflows. That is the acceptance bar, and it belongs in a review checklist, not
a dashboard.

**The design question that has to be answered.** "Centre" is genuinely
ambiguous when there is a copyright line in the same row, and the three readings
look quite different:

- **A. Centre the whole row as one cluster.** `justify-between` becomes
  `justify-center` with a wider gap. Copyright and links sit together in the
  middle. Smallest possible diff, and the links are not truly page-centred
  because the copyright shares the cluster.
- **B. Stack and centre everything on all breakpoints.** Drop `sm:flex-row`:
  copyright on top, controls centred underneath, the mobile layout everywhere.
  Unambiguously centred links; the footer gets taller on desktop.
- **C. Copyright stays left, nav optically centred in the container.** Needs a
  three-column grid with an empty third cell (or absolute positioning). True
  centring, but it leaves a visibly empty right third and is the most fragile of
  the three.

I am not going to pick this on the requester's behalf; it is a taste call about
their own product surface. My recommendation is **B** — it is the only reading
where the links are actually centred, it removes a breakpoint instead of adding
layout machinery, and it degrades cleanly when Contact is hidden. But this is
Decision 2.

## Ask 2: remove all changelogs except the first one

**I do not think we should do this, and I would push back before building it.**

**What "the first one" means is itself ambiguous.** Both changelogs render
newest-first (`lib/changelog.ts:7`, `app/changelog/page.tsx` iterates array
order). So "the first one" is either the *newest* entry (first on the page) or
the *first ever published* (last on the page). Those are opposite outcomes:

- Newest in the dashboard: `api-keys`, 2026-07-19. Oldest: `skills`, 2026-06-24.
- Newest on the marketing site: `2026-07-21-api-keys.md` (v0.4.0). Oldest:
  `2026-06-18-initial-release.md` (v0.1.0).

I have assumed "newest" throughout, because that is what appears first visually.

**What it costs, concretely.**

- **18 permalinks die.** The dashboard changelog carries 5 entry anchors and 18
  per-change anchors — 23 in total — each with a hover-to-copy control built
  specifically so people can share a link to one item
  (`components/changelog/permalink.tsx`). `lib/changelog.ts:8` says in as many
  words: *"treat them as permalinks and only append, never renumber, so shared
  links keep resolving."* Keeping only `api-keys` deletes 4 entry anchors and 14
  change anchors, so 18 of the 23 stop resolving. Any of those already
  pasted into a support reply, a Slack thread, or a customer email becomes a
  scroll-to-top. Nothing in the repos hardcodes those anchors, so the blast
  radius is entirely in places we cannot see from here.
- **A public, indexed page loses its content.** `/changelog` on the marketing
  site is in `sitemap.ts:9` at priority 0.8, has its own OG and Twitter images
  (`app/changelog/opengraph-image.tsx`), and is advertised to crawlers and LLM
  agents in `public/llms.txt:38` and `public/agents.txt:37` as *"Release notes
  and version history."* A single-entry page makes that description untrue and
  throws away the indexed history of v0.1.0–v0.3.0.
- **It inverts the artefact's purpose.** A changelog exists to show momentum. A
  changelog with one entry on it reads as a product that shipped once. For a
  young product where the marketing site leans on "look how fast this moves,"
  that is an own goal on the surface where prospects see it.

**What evidence would justify it, and whether we have it.** The only defensible
reasons to shrink a changelog are (a) nobody reads past the top entry, or (b) the
page is so long it obscures what is new. For (a) the data exists but I cannot
reach it: the dashboard captures `$pageview` and `capture_pageleave` in PostHog,
so `/changelog` visits, scroll depth and anchor-hash traffic are all
answerable — by someone with PostHog access, which I do not have from this
environment. The marketing site has **no analytics at all**, so its `/changelog`
readership is currently unknowable by anyone. For (b) the dashboard page is
genuinely dense: 5 entries and 18 individual changes of long prose in one
scroll. That is a real, plausible motivation.

But if (b) is the motivation, deletion is the wrong fix. **Show one entry;
delete none.** Render the newest entry expanded and collapse the rest behind
"Earlier releases", or paginate. The reader sees exactly one entry — which is
what the request literally asks for visually — while every permalink keeps
resolving and the public page keeps its history and its SEO value. Same outcome,
none of the cost. If the requester's mental model is "the page should show one
release," this satisfies it. If the requester genuinely wants the history *gone*,
that is a different and much more expensive decision and I want it said out loud
(Decision 3).

**The problem I found while looking, which nobody asked about.** Both changelogs
are behind the product and have drifted apart from each other:

- The last dashboard entry is 2026-07-19. Since then, `git log` shows close to 20
  merged commits touching the dashboard, most of them user-visible, including a
  whole feature — org memory (#130, 2026-07-27) — and the Sites page rework
  (#142, 2026-08-01). None of it is in either changelog. As of today the
  changelog is about three weeks and one major feature stale.
- The two changelogs disagree about the same product. The API keys release is
  dated 2026-07-19 in the dashboard and 2026-07-21 on the marketing site. The
  AI website builder has a dashboard entry and no marketing entry. The initial
  release has a marketing entry and no dashboard entry. The marketing site merges
  skills and chat sharing into one entry; the dashboard splits them across five
  weeks. The dashboard has no `version` field; the marketing site has no `label`,
  `summary`, or per-change anchors.

If the underlying feeling behind "remove all the changelogs" is *"our changelog
doesn't reflect reality"*, then the fix is one source of truth plus a habit of
adding an entry when a user-visible change merges — not deleting the four entries
that happen to be accurate. Two changelogs that disagree is the actual defect
here. I am flagging it, not scoping it; it is a larger piece of work and it is
not what was asked for.

## Decisions I need before anything is built

1. **Which footer?** I have assumed the dashboard footer
   (`dropway/apps/dashboard/components/dashboard-footer.tsx`), because it is the
   only place Contact and Changelog appear together. If you meant the marketing
   site footer, say so — and note that centring only those two of its fifteen
   links would look wrong, so that request would become "centre the whole
   marketing footer on desktop," which is a bigger visual change to the site's
   busiest shared component.
2. **Which reading of "centred"?** A (centre the row as one cluster), B (stack
   and centre everything, my recommendation), or C (copyright left, nav optically
   centred). Also: is a taller desktop footer acceptable, which is the price of B?
3. **Do you actually want changelog history deleted, or only hidden?** My
   recommendation is hidden: show the newest entry, collapse the rest, delete
   nothing. If you want it deleted, please confirm (a) that breaking 18 shared
   permalinks is acceptable, (b) that removing v0.1.0–v0.3.0 from a
   search-indexed public page is acceptable, and (c) whether "the first one"
   means the newest or the original release. I would rather be told than guess,
   because this one is not reversible from a user's point of view even if the
   commit is.

## Deliberately out of scope

Named so nobody assumes they are coming:

- **Unifying the two changelogs** into one source of truth, and backfilling the
  shipped-but-unlogged changes since 2026-07-19. Real problem, found during this
  investigation, much larger than this request.
- **The marketing footer's information architecture** — the four columns, the
  Contact link that bypasses the `contactUrl` export at `lib/site-config.ts:26`
  and hardcodes the same Google Form URL at `site-footer.tsx:30`, the Product
  Hunt badge. Untouched.
- **Sharing footer or changelog code between the two repos.** They are separate
  apps with separate design languages; not worth coupling for this.
- **Per-entry changelog routes, RSS/Atom, or "what's new" notifications.**
  Neither repo has any of these. Plausible future work, unrelated to this ask.
- **Adding analytics to the marketing site.** It is the reason we cannot answer
  "does anyone read the public changelog," but fixing it is its own decision.
- **Tests.** Neither footer nor either changelog has any coverage today, and I
  am not proposing that a footer alignment change is the moment to add a test
  suite. Verification for this work is visual review at three breakpoints.

## Evidence I could not get

Stated plainly so the confidence here is not overread:

- **PostHog** — `/changelog` pageviews, scroll depth, and anchor-hash traffic in
  the dashboard. This is the single piece of data that would settle Ask 2. It
  exists; I cannot query it from here.
- **Any user signal for Ask 1** — no ticket, issue, or comment about footer
  alignment exists in either repo. Treat the centring as a stated preference,
  not a discovered problem.
- **A render.** No `node_modules` in either checkout in this environment, so the
  current and proposed layouts were read from Tailwind classes rather than seen
  in a browser. Whoever implements should confirm visually.
- **Where the 18 doomed changelog permalinks have been shared.** Nothing in-repo links
  to them, so the cost of breaking them is real but unmeasurable from here.
