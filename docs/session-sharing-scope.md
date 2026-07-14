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

So: the lever is **log depth** — how many messages a site's log may hold.
The bands deliberately mirror the site-count bands (Free 10 → Pro 100 →
Business/Enterprise unlimited), so the pricing story stays one sentence:
"free is for trying it, Pro is for working, Business is uncapped."

## Tier matrix

| Lever | Free | Pro ($25) | Business ($150) | Enterprise |
| --- | --- | --- | --- | --- |
| Append to the log (dashboard, MCP, CLI) | ✅ | ✅ | ✅ | ✅ |
| **Messages per site log** | **10** | **100** | Unlimited | Unlimited |
| Message size (validation, all tiers) | 64 KiB | 64 KiB | 64 KiB | 64 KiB |
| Per-version grouping in the panel | ✅ | ✅ | ✅ | ✅ |
| "Shared via Dropway" attribution on panel | Always on | Removable | Removable | Removable |
| Access-control inheritance | ✅ | ✅ | ✅ | ✅ |
| Org kill switch (`chat_log_enabled`) | ✅ | ✅ | ✅ | ✅ |

Notes on each lever:

- **Messages per site (the one countable lever).** The cap counts the rows
  currently in the site's log; owners/admins may delete individual messages
  (mistakes, pasted secrets), and deletion frees slots. 10 messages is enough
  for a hand-written "here's roughly how I made this" summary; a real
  multi-hour agent transcript needs Pro's 100; agencies narrating every
  client iteration go Business. Same graduated shape as the site cap, so the
  402s escalate the same way: free → `next_tier: pro`, pro →
  `next_tier: business`.
- **Message size is validation, not a tier lever.** 64 KiB per message on
  every tier keeps any single append bounded (and the whole free/pro log
  under ~640 KiB / 6.4 MiB worst-case) without introducing a second
  purchasable axis. Two overlapping paid levers on one feature muddy the
  upgrade story.
- **Version stamping is universal.** Each message records the site's
  `current_version_id` at append time. The panel groups messages by the
  version they accompanied, and rolling back a site still shows an honest
  history (the log is append-only; it narrates the whole journey rather than
  being pinned to one deploy). This keeps the PRD's "context of how it was
  made" promise on every tier — the paid tiers buy *depth*, not honesty.
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
created_at)` with `UNIQUE (site_id, seq)`. The API is a plain append/list
pair — `POST /v1/sites/{id}/chat` (single message or a batch import from a
pasted export, parsed by an `internal/chatspec` normalizer), `GET
/v1/sites/{id}/chat` (paginated), `DELETE /v1/sites/{id}/chat/{seq}` — plus
an MCP `append_chat` tool (OAuth-forwarding, like `deploy_site`) and
`dropway chat append` in the CLI. Viewers never touch the Go API: the
serving Worker exposes the log at a reserved path on the site's own host
(`/__dropway/chat`), resolved through the same KV-rebuildable-from-Postgres
route projection and the same authz as every other asset — which is what
makes "no Claude account needed to view" true.

## Enforcement mechanics (all existing patterns)

- **One new resource in `internal/quota`:** `ResourceChatMessagePerSite`
  (discrete). Bands live in `cloud/quota` behind the `cloud` build tag —
  free 10, pro 100, business/enterprise unlimited — and the FSL core never
  sees them.
- **Race-safe check in the store:** per-site advisory lock across
  COUNT(messages) → `AllowN(current, n)` → INSERT inside the append
  transaction — byte-for-byte the site/skill cap pattern. Batch imports pass
  `n` = messages in the batch, so a paste either fits entirely or 402s
  before any row lands (no partially-imported conversations).
- **Standard 402:** crossing a cap returns the existing `ExceededError`
  body (`{limit, current, max, plan_tier, next_tier, upgrade_url}`), which
  the dashboard's upgrade modal, the CLI's upgrade message, and MCP error
  mapping already understand. Zero new client error-handling.
- **Downgrade never breaks shipped links.** Dropping to a tier whose cap the
  log already exceeds leaves every existing message viewable — we never
  truncate an artifact a client was sent. The cap binds only on the *next
  append*: new messages are 402'd until deletions bring the log under the
  band. Same posture as the site-count downgrade.
- **Storage accounting:** message rows live in Postgres (they are small and
  bounded by cap × 64 KiB); no blob-store or storage-meter involvement.

## The import-trim problem (free tier, flagged)

A real exported conversation is almost always longer than 10 messages, so a
free user's *first* paste will 402. The importer must handle this as UX, not
as a dead end: on a free org, the dashboard import flow shows the parsed
message list and lets the user pick/trim to 10 (defaulting to the user
prompts, which carry the "what was asked" story), with the 402's upgrade CTA
alongside — "keep the full history" links to Pro. CLI/MCP importers surface
the same choice via a `--last N` / truncation flag rather than failing dry.
This is the feature's highest-intent upgrade surface; it must feel like a
choice, not a wall.

## Upgrade moments (where the 402 actually fires)

1. **11th message** on a free site's log → 402 `next_tier: pro` (via the
   trim UX above when it's an import).
2. **101st message** on a Pro site's log → 402 `next_tier: business`.
3. **Soft prompt:** the free-tier panel footer's "Shared via Dropway"
   attribution links to the product; removing it is part of the Pro pitch.

## Deliberate non-goals for v1 packaging

Per-viewer log analytics ("who read the history") as a Business upsell —
plumbing exists via the access audit log but the panel should earn usage
first; per-org message pools or storage metering for chat rows (bounded by
cap × 64 KiB, too cheap to meter); pinning logs to a single deploy version
(the append log narrates all versions; per-message version stamps cover
grouping); and any self-host restriction — OSS builds use the core
`Unlimited` provider and get everything, uncapped, per the open-core
boundary.
