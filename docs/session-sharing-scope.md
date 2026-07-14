<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Share This Session — free vs. paid scoping

How "Share This Session" (PRD 2) is packaged across plan tiers, and what the
free tier's caps are. This is the pricing/entitlement scope for the feature;
the product shape is summarized only far enough to make the levers concrete.

**Feature recap.** After deploying, a user optionally attaches a conversation
export — pasted or uploaded from Claude Code, ChatGPT, Cursor, or plain text,
via dashboard, MCP, or CLI — to that deploy. Viewers of the site see a
collapsible **"How this was made"** panel rendering the prompt history. The
session is locked to the specific immutable `site_version` it shipped with and
inherits the site's access tier (public / password / allowlist / org), so a
gated site's session is exactly as gated as the site itself.

## Packaging principle: free gets the feature, paid gets the depth

Session sharing should **not** be paid-only. Two reasons:

1. **It is the viral surface.** A session panel on a public site is Dropway
   marketing in front of exactly the audience we want (people receiving
   AI-built artifacts). Cutting free users off would cut off the loop. This
   mirrors the existing free-tier attribution banner: the panel on free-tier
   sites carries "Made with AI · Shared via Dropway" branding, and removing
   that branding is itself a paid perk.
2. **The tool-agnostic pitch needs volume.** "Works for Cursor, ChatGPT,
   manual notes" only lands if hobbyists actually attach sessions. The paid
   pitch ("client-friendly handoff with access control and version history")
   is the professional layer on top.

So: free users can attach sessions, capped; paid users get uncapped counts,
bigger transcripts, per-version history, and no attribution. The caps follow
the house pattern — **pay for output, not seats** — and reuse the existing
open-core quota seam unchanged.

## Tier matrix

| Lever | Free | Pro ($25) | Business ($150) | Enterprise |
| --- | --- | --- | --- | --- |
| Attach sessions (dashboard, MCP, CLI) | ✅ | ✅ | ✅ | ✅ |
| Live attached sessions per org | **5** | Unlimited | Unlimited | Unlimited |
| Transcript size per session | **1 MiB** | 10 MiB | 10 MiB | 10 MiB |
| Session ↔ version binding | Latest deploy only | Every version | Every version | Every version |
| "Shared via Dropway" attribution on panel | Always on | Removable | Removable | Removable |
| Access-control inheritance | ✅ | ✅ | ✅ | ✅ |
| Org kill switch (`sessions_enabled`) | ✅ | ✅ | ✅ | ✅ |

Notes on each lever:

- **5 live sessions per org (free).** The countable, race-safe lever, same
  shape as the free skill cap. "Live" means currently attached and viewable;
  detaching a session frees the slot. Five is half the free site cap (10), so
  a free user can showcase their best handful of artifacts but an agency
  doing client handoffs on every site outgrows it immediately — which is the
  intended upgrade moment. Every paid tier is uncapped, matching the skills
  precedent (free capped → all paid unlimited) rather than the graduated site
  bands, because sessions are cheap (text blobs) and counting them per paid
  tier adds pricing surface without revenue.
- **1 MiB vs 10 MiB transcripts.** 1 MiB comfortably holds a long pasted
  conversation; multi-day agent transcripts with embedded tool output need
  more. 10 MiB matches the MCP inline download cap, so a paid-tier transcript
  is always representable across every surface. The size check is validation
  at upload prepare *and* finalize (server-verified bytes), the same
  two-phase enforcement as `internal/skillspec`.
- **Latest-only vs per-version history (the real paid hook).** On free, a
  site holds one session, bound to its current version; attaching on a new
  deploy replaces it. On paid tiers every deploy version keeps its session,
  so rolling back a site rolls back its "How this was made" panel too, and a
  client permalink to an old version shows the session that actually
  produced it. This is the PRD's "session stays locked to the specific
  deploy version" promise in full — free users get a taste, professionals
  paying for client handoffs get the guarantee. It also bounds free storage
  growth structurally instead of with another meter.
- **Attribution.** Reuses the `RouteValue.plan_tier` plumbing the free-tier
  site banner already uses; no new serving-side state.
- **Not levers.** Access-control inheritance is inherent to serving the panel
  under the site's existing authz — gating it per-tier would mean shipping a
  *less* safe free product, so it's universal. The org kill switch is
  governance, and governance toggles (like `mcp_enabled`) are free on every
  tier. Ingest surfaces (dashboard/MCP/CLI) are universal because the upload
  contract is shared; gating a surface would fork the client code, not the
  policy.

## Enforcement mechanics (all existing patterns)

- **Two new resources in `internal/quota`:**
  `ResourceSessionPerOrg` (discrete; free = 5, paid unlimited, self-host
  Unlimited) and `ResourceSessionTranscriptBytes` (continuous, checked with
  `AllowN(0, size)` at finalize; free = 1 MiB, paid = 10 MiB). Bands live in
  `cloud/quota` behind the `cloud` build tag; the FSL core never sees them.
- **Race-safe check in the store:** per-org advisory lock across
  COUNT(live sessions) → `Allow` → INSERT inside the attach transaction —
  byte-for-byte the site/skill cap pattern.
- **Standard 402:** crossing a cap returns the existing
  `ExceededError` body (`{limit, current, max, plan_tier, next_tier: "pro",
  upgrade_url}`), which the dashboard's upgrade modal, the CLI's upgrade
  message, and MCP error mapping already understand. Zero new client
  error-handling.
- **Downgrade never breaks shipped links.** Dropping to free with >5 live
  sessions (or per-version history) leaves everything already attached
  viewable — we never 404 an artifact a client was sent. The cap binds only
  on the *next attach*: new attaches are 402'd until the org is under the
  band, and new deploys on free resume latest-only behavior. Same posture as
  the site-count downgrade.
- **Storage accounting:** transcripts are content-addressed blobs in the
  existing per-org store (`blobs/<org>/<sha256>`), so they flow through the
  dedup-aware storage meter for free; no separate session byte meter.

## Upgrade moments (where the 402 actually fires)

1. **Sixth live session** on a free org → 402 `next_tier: pro`; dashboard
   opens the upgrade modal, CLI/MCP print the upgrade URL.
2. **Transcript over 1 MiB** on free → 402 at prepare (before any bytes
   move), with the same CTA.
3. **Soft prompt, not a 402:** when a free user re-deploys a site that has a
   session attached, the "attach a session" affordance notes the previous
   session will be replaced — "keep session history for every version" links
   to Pro. This is the highest-intent surface and costs nothing to show.

## Deliberate non-goals for v1 packaging

Per-viewer session analytics ("who read the session") as a Business upsell —
plumbing exists via the access audit log but the panel should earn usage
first; metered session counts on paid tiers (text is too cheap to meter);
per-site session *count* caps (the org cap plus latest-only already bounds
free usage, and two overlapping caps confuse the 402 story); and any
self-host restriction — OSS builds use the core `Unlimited` provider and get
everything, uncapped, per the open-core boundary.
