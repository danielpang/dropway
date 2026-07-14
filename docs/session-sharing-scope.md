<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Share This Session — free vs. paid scoping

How "Share This Session" (PRD 2) is packaged across plan tiers, and what the
free tier's caps are. This is the pricing/entitlement scope for the feature;
the product shape is summarized only far enough to make the levers concrete.

**Feature recap.** Every site carries an optional **append-only chat log**:
the conversation that produced the artifact, pasted or uploaded from Claude
Code, ChatGPT, Cursor, or plain text — via dashboard, MCP, or CLI. Viewers of
the site see a collapsible **"How this was made"** panel rendering the log.
There is no separate "session" object to create, attach, or manage: messages
are appended to the site's one log, each message is stamped with the site's
current deploy version at append time (so the panel can group the history by
version), and the log inherits the site's access tier (public / password /
allowlist / org) — a gated site's log is exactly as gated as the site itself.
A log can also stand alone, with no deployed artifact at all: a **chat-only
site** whose page *is* the transcript (see *Standalone chat logs* below).

## Packaging principle: free gets the feature, paid gets the depth

Session sharing should **not** be paid-only. Two reasons:

1. **It is the viral surface.** A "How this was made" panel on a public site
   is Dropway marketing in front of exactly the audience we want (people
   receiving AI-built artifacts). Cutting free users off would cut off the
   loop. This mirrors the existing free-tier attribution banner: the panel on
   free-tier sites carries "Made with AI · Shared via Dropway" branding, and
   removing that branding is itself a paid perk.
