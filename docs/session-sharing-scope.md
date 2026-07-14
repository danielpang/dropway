<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Share This Session — free vs. paid scoping

How "Share This Session" (PRD 2) is packaged across plan tiers, and what the
free tier's caps are. This is the pricing/entitlement scope for the feature;
the product shape is summarized only far enough to make the levers concrete.

**Feature recap.** A **chat log** is a first-class org object: an append-only
conversation history, pasted or uploaded from Claude Code, ChatGPT, Cursor,
or plain text — via dashboard, MCP, or CLI. Entries are either conversation
turns or **LLM-authored action annotations** — the model commenting on a tool
run or file edit it just performed ("Inlined the font to satisfy the CSP"),
so the log tells what was *done*, not only what was said. **Site attachment
is optional and re-pointable.** A log can be:

- **Attached to a site** — viewers of the site see a collapsible **"How this
  was made"** panel rendering the log, served under the site's access tier
  (public / password / allowlist / org). While attached, each appended
  message is stamped with the site's current deploy version, so the panel
  groups history by version. One attached log per site.
- **Unattached** — an org-internal conversation in the dashboard's chat
  library: browsable by the org, appendable, but with no viewer surface
  until it's attached to a site or published standalone.
- **Published standalone** — shared without any deployed artifact by
  attaching it to a **chat-only site** whose page *is* the transcript (see
  *Standalone publishing* below).

Attach, detach, and move are metadata operations on the log; the messages
never copy or migrate.

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

So: the lever is **log depth** — how much history a chat log holds. The
bands deliberately mirror the site-count bands (Free 10 → Pro 100 →
Business/Enterprise unlimited), so the pricing story stays one sentence:
"free keeps your last 10 messages, Pro holds 100, Business is uncapped."

## Tier matrix

| Lever | Free | Pro ($25) | Business ($150) | Enterprise |
| --- | --- | --- | --- | --- |
| Create/append logs (dashboard, MCP, CLI) | ✅ | ✅ | ✅ | ✅ |
| **Messages per chat log** | **Rolling last 10** (older rows auto-pruned) | **100 hard cap** (402 past it) | Unlimited | Unlimited |
| Chat logs per org | Unlimited (dormant seam) | Unlimited | Unlimited | Unlimited |
| Attach / detach / move between sites | ✅ | ✅ | ✅ | ✅ |
| Standalone publishing (chat-only site) | ✅ (counts toward the 10-site cap) | ✅ (100-site cap) | Unlimited | Unlimited |
| Message size (validation, all tiers) | 64 KiB | 64 KiB | 64 KiB | 64 KiB |
| Per-version grouping in the panel | ✅ | ✅ | ✅ | ✅ |
| "Shared via Dropway" attribution on panel | Always on | Removable | Removable | Removable |
| Access-control inheritance | ✅ | ✅ | ✅ | ✅ |
| Org kill switch (`chat_log_enabled`) | ✅ | ✅ | ✅ | ✅ |

Notes on each lever:

- **Free is a rolling window, not a wall.** Appends on the free tier never
  fail: inserting past 10 deletes the oldest rows in the same transaction,
  so a log always holds its newest 10 messages. This is deliberate
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
- **Logs per org is uncapped with a dormant seam.** `ResourceChatLogPerOrg`
  exists but returns unlimited on every tier (the `ResourceSkillPerOrg`
  pattern), because per-log content is already tightly bounded
  (window/cap × 64 KiB) and the viewer-facing surfaces are bounded by the
  site caps. The seam means abuse of the unattached library can be
  tightened in the provider without a store or handler change.
- **Message size is validation, not a tier lever.** 64 KiB per message on
  every tier keeps any single append bounded (and a whole free/pro log
  under ~640 KiB / 6.4 MiB worst-case) without introducing a second
  purchasable axis. Two overlapping paid levers on one feature muddy the
  upgrade story.
- **Version stamping happens while attached.** Each message appended while
  the log is attached records the site's `current_version_id`; messages
  appended unattached (or on a chat-only site) stamp NULL. The panel groups
  by stamp, and rolling back a site still shows an honest history — the log
  narrates the whole journey rather than being pinned to one deploy. Depth
  is what paid tiers buy, not honesty.
- **Attribution.** Reuses the `RouteValue.plan_tier` plumbing the free-tier
  site banner already uses; no new serving-side state.
- **Not levers.** Attach/detach/move is metadata, universal. Access-control
  inheritance is inherent to serving the panel under the site's existing
  authz — gating it per-tier would ship a *less* safe free product, so it's
  universal. The org kill switch is governance, and governance toggles
  (like `mcp_enabled`) are free on every tier. Ingest surfaces
  (dashboard/MCP/CLI) are universal because the append contract is shared;
  gating a surface would fork client code, not policy.

