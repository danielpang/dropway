<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Dropway GTM Plan — Drive Sign‑ups → Drive Revenue

**Goal:** more sign‑ups (leading), revenue growth (lagging outcome).
**Owner:** GTM Engineering.
**Framework:** built on the Beacon *"How to get users"* playbook (Asher King‑Abramson):
North‑Star Metric → ICP = demographics **+ a trigger/pain** → growth loops →
tactics & paid channels. Where this doc says *(Beacon)*, it's applying a principle
straight from that guide.

> **What's in here (maps to the request)**
> 1. GTM plan + personas → [§2](#2-north-star-metric), [§3](#3-personas-icp--demographics--a-trigger), [§4](#4-positioning--message-map-beacon-value-prop-table)
> 2. Growth loops / network effects → [§5](#5-growth-loops--network-effects)
> 3. Ad networks (Google, LinkedIn, …) → [§6](#6-paid-channels-which-ad-networks-and-in-what-order)
> 4. Copy competitor SEO via sitemap → Claude → [§7](#7-seoaeogeo-copy-the-competition-via-their-sitemap)
> 5. Targeted keywords, **no broad match** → [§8](#8-google-ads-keyword-plan-high-intent-only-no-broad-match)

---

## 1. Product in one line

**A folder of files → a live, access‑controlled URL in one command.** Open‑source +
self‑hostable, with a hosted SaaS at [dropway.dev](https://dropway.dev).

The wedge insight *(straight from the README, and it's a good one):* **building
something is easy now; sharing it with the *right* people, safely, is still hard.**
Dropway is the missing "share layer": publish in seconds, with sharing and access
control as first‑class features — public, password, a specific email allowlist, or
"anyone in your org" — plus versioned/immutable deploys, instant revocation at the
edge, audit logs, and an OAuth‑protected **MCP server** so AI agents publish and read
sites under the same access rules as people.

**Why this matters for GTM:** the differentiator is *not* "host a static site" (Vercel,
Netlify, Tiiny, S3 all do that). It's **governed sharing + an agent‑native publish
layer.** Every message, keyword, and loop below leans on that, because broad
"static hosting" positioning loses to incumbents and burns ad budget.

---

## 2. North‑Star Metric

*(Beacon: the NSM is **not** sign‑ups — "what if people sign up but never share?" — and
it's **not quite** revenue. Revenue happens *because of* the NSM.)*

> **Primary NSM: Weekly Shared‑and‑Viewed Sites** — the number of sites that were
> published *and* opened by at least one recipient in the week.

This captures the actual moment of value: a published site that someone *received and
viewed*. A user who deploys but never shares hasn't gotten value; a share that's never
opened didn't land. Drive this number and sign‑ups + revenue follow.

**Input metrics (the levers that move the NSM):**

| Stage | Metric | Why it matters |
|---|---|---|
| Acquisition | New sign‑ups / week | Top of funnel (the request's target) |
| Activation | % of new users who publish a site in 24h | "Aha" #1 |
| **Core value** | % who **share** a published site within 7 days | "Aha" #2 — the loop trigger |
| Loop | Recipients per shared site who later sign up | Network‑effect coefficient (K) |
| Revenue | Orgs hitting a paid trigger (seats / custom domain / SSO / audit) | NSM → $ |

Instrument every step from day one (PostHog/Amplitude‑style funnels, as in the Beacon
"NSM: Tracking" slide). You can't make the number go up if you can't see it.

---

## 3. Personas (ICP ≠ demographics → demographics **+ a trigger**)

*(Beacon: "Eng managers at 50+‑person cos" is weak; "…**with high employee churn**" is
an ICP. Good growth comes from **pain with bad alternatives.")* For each persona below
the **trigger** and the **bad alternative they're escaping** is what makes them sign up.

### P1 — AI app & agent builders *(the wedge — highest‑priority)*
- **Who:** devs building on Claude / Cursor / Codex / v0, agent frameworks, "vibe‑coding"
  and internal‑tool generators that *produce* websites/reports.
- **Trigger:** "My agent just generated a site/report and I need to hand the user a
  **real, access‑controlled URL** — programmatically, not by hand."
- **Bad alternative:** spin up a Vercel/Netlify project per output (needs GitHub, no
  per‑recipient access control), or paste raw HTML the user can't use.
- **Why they sign up:** Dropway's **MCP server** lets the agent create → deploy →
  set sharing in‑flow (OAuth 2.1, no API keys), and the end user gets a live URL with
  the right access tier. This is also the strongest **network‑effect** persona (see §5).

### P2 — Data / analytics / ML engineers
- **Who:** people generating HTML reports, notebook exports, benchmark dashboards, one‑off tools.
- **Trigger:** "I just generated an analysis and need to share it with *this client / my
  team* — not the whole internet — and be able to update or revoke it."
- **Bad alternative:** email a zip or screenshot (stale instantly, no live URL), paste
  into Slack/wiki (loses CSS/JS/interactivity), or stand up S3 + CloudFront (overkill).
- **Why they sign up:** drag‑to‑deploy, live versioned URL, password or allowlist in one click.

### P3 — Designers, PMs & agencies (client review)
- **Who:** product designers, PMs, dev shops shipping prototypes and review builds.
- **Trigger:** a client/stakeholder review cycle — "show *exactly these people* the build,
  password‑protected, and roll back if a version is bad."
- **Bad alternative:** Netlify Drop (free deploys **expire in 24h**), zip files, screenshots.
- **Why they sign up:** per‑site sharing tiers, instant rollback, custom domains for the agency brand.

### P4 — Finance / accounting / consulting professionals
- **Who:** analysts and advisors handing confidential analyses to clients.
- **Trigger:** a deliverable handoff that **must** stay confidential and auditable.
- **Bad alternative:** email a PDF (no audit, no revoke), or a generic file share with no expiry.
- **Why they sign up:** allowlist/password access + **audit log of who saw what** + instant revocation.

### P5 — Companies needing *governed* sharing *(the buyer / expansion & monetization persona)*
- **Who:** an admin/security owner at a company where many people already publish ad‑hoc.
- **Trigger:** a security/compliance review — or a near‑miss leak — forces "internal by
  default, external only if an admin allows it."
- **Bad alternative:** internal file shares (no external link, no audit), or ungoverned Vercel/Netlify sprawl.
- **Why they sign up / pay:** org‑wide policy to forbid external sharing, roles
  (owner/admin/member), audit export, SSO/SAML, custom domains, immediate revocation at the edge.

**Sequencing:** **P1 + P2** are the acquisition wedge (fast, self‑serve, viral). **P5** is
where bottoms‑up usage converts to revenue (seats, SSO, audit, custom domains). P3/P4 are
high‑intent SEO/ads niches that punch above their weight.

---

## 4. Positioning & message map *(Beacon value‑prop table)*

Bad Alternative → Problem → Implication → How Dropway beats it → Benefit:

| Bad alternative | Problem | Implication | Dropway value prop | Benefit |
|---|---|---|---|---|
| Email a zip / screenshot | No live URL, instantly stale | Recipients see old work, endless re‑sends | Drag‑to‑deploy → live, versioned URL | One link, always current |
| S3 + CloudFront | Too much setup for a share | Eng time wasted; non‑tech can't do it | Folder → URL in one command | Ship in seconds, no pipeline |
| Vercel / Netlify project | Needs GitHub; can't share with *just my team* | No per‑recipient access control | 4 sharing tiers per site (public / password / allowlist / org) | Right people, default‑deny |
| Netlify Drop | Free deploys expire in 24h | Demo dies; not for real sharing | Persistent, versioned, revocable sites | Durable links + rollback |
| Paste into wiki | Loses layout, CSS/JS, interactivity | Broken deliverable | Serves the real static output | Looks/works exactly as built |
| Internal file share | No external link, no audit, no expiry | Can't prove who saw what | Audit log + instant edge revocation | Governance + compliance |
| Hand an LLM agent raw HTML | User can't *use* generated output | Dead end in agent apps | MCP: deploy + gate a URL programmatically | Agents ship real, governed URLs |

**Tagline options to A/B test:**
- "Share what you built — with exactly the right people."
- "A folder → a live, access‑controlled URL. In one command."
- "The publish‑and‑share layer for the things you (and your agents) build."

---

## 5. Growth loops & network effects

*(Beacon "Advanced": a growth loop is **Input → Activation → Output**, where the output
*becomes* the next input. Best loops, like Linktree — "see it on a profile → sign up →
put it on your own profile" — and Reddit — "user content ranks → pulls in the next user.")*

### Loop A — Shared‑link / "Made with Dropway" loop *(core viral loop)*
```
Input:      recipient receives a Dropway URL (or sees a "Published with Dropway" badge)
   ↓
Activation: they open it, see the content + realize publishing/gating is this easy
   ↓
Output:     they sign up, publish their own site, share with their recipients  ──┐
   └──────────────────── those recipients are the next Input ───────────────────┘
```
**Accelerant:** a subtle **"Published with Dropway" footer badge** on public/free‑tier
sites (Beacon: the classic "powered‑by" badge — Calendly, Typeform, Linktree). Default‑on
for free; **removable on paid** → doubles as a monetization lever.

### Loop B — Agent / MCP distribution loop *(the differentiated, defensible one)*
```
Input:      an AI app/agent (Claude, Cursor, Codex) uses the Dropway MCP connector
   ↓
Activation: it deploys a site for an end user → end user gets a real, gated URL
   ↓
Output:     end users sign up to claim/manage/re‑share; builders adopt Dropway as
            the default "share layer" → every integration mints more deployed sites
            and more recipients (B2B2C network effect)
```
Each AI product that wires Dropway in as its publish‑and‑share layer pumps a stream of
recipients into Loop A. Plus: public Dropway sites auto‑serve **`llms.txt`** and welcome
ClaudeBot/GPTBot/PerplexityBot → content gets **cited by LLMs** (AEO/GEO, §7). Structural
moat: Dropway is the hosting layer that's *legible to agents under access control.*

### Loop C — Org / seat‑expansion loop *(bottoms‑up SaaS → revenue)*
```
Input:      someone shares an "org‑only" or allowlist site with a colleague
   ↓
Activation: colleague must sign in to view → becomes a user in the org
   ↓
Output:     more colleagues publish + share internally → invite more colleagues →
            admin upgrades for governance (SSO / audit / custom domain)
```
The access model itself is the invite mechanism: gated content **forces** recipients to
authenticate (Slack/Loom‑style seat expansion). This is the primary path NSM → $.

### Loop D — Programmatic‑SEO‑from‑your‑data loop *(compounding, §7)*
Public sites + an opt‑in showcase/gallery + templates become indexable pages that rank,
carry the badge, and pull in the next user — Reddit‑style content compounding.

**Priority:** A and B build the network effect; C converts it to revenue; D compounds reach.

---

## 6. Paid channels: which ad networks, and in what order

*(Beacon stance: start with **high‑intent** demand capture; **try FB/IG before LinkedIn**;
LinkedIn is powerful but expensive — reserve it for ABM.)*

| Order | Channel | Use it for | Notes (Beacon + 2025–26 benchmarks) |
|---|---|---|---|
| 1 | **Google Search Ads** | Bottom‑funnel intent + competitor conquest | Highest‑ROI for "I have this problem now." **High‑intent KWs only, no broad match, negative‑keyword list, enhanced conversion tracking, track `utm_term`.** See §8. |
| 2 | **Facebook / Instagram Ads** | Cheap creative testing + lookalikes of converted sign‑ups | Yes, B2B works. Use **screen recordings** (folder → live URL in 10s), not stock illustrations. Carousel/video. |
| 3 | **LinkedIn Ads** | ABM for **P5** (governed sharing / security buyers) only | Expensive (~$8–15 CPC). Use promoted posts from a *person* (founder) + conversation ads + Lead Gen Forms. *Try FB/IG first.* |
| — | **Niche newsletters** | Underrated reach into P1/P2 | Sponsor AI‑eng / data / dev sends (TLDR, Bytes, Pointer, Data Eng Weekly). Cheap, trusted, on‑ICP. |
| — | **Organic Reddit / HN / dev communities** | Awareness for P1/P2 | Reddit *ads* are weak (Beacon); organic Reddit + Show HN + Hacker News launch are strong for a dev/OSS tool. |
| — | **G2 / Capterra** | Capture comparison shoppers | Test for the niche; limited scale. |
| — | **ChatGPT / AI‑answer ads** | Experiment | Cheap clicks, weaker conversion; new‑channel arbitrage. |

**Conversion‑tracking discipline (Beacon):** enable Google **Enhanced Conversions**, fire
a sign‑up conversion, and tag every link with full UTMs **including `utm_term`** so you can
attribute revenue to the exact keyword (e.g., kill/scale "netlify password protect" on real
pipeline, not clicks).

**Budget logic:** Google (intent) first because it has the lowest CAC for someone actively
escaping a bad alternative. Use FB/IG to cheaply test which *message* (P1 vs P2 vs P3)
converts, then pour LinkedIn ABM budget only at the P5 accounts that justify the CPC.

---

## 7. SEO/AEO/GEO: copy the competition via their sitemap → Claude

*(Beacon hack: **run competitor sitemaps through Claude** to reverse‑engineer their ranking
page architecture. AEO/GEO ≈ **80% the same as SEO + 20% being in LLM training/citations.**
For agencies: keyword research is fine to outsource, **content is not.**)*

### 7a. The method (repeatable — run it yourself to refresh)
```bash
# 1) Pull a competitor's sitemap (Vercel, Netlify, Tiiny, Render, Cloudflare Pages, Surge…)
curl -s https://vercel.com/sitemap.xml -o vercel-sitemap.xml
# (some are split: sitemap-0.xml, /docs/sitemap.xml, etc.)
```
Then paste into Claude with this prompt:
> "Here is a competitor's sitemap.xml. Group the URLs into content templates/page types
> (docs, guides, templates, comparisons, integrations, solutions, blog, customers).
> Tell me which are **programmatic** (one template × many entities), estimate the count per
> type, and infer the head/long‑tail keywords each type targets. Then propose the equivalent
> page architecture for **Dropway**, whose differentiator is *access‑controlled* sharing and
> an *agent/MCP* publish layer — only suggest pages where we have a unique, defensible angle."

> ⚠️ **Note for this run:** the competitor sitemaps (Vercel/Netlify/Tiiny) couldn't be fetched
> from this sandbox — outbound network policy blocked the domains *and* those sites bot‑block
> their sitemap. The taxonomy below is reconstructed from public knowledge + search; re‑run the
> two commands above from an unrestricted machine to refresh exact URLs/counts.

### 7b. What the Vercel/Netlify‑class playbook looks like (the template types that rank)
- **`/docs/*`** — large; product + framework docs (foundational, brand + long‑tail).
- **`/guides/*`** — **programmatic**: "how to {deploy / do X} with {framework}" — task × framework matrix, hundreds of pages.
- **`/templates/*`** — **programmatic**: starter‑template gallery, huge long‑tail surface.
- **`/blog/*`** — thought leadership + product/SEO playbook posts.
- **`/customers/*`** — case studies (trust + bottom‑funnel).
- **Comparison / `vs` pages** — "X vs Y", capability landing pages (high commercial intent).
- **`/integrations/*`**, **solutions/use‑case** pages.

### 7c. Dropway's SEO/AEO architecture (mapped to our wedge — build, don't copy verbatim)
1. **`/guides/*` programmatic matrix** *(Loop D engine)* — `{action} × {artifact}`:
   - actions: *share · password‑protect · host · deploy · make internal‑only · revoke access to*
   - artifacts: *a React build · a Vite app · an HTML report · a Jupyter notebook · a data dashboard · a static site · an AI‑generated site*
   - = dozens of pages like **"How to password‑protect a static site"**, **"Share a Jupyter
     notebook as a private link"**, **"Host an HTML report online for one client."**
     *(Beacon caveat: each page must carry **unique, verifiable steps** — real content, not
     spun templates, or Google/LLMs filter it as spam.)*
2. **`/vs/*` comparison pages** (commercial intent): *Dropway vs Netlify · vs Vercel · vs
   Tiiny.host · vs Netlify Drop · vs S3 + CloudFront* — each leading with the gap incumbents
   leave: **per‑recipient access control + audit + revocation.**
3. **`/alternatives/*`**: "Netlify alternative for password‑protected sites," "Tiiny.host
   alternative without the 100 MB cap."
4. **Capability landing pages** (these *are* the high‑intent ad landing pages too, §8):
   *password‑protected static hosting · internal‑only website hosting · share a site with
   specific people · access‑controlled hosting for AI‑generated sites.*
5. **AEO/GEO (the 20%)** — Dropway has a **structural advantage**: public sites already serve
   `llms.txt` and welcome ClaudeBot/GPTBot/PerplexityBot, so Dropway‑hosted content is
   AI‑discoverable *by design.* Lean in: add `SoftwareApplication` + `FAQPage` schema and
   `ItemList` on `/vs` pages, and pursue citations/listicles ("best way to share a static site
   privately," "tools that password‑protect a static site") so LLM answers name Dropway.

---

## 8. Google Ads keyword plan: high‑intent only, **no broad match**

*(Beacon: **only high‑intent KWs · avoid broad match · use negative keywords · enhanced
conversion tracking · track `utm_term`.**)* Match types: **Exact `[ ]`** for proven
converters, **Phrase `" "`** for controlled discovery. **No broad match** — with a small
budget it wastes spend on irrelevant intent. Mine the search‑terms report weekly and push
new negatives.

### Tier 1 — Problem/solution intent (start here, tight match)
```
[password protect static site]
"password protect a website"
[host static site with login]
[internal only website hosting]
"share a website with specific people"
[access controlled static hosting]
"share html report with a client"
[share a dashboard with a password]
"client review site password protected"
[deploy a folder to a url]
"share a static site privately"
```

### Tier 2 — Competitor conquest (phrase/exact; pair with `/vs` landing pages)
```
"netlify password protection"
[netlify drop alternative]
"vercel password protect deployment"
"vercel preview password"
[tiiny host alternative]
"surge.sh alternative"
[static hosting with access control]
```

### Tier 3 — Audience / use‑case (P1–P4)
```
"share an AI generated website"
[share a react build without github]
"host a jupyter notebook as a website"
[publish a folder of html files]
"share a vite build link"
[share a data dashboard link securely]
```

### Tier 4 — Branded (cheap, defensive)
```
[dropway]
"dropway dev"
[dropway hosting]
```

### Negative keywords (critical — "dropway" collides with logistics/freight!)
```
# brand‑collision / wrong vertical
-shipping -freight -logistics -trucking -delivery -courier -"drop way" -warehouse
# DIY / no‑intent / wrong product
-free -wordpress -wix -squarespace -shopify -godaddy -namecheap -"google sites"
-tutorial -course -teven -jobs -salary -hiring -"how to make a website for free"
# unrelated "dashboard/report" senses
-car -dashboard -warning -light   # vehicle dashboards
```

**Campaign structure:** one ad group per keyword theme (don't mix Tiers), each pointing at a
**matching capability/`vs` landing page** (Beacon: "tie copy to targeting"). Single‑keyword‑ad‑groups
(SKAGs) for the top 5 converters. Enable Enhanced Conversions; tag URLs with
`utm_source=google&utm_campaign=...&utm_term={keyword}` so revenue attributes to the term.

---

## 9. Non‑paid tactics *(Beacon Part 3)*

- **Learn from users first** *(Beacon: do this before spending a dollar)*: interview 10–15
  recent sign‑ups — *What did you do before? Where did it break? What did you do next?* — to
  confirm the trigger/bad‑alternative per persona and sharpen §4/§8 copy.
- **Warm > cold outreach**: start with *their* problem, personalize, tie copy to targeting,
  no blasts. Trigger‑based: scrape/notice public sites awkwardly hosted on Netlify Drop or
  raw S3, or teams posting "how do I password‑protect a static site" on Reddit/Stack Overflow.
- **ABM for P5**: research target accounts, tailor the security/governance angle.
- **Greyhairs / advisors** for enterprise (P5) credibility and intros.
- **Launch surface**: Show HN / Hacker News, Product Hunt, and dev communities — natural for
  an open‑source, MCP‑native tool. Open‑source itself is a top‑of‑funnel + trust engine.

---

## 10. 90‑day execution sequence

| Weeks | Focus | Output |
|---|---|---|
| 1–2 | Instrument NSM + funnel; 10 user interviews | Dashboards live; trigger/copy validated |
| 1–3 | Ship "Published with Dropway" badge (Loop A); badge removable on paid | Viral loop + monetization lever on |
| 2–4 | Google Ads Tier 1–2 + `/vs` & capability landing pages | First high‑intent sign‑ups, clean attribution |
| 3–6 | Programmatic `/guides` matrix v1 (10–15 real pages) + schema/llms.txt (Loop D) | Organic + AEO surface compounding |
| 4–8 | FB/IG screen‑recording creative + lookalikes of converters | Cheap message testing, scaled reach |
| 6–10 | MCP loop: docs + 2–3 AI‑app integration partners (Loop B) | Agent‑driven recipients into funnel |
| 8–12 | LinkedIn ABM for P5; SSO/audit/custom‑domain upsell motion | NSM → revenue (seats + enterprise) |

**Success criteria:** NSM (weekly shared‑and‑viewed sites) up week‑over‑week; sign‑ups up; a
measurable loop coefficient (recipients/site who sign up) > 0; first paid conversions on the
governance triggers.