2. **The tool-agnostic pitch needs volume.** "Works for Cursor, ChatGPT,
   manual notes" only lands if hobbyists actually append their history. The
   paid pitch ("full iteration history for client handoffs, with access
   control") is the professional layer on top.

So: the lever is **log depth** — how much history a site's log holds. The
bands deliberately mirror the site-count bands (Free 10 → Pro 100 →
Business/Enterprise unlimited), so the pricing story stays one sentence:
"free keeps your last 10 messages, Pro holds 100, Business is uncapped."

## Tier matrix

| Lever | Free | Pro ($25) | Business ($150) | Enterprise |
| --- | --- | --- | --- | --- |
| Append to the log (dashboard, MCP, CLI) | ✅ | ✅ | ✅ | ✅ |
| **Log depth per site** | **Rolling last 10** (older rows auto-pruned) | **100 hard cap** (402 past it) | Unlimited | Unlimited |
| Message size (validation, all tiers) | 64 KiB | 64 KiB | 64 KiB | 64 KiB |
| Per-version grouping in the panel | ✅ | ✅ | ✅ | ✅ |
| Standalone chat-only sites | ✅ (counts toward the 10-site cap) | ✅ (100-site cap) | ✅ | ✅ |
| "Shared via Dropway" attribution on panel | Always on | Removable | Removable | Removable |
| Access-control inheritance | ✅ | ✅ | ✅ | ✅ |
| Org kill switch (`chat_log_enabled`) | ✅ | ✅ | ✅ | ✅ |

Notes on each lever:

- **Free is a rolling window, not a wall.** Appends on the free tier never
  fail: inserting past 10 deletes the oldest rows in the same transaction,
  so the log always holds the newest 10 messages. This is deliberate
  first-run UX — a pasted 50-message conversation imports cleanly (newest 10
  kept) instead of erroring, and the trimmed remainder becomes the upgrade
  pitch ("keep your full history" → Pro). Pruning is disclosed, never
  silent: the importer reports what was trimmed, and the free panel footer
  notes "showing the last 10 messages."
- **Paid tiers never auto-delete — that's why Pro is a hard cap.** The
  asymmetry is intentional: silently pruning a *paying* customer's shipped
  history is worse than a wall. Free trades disclosure-based pruning for a
  frictionless feature; Pro gets an explicit 402 at the 101st message
  (`next_tier: business`) and chooses to delete or upgrade. Owners/admins
  can delete individual messages on any tier (mistakes, pasted secrets),
  which frees Pro cap slots.
- **Message size is validation, not a tier lever.** 64 KiB per message on
  every tier keeps any single append bounded (and the whole free/pro log
  under ~640 KiB / 6.4 MiB worst-case) without introducing a second
  purchasable axis. Two overlapping paid levers on one feature muddy the
  upgrade story.
- **Version stamping is universal.** Each message records the site's
  `current_version_id` at append time. The panel groups messages by the
  version they accompanied, and rolling back a site still shows an honest
  history (the log narrates the whole journey rather than being pinned to
  one deploy). This keeps the PRD's "context of how it was made" promise on
  every tier — the paid tiers buy *depth*, not honesty.
- **Attribution.** Reuses the `RouteValue.plan_tier` plumbing the free-tier
  site banner already uses; no new serving-side state.
- **Not levers.** Access-control inheritance is inherent to serving the panel
  under the site's existing authz — gating it per-tier would ship a *less*
  safe free product, so it's universal. The org kill switch is governance,
  and governance toggles (like `mcp_enabled`) are free on every tier. Ingest
  surfaces (dashboard/MCP/CLI) are universal because the append contract is
  shared; gating a surface would fork client code, not policy.

## Data & API shape (summary)

One new RLS-scoped tenant table, `app.site_chat_messages` — append-heavy
rows like `ai_messages`, not a content-addressed blob: `(id, org_id,
site_id, seq, version_id, created_by, source_tool, role, content,
created_at)` with `UNIQUE (site_id, seq)`. `seq` stays monotonic across
pruning (it never reuses numbers), so clients can page stably and the panel
can say "messages 41–50 of a longer conversation." The API is a plain
append/list pair — `POST /v1/sites/{id}/chat` (single message or a batch
import from a pasted export, parsed by an `internal/chatspec` normalizer),
`GET /v1/sites/{id}/chat` (paginated), `DELETE /v1/sites/{id}/chat/{seq}` —
plus an MCP `append_chat` tool (OAuth-forwarding, like `deploy_site`) and
`dropway chat append` in the CLI. Viewers never touch the Go API: the
serving Worker exposes the log at a reserved path on the site's own host
(`/__dropway/chat`), resolved through the same KV-rebuildable-from-Postgres
route projection and the same authz as every other asset — which is what
makes "no Claude account needed to view" true.

## Standalone chat logs — a chat is just a site

Sharing a conversation that isn't attached to any deployed artifact does NOT
introduce a second shareable object. It reuses the site: `app.sites` gains a
`kind` column (`'site'` default, `'chat'`), and a chat-only site is an
ordinary site row with zero deploys whose serving path renders the log as the
whole page. Concretely:

- **Creation:** `dropway chat share <export-file>` (CLI) and a `share_chat`
  MCP tool do create-site(kind=chat) + batch append in one step and return
  the live URL; the dashboard offers "Share a conversation" alongside "New
  site." The same `chatspec` normalizer parses the export.
- **Serving:** the Worker already special-cases the log at
  `/__dropway/chat`; for `kind='chat'` sites it serves a built-in transcript
  page at `/` (no user deploy exists, so there is nothing else to route).
  Messages' `version_id` stamps are simply NULL — there are no versions.
- **Everything inherited, nothing duplicated:** the four access tiers,
  edge authz + instant revocation, custom domains, the org feed
  (votes/comments — chat posts render like site posts with a kind badge),
  audit logging, and the org kill switch all apply because a chat *is* a
  site. Building a separate "shared transcript" object would have meant
  re-implementing each of those surfaces.
- **Quota needs zero new resources:** a chat-only site counts toward the
  existing `sites_per_org` bands (free 10 / pro 100 / business+
  unlimited), and its log obeys the same per-site message bands above
  (free rolling-10 / pro 100 / unlimited). The two levers compose: a free
  org gets up to 10 shares total — artifacts or conversations — each
  carrying its last 10 messages. Later attachment is trivial: deploying
  files to a chat site just flips it into a normal site with its history
  already in place.

## Enforcement mechanics (small seam extension + existing patterns)

- **One new resource in `internal/quota`:** `ResourceChatMessagePerSite`
  (discrete). Bands live in `cloud/quota` behind the `cloud` build tag; the
  FSL core never sees them.
- **The seam gains one method** for window semantics: alongside
  `Allow`/`AllowN`, the provider exposes `RetentionWindow(planTier, res)
  (n int64, ok bool)`. Cloud returns `(10, true)` for free; every paid tier
  and the core `Unlimited` provider return `ok=false`. The store's append
  transaction, under the per-site advisory lock, does: window set → INSERT
  then DELETE rows beyond the newest `n`; no window → COUNT →
  `AllowN(current, n)` → INSERT (the standard 402 path, which is how Pro's
  100-cap fires). Policy stays pure and unit-testable; the DB mechanics stay
  in the store.
- **Batch imports** pass `n` = messages in the batch. On free the whole
  batch lands and pruning keeps the newest 10; on Pro the batch either fits
  entirely or 402s before any row lands (no partially-imported
  conversations).
- **Standard 402** (Pro only now): the existing `ExceededError` body
  (`{limit, current, max, plan_tier, next_tier: "business", upgrade_url}`),
  which the dashboard's upgrade modal, the CLI, and MCP error mapping
  already understand.
- **Downgrade prunes lazily, not retroactively.** Dropping from a paid tier
  with a >10-message log does NOT delete anything at downgrade time — links
  a client was already sent keep working. The window applies on the *next
  append*, which prunes to the newest 10 like any free append. Same "binds
  on next action" posture as the site-count downgrade.
- **Storage accounting:** message rows live in Postgres (bounded by
  window/cap × 64 KiB); no blob-store or storage-meter involvement.

## Upgrade moments

1. **Import trim on free:** pasting a conversation longer than 10 messages
   succeeds, and the importer's "kept the last 10 of 47 — keep your full
   history with Pro" notice is the feature's highest-intent CTA. The same
   notice appears in the free panel footer.
2. **101st message on Pro** → 402 `next_tier: business` (upgrade modal /
   CLI upgrade message).
3. **Soft prompt:** the free-tier panel's "Shared via Dropway" attribution
   links to the product; removing it is part of the Pro pitch.

## Deliberate non-goals for v1 packaging

Per-viewer log analytics ("who read the history") as a Business upsell —
plumbing exists via the access audit log but the panel should earn usage
first; per-org message pools or storage metering for chat rows (bounded and
too cheap to meter); pinning logs to a single deploy version (the append
log narrates all versions; per-message version stamps cover grouping);
recovering pruned free-tier rows after an upgrade (pruned means deleted —
the importer said so at trim time); and any self-host restriction — OSS
builds use the core `Unlimited` provider and get everything, uncapped, per
the open-core boundary.