## Data & API shape (summary)

Two new RLS-scoped tenant tables:

- **`app.chat_logs`** — the aggregate: `(id, org_id, site_id NULLABLE →
  app.sites, title, source_tool, created_by, created_at)`, with a partial
  unique index on `site_id` (one attached log per site). Attach/detach/move
  is an UPDATE of `site_id`; nothing else moves.
- **`app.chat_messages`** — **one message per row**, append-heavy like
  `ai_messages`: `(id, org_id, chat_log_id, seq, version_id NULLABLE,
  created_by, role, kind, content, meta jsonb NULLABLE, created_at)` with
  `UNIQUE (chat_log_id, seq)`. `seq` stays monotonic across pruning
  (numbers are never reused), so clients page stably and the panel can say
  "messages 41–50 of a longer conversation."

  **Two message kinds.** `kind = 'chat'` (default) is a conversation turn.
  `kind = 'action'` is an **LLM-authored annotation about work performed** —
  a tool invocation or file edit — where `content` is the model's
  commentary ("Switched the chart to a log scale per the feedback in the
  previous message") and `meta` carries the structured facts the UI renders:
  `{action: 'tool_use' | 'file_edit', tool?: string, paths?: string[]}`
  (validated by `chatspec`: known action enum, ≤ 20 paths, clean relative
  paths only). Annotations are ordinary rows in every mechanical respect —
  they count toward the tier bands and are pruned by the free window like
  any other message. One band, no kind carve-outs: exempting annotations
  would invite smuggling conversation past the cap by labeling it an
  action, and an agent that narrates verbosely should feel the same
  pressure to be selective on free that a human does.

API — logs are the primary resource, with a site-scoped convenience:

- `POST /v1/chats` — create a log, optionally with `site_id` and an inline
  batch import (a pasted export, parsed by the `internal/chatspec`
  normalizer). The normalizer flattens raw tool-call/tool-result JSONL
  noise rather than giving every event a row, but can (opt-in,
  `derive_actions`) condense runs of tool events from an export into
  `kind='action'` rows — e.g. "Edited `src/app.tsx`, `styles.css`" — so
  even an after-the-fact import carries the activity trail. Commentary on
  those derived rows is whatever the export contained; the *good* comments
  come from the live path below.
- `POST /v1/chats/{id}/messages` (single or batch append), `GET
  /v1/chats/{id}` + `/messages` (paginated), `DELETE
  /v1/chats/{id}/messages/{seq}`, `DELETE /v1/chats/{id}`.
- `PUT /v1/chats/{id}/site` `{site_id | null}` — attach / detach / move
  (audit-logged; rejects a site that already has a log attached).
- `GET /v1/sites/{id}/chat` — convenience read of the attached log;
  `POST /v1/sites/{id}/chat` appends to it (creating one if absent), which
  keeps the deploy-then-attach flow one call for agents.
- **MCP** `share_chat` (create/import, optional site binding) and
  `append_chat`, both OAuth-forwarding like `deploy_site`. `append_chat`
  accepts either kind, so a *working agent narrates as it goes*: after an
  edit or tool run it appends
  `{kind: 'action', action: 'file_edit', paths: ['index.html'],
  content: 'Inlined the font to satisfy the CSP'}` — the tool description
  tells the agent to comment on *why*, not to restate the diff. **CLI**
  `dropway chat share <file> [--site <name>]`, `chat append [--action
  file_edit --path <p> ...]`, `chat attach --site <name>`, `chat detach`.

Viewers never touch the Go API: the serving Worker exposes the attached
log at a reserved path on the site's own host (`/__dropway/chat`), resolved
through the same KV-rebuildable-from-Postgres route projection and the same
authz as every other asset — which is what makes "no Claude account needed
to view" true. An unattached log has no viewer surface at all; it is
dashboard/API-only until attached or published.

## Standalone publishing — a published chat is just a site

Sharing a conversation that isn't attached to any deployed artifact does NOT
introduce a second serving/access surface. `app.sites` gains a `kind` column
(`'site'` default, `'chat'`): publishing a standalone log creates a site row
with zero deploys, attaches the log to it, and the Worker serves a built-in
transcript page at `/` (there is no user deploy to route). Everything is
inherited — the four access tiers, edge authz + instant revocation, custom
domains, the org feed (chat posts render like site posts with a kind badge),
audit, and the kill switch — because a published chat *is* a site. It counts
toward the existing `sites_per_org` bands (free 10 / pro 100 / business+
unlimited), so quota composes with zero new resources: a free org gets up to
10 shares total — artifacts or conversations — each carrying its last 10
messages. Deploying files to a chat-only site later flips it into a normal
site with its history already attached, and detaching the log from any site
returns it to the unattached library with its messages intact.

## Viewer UI — one renderer, two surfaces

The transcript viewer is a single self-contained page the Worker serves at
`/__dropway/chat`, and both viewing surfaces are that page:

- **On a deployed site**, the Worker (HTMLRewriter, only for `text/html`
  responses of sites with an attached log) injects one small deferred
  script before `</body>`. It renders a floating **"✨ How this was made"**
  pill in the corner; clicking it slides in a right-side drawer (bottom
  sheet on mobile) whose body is an `<iframe>` of `/__dropway/chat` on the
  same host — so the site's authz token/cookie applies to the transcript
  automatically and the artifact's own DOM/CSS is never touched beyond the
  pill. `Esc` or a backdrop click closes it; the pill honors
  `prefers-reduced-motion`. Site owners can switch the panel off per site
  (`chat_panel_enabled`) without detaching the log.
- **On a chat-only site**, the same page *is* `/` — no pill, no drawer,
  just the full-width transcript.

The transcript page itself:

- **Conversation layout, not a log dump.** Role-distinct bubbles (user
  right-aligned on an accent tint, assistant left on neutral; the
  `source_tool` shows once as a header badge — "Claude Code", "ChatGPT",
  "Cursor" — not per message), relative timestamps, and a sticky header
  with the log title and message count.
- **Version dividers.** Where consecutive messages' `version_id` stamps
  differ, a divider line reads "↑ deployed as v3" — the iteration story the
  PRD is about, visible at a glance. NULL stamps (unattached/chat-only
  appends) simply omit dividers.
- **Action annotations render as activity rows, not bubbles.** A
  `kind='action'` message is a compact full-width row between bubbles: an
  icon (✏️ file edit, 🔧 tool use), the targets as code chips
  (`index.html`, `npm test`), and the LLM's commentary in one line,
  expanding on click if longer. Consecutive action rows collapse into a
  group ("3 edits · 1 command" → expandable), so a burst of agent activity
  reads as one beat of the story instead of drowning the conversation.
- **Markdown, safely.** Message content renders through the same
  dependency-free, escape-first renderer pattern as `lib/markdown.ts` —
  this page is served on the untrusted-content domain under its strict
  CSP, so it must be fully self-contained: inline CSS, no external fonts,
  scripts, or fetches, light/dark via `prefers-color-scheme`. Code blocks
  get a monospace treatment with a copy button; long messages and bulky
  tool output collapse behind "show more."
- **Shareable positions.** Each message anchors as `#msg-<seq>`, so "look
  at message 41" is a link; the header offers "copy as Markdown" for the
  whole visible transcript.
- **Tier surface, unchanged from the matrix:** the only tier-dependent UI
  is the footer — free shows the "Shared via Dropway" attribution and the
  "showing the last 10 messages" note; paid shows neither. The viewer is
  otherwise identical on every plan: polish is not a lever, depth is.

The dashboard's chat library reuses the same rendering component inside the
app (where it can be richer — search, delete-message affordances, attach/
detach controls) but the served viewer stays the minimal, dependency-free
build above.

## Enforcement mechanics (small seam extension + existing patterns)

- **Resources in `internal/quota`:** `ResourceChatMessagePerLog` (discrete,
  the active lever) and `ResourceChatLogPerOrg` (dormant, unlimited on every
  tier). Bands live in `cloud/quota` behind the `cloud` build tag; the FSL
  core never sees them.
- **The seam gains one method** for window semantics: alongside
  `Allow`/`AllowN`, the provider exposes `RetentionWindow(planTier, res)
  (n int64, ok bool)`. Cloud returns `(10, true)` for free; every paid tier
  and the core `Unlimited` provider return `ok=false`. The store's append
  transaction, under a per-log advisory lock, does: window set → INSERT
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
  append* to that log, which prunes to the newest 10 like any free append.
  Same "binds on next action" posture as the site-count downgrade.
- **Storage accounting:** message rows live in Postgres (bounded per log by
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
too cheap to meter); multiple logs attached to one site (one panel, one
story — the library holds the rest); pinning logs to a single deploy version
(per-message version stamps cover grouping); recovering pruned free-tier
rows after an upgrade (pruned means deleted — the importer said so at trim
time); and any self-host restriction — OSS builds use the core `Unlimited`
provider and get everything, uncapped, per the open-core boundary.
