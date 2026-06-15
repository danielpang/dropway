# Shipped — Architecture Plan (Quick-style multi-tenant share platform)

> *A folder of files → a live, access-controlled URL in one command. Static now, dynamic-ready. Built on Cloudflare (R2 + Workers), a Next.js control plane, a Go deploy/runtime backend, and Supabase Postgres.*

This plan was produced after reading Shopify's "Quick" writeup (https://shopify.engineering/quick) to copy its proven feature set, then extending it for true multi-tenancy + 3-tier sharing.

> 📊 **Diagrams:** see [`docs/diagrams/`](diagrams/) for the component topology and the request-flow sequence diagrams (sign-up, sign-in, deploy, gated access).

---

## 1. Context — the why

Shopify's internal **Quick** proved a sharp thesis: *sharing things is harder than building them.* Quick collapsed "a folder of files" into "a live secure URL" with one command — no pipeline, no config. But Quick is **single-tenant, internal, single-access-model**: every site is open to every Shopify employee, with no per-site permissions or ownership.

**Shipped takes that ergonomic promise external and multi-tenant.** The hard problems Quick never had to solve are now first-class:

- **Three sharing tiers per site** — (a) explicit email allowlist, (b) anyone in the org, (c) public (listed / unlisted-signed / password-protected).
- **Orgs/enterprise as first-class tenants** — sites shared by default within an org; membership via verified-domain auto-join *and* invites/links.
- **Serving untrusted, user-controlled HTML/JS** to the public internet without one tenant's site stealing another tenant's (or the platform's) session.

**Decided constraints (confirmed with the user):** static content now (HTML/CSS, Markdown, client-side React/JS bundles), architected so dynamic (SSR + per-site backends) drops in *without a rewrite*; hybrid infra (own storage+CDN+auth, optional managed runtime later); Cloudflare R2 + Workers at the edge; Next.js/TS control plane; Go backend; Supabase Postgres via an ORM; both verified-domain auto-join AND invites.

**The single load-bearing decision that makes this safe:** serve all tenant content from a **separate registrable domain on the Public Suffix List**, never a subdomain of the app/auth domain. Everything else layers on top.

**Business model — open-core (Supabase/PostHog-style):** Shipped is **open source + hosted SaaS**. The full app is source-available under the **Functional Source License (FSL-1.1-Apache-2.0)** — anyone may self-host and use it internally for free, but may **not** offer it as a competing paid service; it converts to Apache 2.0 after 2 years. The **quota + billing engine (the 10-sites-per-user / 5-members-per-org free limits + Stripe) is a cloud-only module** that does not ship in the self-host build — self-host is unlimited, the *license* is what prevents resale. Enterprise-only features live in a separately-licensed `ee/` directory. Full model + licensing in **§14**.

---

## 2. Requirements

### 2.1 Shopify Quick feature disposition (keep / adapt / drop / defer)

| Quick feature | Disposition | Rationale |
|---|---|---|
| One command: folder → live secure URL | **KEEP** | The core promise / north star. |
| `quick deploy` CLI wrapping rsync | **ADAPT** | Keep one command; rsync → R2 presigned multipart + content-addressed manifest, per-user auth, org-scoped. |
| Overwrite-on-redeploy (mutate in place) | **ADAPT** | Replace with immutable, atomic, content-addressed deploys + pointer flip → versioning/rollback. |
| NGINX wildcard + gcsfuse GCS mount | **DROP** | Cloudflare-native: Worker with R2 binding; no fuse, no NGINX, free egress. |
| Subdomain routing `mysite.quick…` | **ADAPT** | Keep subdomain-per-site, but on a separate PSL-listed content domain. |
| One bucket/folder per site | **ADAPT** | Single bucket, per-**org**/site/deploy prefixes (`blobs/<org_id>/<sha256>`). |
| Google IAP (all-employees) | **ADAPT** | Replace with a thin edge Worker + **Go-API authz** (and a gated-site `/authz` exchange over Better Auth) — external users + 3 tiers, not one Google org. |
| Single access model, no per-site perms | **DROP** | The product *is* per-site ownership + 3 tiers. Direct divergence. |
| CloudSQL + Go API server | **ADAPT** | CloudSQL → Supabase Postgres; keep Go for deploy orchestration + runtime. |
| 50k+ sites on one $200/mo VM | **KEEP (benchmark)** | Serverless edge should beat single-VM cost/scale. |
| Runtime: collection DB + realtime / file upload / LLM proxy / WebSockets | **DEFER** | Dynamic-phase; build on Supabase Realtime / Durable Objects + Go proxy. |
| Runtime: BigQuery integration | **DROP** | Shopify-internal coupling; revisit as generic connectors. |
| Identity API (name/title/team) | **ADAPT + DEFER** | Re-scope to multi-tenant (email/org/role), authorized viewers only. |
| AI-agent "skills" / shared JS libraries | **DEFER / DROP** | After runtime GA / internal play out of scope. |
| Rate limiting | **KEEP** | Essential externally — denial-of-wallet on public/runtime. |
| *(absent in Quick)* Versioning + rollback | **ADD — MVP** | Free byproduct of immutable deploys; table stakes. |
| *(absent)* Password protection / unlisted | **ADD — MVP** | Part of the public tier. |
| *(absent)* Custom domains | **ADD — Enterprise** | Cloudflare for SaaS custom hostnames. |
| *(absent)* Analytics / Expiration (TTL) | **ADD — v1** | Paid feature / valuable for "quick share." |

### 2.2 Functional requirements (priority-tagged)

- **Orgs & membership** — **Better Auth Organization plugin**: create org (creator = `owner`); solo users get a default single-member org. Roles `owner`/`admin`/`member` **[MVP]**; **only an existing admin/owner promotes a member to admin** **[MVP]**. Sites default to **Tier (b) org-visible**. Invite + invite-link **[MVP]**; verified-domain auto-join **[v1]**; SSO/SAML keyed on user UUID, not email **[Enterprise]**.
- **Auth & identity** — **Better Auth** (self-hosted in the Next.js app) **[MVP]**: **Google sign-in/up is a core, first-class method** alongside email/password + magic link; **email verification required**. Cookie sessions for the dashboard; the **JWT plugin** issues short-lived (5–15 min) **EdDSA** JWTs + a **JWKS endpoint**. **The Go API is the JWT verifier and the authz boundary** (pins EdDSA, rejects `alg:none`/`HS256`, JWKS-by-`kid` with rate-limited refresh). The **public serve path carries no JWT** (cacheable); identity-gated tiers use a host-scoped edge token exchange **[Phase 2]**. No anonymous access to identity-gated tiers (see Sharing).
- **Org sharing policy & roles** **[MVP]** — Org-level **`allow_external_sharing`** flag (default **false**): when false, **no site may be shared outside the org** — the public tier and any external-email allowlist grant are rejected at both the control plane and the edge. When true, public + external allowlist are permitted. **Only org admins/owners** may toggle this policy and assign/revoke the `admin` role. Flipping it back to false **reconciles existing sites** (revokes external grants + public visibility, writes edge deny-list).
- **Deploy/upload** — `shipped deploy` CLI: folder → live URL, no config required **[MVP]**; **folder drag-and-drop in the dashboard [MVP]** (see §7.2 — explicit user requirement, shares the CLI's backend contract); immutable atomic content-addressed deploys, only-changed-blob upload, pointer-flip cutover **[MVP]**; per-deploy preview URL + stable site URL **[MVP]**; versioning + rollback **[MVP]**; presigned direct-to-R2 upload **[MVP]**; Git push-to-deploy + optional managed build **[v1]**.
- **Sharing (3 tiers)** — Per-site visibility settable by the **site owner (member) or an org admin**, but **only within the org sharing policy above**; allowlist supports pre-registration emails; **default-deny [MVP]**; **allowlist requires the invitee to create a Shipped account first**, and a grant is matched only on a **verified** email/sub **[MVP]**; edge enforcement per request; password mode; unlisted/signed **[MVP]**; expiration TTL **[v1]**; share-event audit **[v1]**.
- **Serving** — Dispatch Worker on `*.shippedusercontent.com`; resolve host → org/site/deploy via KV; auth; stream from R2 **[MVP]**; Cache API for public only, never cache private **[MVP]**; per-site security headers **[MVP]**; custom domains via Cloudflare for SaaS **[Enterprise]**.
- **Runtime (architected now, built later)** — Identity API, rate limiting, collection-DB CRUD **[v1]**; realtime, file uploads, LLM/image proxy **[v1/later]**; WebSockets/DOs, SSR via Workers-for-Platforms **[later]**. Static→dynamic adds a user-Worker behind the same Host; **no rewrite**.
- **Billing & limits (cloud-only)** — Member-count bands with **hard caps**: **Free ≤5 members & ≤10 sites/user → Business 6–99 → Enterprise 100–1,000 → Contact Sales >1,000** **[MVP]**. Exceeding a cap **blocks the action and opens the Stripe subscription modal** (or sales CTA at the top). Stripe, one Customer per org; **the paid `plan_tier` is persisted to the DB by a signature-verified webhook** (not the browser redirect); seats from Better Auth `member` rows; metered egress + runtime **[plans MVP, metering v1]**. **All limits/billing run only in the hosted-cloud build; self-host is unlimited (see §14).**
- **Open source + SaaS (open-core)** **[MVP]** — Repo is public under **FSL-1.1-Apache-2.0** (self-host free, no resale; → Apache 2.0 in 2 yrs); a `cloud/` module (quotas + Stripe + subscription modal) and an `ee/` module (enterprise features) are separately licensed and excluded from the self-host build. One-command self-host (Docker Compose / Helm). Details in §14.
- **Admin** — Dashboard **[MVP]**, access requires a **Better Auth login** (no Cloudflare Access gate). The **internal staff/super-admin** surface is the same login plus a **platform-staff role check** (server-side; staff verified-domain + MFA), not a network gate **[MVP]**; audit log **[MVP]**; abuse/takedown tooling **[MVP — §10]**.
- **Dashboard look & feel** **[MVP]** — Modern, minimal aesthetic in the **Vercel / Cursor** vein (crisp typography, generous whitespace, subtle borders, monochrome + one accent, tasteful motion). **Automatic light/dark from the device** (`prefers-color-scheme`) with an optional manual override; WCAG-AA contrast in both themes; `prefers-reduced-motion` respected. The **sign-up / sign-in screens are first-impression surfaces** and must feel polished (Google button primary, email/magic-link secondary). Design system in §4.

### 2.3 Non-functional requirements

- **Security** — Separate PSL-listed content domain; **Go API origin is the authz boundary**; public serve path JWT-free; gated tiers via a host-scoped `/authz` exchange (app session never reaches content origin); short-TTL tokens + Phase-4 denylist; `__Host-` cookies; RLS everywhere, default-deny, `FORCE RLS`; tenant code isolation later (Workers-for-Platforms untrusted mode). *Detail in §10.*
- **Isolation** — Single R2 bucket, **per-org** prefixes, Worker-gated reads; one immutable deploy = one origin; shared-schema + `org_id` + RLS (no DB-per-tenant).
- **Scale/perf** — Match/beat 50k sites cheaply; architect for ≥1M. Cache-hit < 20ms from PoP; KV routing 0.5–10ms. Respect platform limits (KV 1 write/sec/key → never counters; D1 caps; replica lag).
- **Cost** — Exploit R2 free egress; cache aggressively to cut Class-B ops; per-tenant cost attribution + denial-of-wallet caps.
- **Observability** — Structured logs (deploys, edge auth decisions, `/authz` mints); metrics (requests/site, cache-hit ratio, R2 ops, auth failures, deploy latency); `request_id` correlated edge→`/authz`→Go→Postgres.
- **Compliance** — GDPR/CCPA deletion/export; org-scoped hard-delete of blobs/manifests; SOC2 posture (Supabase Team); HIPAA/VPC (Enterprise).

---

## 3. Recommended Architecture

**Three registrable domains (the security spine):**

| Domain | Host | PSL? | Role | Cookies |
|---|---|---|---|---|
| `app.shipped.app` | **Vercel** | No | Next.js dashboard + **Better Auth** (`/api/auth/*`) + `/authz` exchange (P2) + JWKS | `__Host-` host-only Better Auth session; never `Domain=` |
| `api.shipped.app` | Go service (Cloud Run/Fly/container) | No | Go control-plane + runtime API | Bearer tokens only (Better Auth JWT or deploy token), no cookies |
| **`*.shippedusercontent.com`** | **Cloudflare** (Worker + R2 + KV/D1) | **Yes (submit to PSL before launch)** | All served tenant content + serving Worker | None on public path; short-lived host-scoped token only on gated sites (P2) |

```
                              ┌────────────────────────────────────────────────┐
                              │                   DEVELOPER                      │
                              │   shipped deploy ./dist        browser (dash)    │
                              └───────┬───────────────────────────────┬─────────┘
                                      │ Bearer deploy-token           │ Better Auth session
                                      ▼                               ▼
   ┌───────────────────────┐   ┌──────────────────────────┐   ┌──────────────────────────────┐
   │  STRIPE (cloud-only)  │◄─►│  GO API (api.shipped.app) │◄──│  NEXT.JS DASHBOARD (Vercel)  │
   │  Customer per org,    │   │  = SYSTEM OF RECORD       │ ↑ │  app.shipped.app             │
   │  meters, webhooks     │   │  chi; verifies EdDSA JWT  │ │ │  - BETTER AUTH (/api/auth/*) │
   └───────────┬───────────┘   │  (iss/aud/exp · kid·alg)  │ │ │    Google/email/magic + Org  │
               │ webhooks      │  - deploy orchestration   │ │ │    OWNS its identity tables  │
               ▼  (cloud/)     │  - publish → KV projection│ │ │  - UI calls Go API for ALL   │
   ┌───────────────────────┐   │  - quota provider (iface) │ │ │    business data (OpenAPI)   │
   │  outbox / reconciler  │   └──┬──────────┬─────────────┘ │ └──────────┬───────────────────┘
   └───────────┬───────────┘      │ sqlc     │ goose          │ Bearer    │ Better Auth owns +
               │                  │ (app +   │ migrations     │ EdDSA JWT │ migrates identity
               │                  │ org_meta)│ (app schema)   │ (5–15m)   │ tables only
               ▼                  ▼          ▼                └──────────►▼
   ┌───────────────────────────────────────────────────────────────────────────────────┐
   │              SUPABASE / POSTGRES   (single source of truth · FORCE RLS)             │
   │   auth schema:    Better Auth tables (Better-Auth-migrated; Go reads, never migrates)│
   │   app schema:     org_meta + sites/versions/domains/policy/org_usage (Go·goose·sqlc) │
   │   billing schema: subscriptions… (cloud-only; FK → app.org_meta; app NEVER → billing)│
   │   Go connects as non-BYPASSRLS `shipped_app`; SET LOCAL app.current_org_id per tx     │
   └───────────────────────────────────┬───────────────────────────────────────────────┘
                                        │ Go publishes a REBUILDABLE projection (contracts/ shape)
                                        ▼
   ┌───────────────────────────────────────────────────────────────────────────────────┐
   │                         CLOUDFLARE EDGE (PoP)                                       │
   │  KV  route:<host> → {org_id, site_id, version_id, access_mode, schema_version}     │
   │  D1  allowlist projection (large lists)      Cache API (PUBLIC responses only)      │
   │   ┌────────────────────────────────────────────────────────────────────────────┐ │
   │   │  SERVING WORKER  *.shippedusercontent.com / custom domains  (R2 + KV + D1)   │ │
   │   │   PUBLIC: KV host→version → stream R2   (NO JWT · cacheable · 95% path)      │ │
   │   │   password: Worker checks pw → host-scoped signed cookie (no identity)       │ │
   │   │   allowlist/org-only [Phase 2]: 302 → app.shipped.app/authz → host-scoped    │ │
   │   │     token; Worker verifies THAT token, never the operator JWT                │ │
   │   └───────────────────────────────────┬────────────────────────────────────────┘ │
   └───────────────────────────────────────┼──────────────────────────────────────────┘
                                            ▼
                              ┌──────────────────────────────┐
                              │  R2 (single private bucket)  │
                              │  blobs/<org_id>/<sha256>     │ ← content-addressed, per-org
                              │  manifests/<org>/<site>/<dpl>│ ← immutable deploy manifests
                              └──────────────────────────────┘
```

**The Go API origin is the authz boundary — not the edge.** The dashboard renders UI and runs Better Auth (which owns its own identity tables); for *all business data* it calls the Go API via an OpenAPI-typed client carrying a short-lived EdDSA JWT. The Go API verifies every JWT itself and is the only writer of app data. Postgres is the single source of truth; **KV/D1 and R2 metadata are projections the Go API can fully rebuild**.

**Request lifecycle — public site (the 95% path):** browser → PoP → Serving Worker resolves `route:<host>` from KV → streams the version's bytes from R2 (Cache API). **No JWT, no broker, fully cacheable.**

**Request lifecycle — gated site [Phase 2]:** `password` → Worker prompts, verifies, sets a host-scoped signed cookie (no identity needed). `allowlist`/`org-only` → no valid host-scoped cookie → 302 → `app.shipped.app/authz?return=<host>` → Better Auth confirms identity + checks allowlist/membership → issues a **short-lived, host-scoped** signed token (cookie on the *site* host, or a one-time `?token=` the Worker exchanges) → the Worker verifies **that host-scoped token** (its own secret/JWKS), *never* the operator dashboard JWT. This cross-domain exchange is the genuinely hard piece and is deliberately deferred out of Phase 0/1.

---

## 4. Tech Stack Options & Trade-offs *(the explicit ask)*

### Frontend framework
| Option | Pros | Cons |
|---|---|---|
| **Next.js (App Router)** ✅ | RSC cuts client JS; **Better Auth has first-class Next.js support** (handler + middleware + server `getSession`); co-locates the `/authz` exchange BFF | App Router caching/RSC complexity; soft Vercel pull |
| Remix / RR7 | Web-standard loaders; runs native on Workers | Smaller ecosystem; diverges from constraint |
| Vite SPA | Simplest; trivially static-hostable | No SSR/RSC; hand-roll auth-guard + data plumbing |

**Recommended: Next.js (App Router)** — constraint-aligned, hosts Better Auth (`/api/auth/*`) and the gated-site `/authz` exchange in one BFF.

### Dashboard design system & theming (Vercel/Cursor aesthetic, system light/dark)
| Concern | Choice | Why |
|---|---|---|
| **Styling + components** | **Tailwind CSS + shadcn/ui** (Radix primitives) | The de-facto stack behind the Vercel/Linear/Cursor look; tokens as CSS variables make dual-theme trivial and accessible |
| **Type + base UI** | **Geist** font (Vercel's, OSS) + **Geist-style tokens** | Matches the exact reference aesthetic; crisp at all sizes |
| **Theme switching** | **`next-themes`**, `attribute="class"`, **`defaultTheme="system"`** | Follows the device's `prefers-color-scheme` automatically; optional manual toggle persists per-user; no flash-of-wrong-theme (SSR-safe) |
| **Tokens** | Semantic CSS variables (`--background`, `--foreground`, `--border`, `--accent`) defined for `:root` and `.dark` | One token set, both themes; guarantees WCAG-AA contrast in each |
| **Motion** | Subtle (Tailwind transitions / Framer Motion sparingly), gated by `prefers-reduced-motion` | The "tasteful, not flashy" feel |

**Auth screens (sign-up / sign-in)** are bespoke and polished: centered card on a quiet background (subtle grid/gradient that adapts per theme), **Google as the primary button**, email + magic-link secondary, inline validation, loading/empty/error states designed, full keyboard/focus-ring a11y. Better Auth supplies the logic; the UI is ours. *(During build, the `frontend-design` skill can generate these screens to this spec.)*

### Dashboard hosting
| Option | Pros | Cons |
|---|---|---|
| **Vercel** ✅ | Best Next.js App Router DX, preview deploys, zero-config; the dashboard is low-traffic so egress cost is negligible | Second vendor alongside Cloudflare |
| Cloudflare (OpenNext) | Single vendor w/ R2/KV/Workers | OpenNext full-App-Router less turnkey |

**Recommended (chosen): Vercel for the dashboard** (`app.shipped.app` — Next.js + Better Auth + the `/authz` exchange). **Cloudflare keeps the whole content plane: R2 object store, the serving Worker on `*.shippedusercontent.com`, and KV/D1.** This split is natural here — the dashboard and content already live on different domains, and the heavy, cost-sensitive traffic (served tenant sites, free R2 egress) stays on Cloudflare while only the light management UI runs on Vercel. The **public serve path never touches Vercel or carries a JWT**; only the gated-site `/authz` exchange (Phase 2) does. Self-host swaps Vercel for a container (the dashboard is just a Next.js app).

### Backend API
| Option | Pros | Cons |
|---|---|---|
| **Go net/http + chi** ✅ | Std-lib native; ideal for long-running deploy/runtime concurrency; clean RLS `SET LOCAL` wrapper | Assemble validation/error envelope yourself |
| Echo | Batteries-included | Custom context diverges from `http.Handler` |
| Fiber | Fast | Built on fasthttp — breaks `net/http` ecosystem; long-term liability |
| No Go (Next routes only) | One language, fast MVP | Deploy orchestration, long jobs, multipart uploads poor fit for serverless |

**Recommended: Go `net/http` + chi**, exposing an **OpenAPI** spec from which the dashboard generates a typed TS client (the dashboard's contract is the *API*, never the DB — it never touches Postgres directly). Avoid Fiber. The Go API is the **system of record and the authz boundary**: it verifies every JWT and is the only writer of app data.

### Database (managed Postgres)
| Option | Pros | Cons |
|---|---|---|
| **Supabase (Postgres only)** ✅ | Managed Postgres + PITR + branching; optional Realtime/Storage later; user-requested | If we drop Supabase Auth we lose its turnkey `authenticated` role/JWT plumbing (replaced by app-set GUCs) |
| Neon | Serverless branching, scale-to-zero | Fewer batteries (no Realtime/Storage) |
| RDS/Cloud SQL | Mature, VPC-native | More ops; no branching |

**Recommended: Supabase as managed Postgres** (keep the user's choice); drop Supabase Auth.

### Auth provider
| Option | Pros | Cons |
|---|---|---|
| **Better Auth** ✅ | TS-native, self-hosted, owns tables in *our* Postgres; **Organization plugin = orgs/members/roles/invitations/access-control out of the box** (covers reqs 3–4); **JWT plugin + JWKS** preserves edge verify; Google/email/magic + email-verification + SSO/SAML plugins; no per-MAU vendor bill | Younger than incumbents; we run/upgrade it; org access-control rules are ours to define & test |
| Supabase Auth | Turnkey, integrated RLS | Weaker built-in org/role model; the thing we're replacing |
| Clerk / WorkOS | Polished B2B orgs + SSO | Per-MAU cost; identity lives off-platform; RLS-claim wiring is DIY |
| DIY | Max control | Rebuild months of security-critical auth |

**Recommended: Better Auth** (self-hosted in Next.js) — Organization + JWT + SSO plugins, Google as a core social provider, asymmetric JWT + JWKS for edge/Go verification. Auth tables live in our Supabase Postgres; RLS is driven by app-set `app.*` GUCs (§8), not Supabase's `auth.uid()`.

### Object storage + CDN
| Option | Pros | Cons |
|---|---|---|
| **Cloudflare R2 + Workers** ✅ | Zero egress; private bucket + per-org prefixes; Worker-gated reads; native path to Workers-for-Platforms | No per-object ACL (authz in Worker); gated read = Class-B op unless cached |
| S3 + CloudFront | Mature, IAM/OAC | Egress cost; clunkier edge auth; cross-cloud |
| GCS + Cloud CDN | Solid (Quick's stack) | Egress cost; weaker edge auth; re-treads Quick |

**Recommended: R2 (single private bucket, per-org prefixes) + Worker + Cache API.** Presigned SigV4 URLs for direct uploads only.

### Edge auth mechanism
| Option | Pros | Cons |
|---|---|---|
| **Thin Worker + `/authz` exchange (gated only)** ✅ | Public path is JWT-free + cacheable; gated tiers get a host-scoped token from `app.shipped.app/authz` that the Worker verifies (never the operator JWT) | You own the host-scoped-token hardening; introduce only in Phase 2 |
| Cloudflare Access | Managed SSO at the network edge | Can't gate the Vercel-hosted dashboard; not for end-user per-object authz — **not used here** |
| Signed-URL only | Simple; great for bulk public download | URL secrecy ≠ boundary; can't model tiers/revoke |

**Recommended: keep the public serve path JWT-free** (Worker = thin router: KV host→version → R2, cacheable — the 95% case). **Gated tiers [Phase 2] use a host-scoped token exchange** at `app.shipped.app/authz` (the Worker verifies that token, not the operator JWT — §6). **The real authz boundary is the Go API origin** (it verifies every JWT). **Better Auth login + a platform-staff role** gate the dashboard and internal admin (no Cloudflare Access); signed URLs narrowly for upload/bulk download.

### CLI distribution
**Recommended: Go single static binary** (GoReleaser → Homebrew/Scoop/`curl|sh`), reusing backend deploy code; optional thin `npx shipped` wrapper that downloads the binary.

### Billing
| Option | Pros | Cons |
|---|---|---|
| **Stripe** ✅ | Native meters + seats + tiered pricing; invoicing/ACH; Stripe Tax | You are merchant of record (own tax remittance) |
| Paddle / Lemon Squeezy | MoR (handles VAT) | Weaker metering; less Enterprise-invoice flexibility |

**Recommended: Stripe** (hybrid seat+metered fits best) + Stripe Tax. Keep a thin `BillingProvider` Go interface so an MoR can wrap later.

### ORM / data access — *one writer-owner per table, generated consumers (refined)*
**Recommended: Go = sqlc + pgx/v5 over the `app` schema (migrations via `goose`); Better Auth OWNS + migrates its own identity/org tables (in an `auth` schema); the dashboard consumes a generated OpenAPI TS client, NOT Postgres (no Drizzle, no `supabase-js`).** The only genuine cross-language *data* contract is the Worker's KV value shape, defined once in `contracts/` (JSON Schema → Go struct + TS type + round-trip test). No single migration engine over the union — Better Auth and goose each own their schema, with a documented, CI-gated migration order. Full rationale in §8.

---

## 5. Data Model + RLS

**Schema ownership — one writer-owner per table, namespaced schemas (refined):**
- **`auth` schema — Better Auth owns AND migrates** (user/session/account/verification/jwks/organization/member/invitation). The Go API reads it (membership/role for authz) but **never migrates it**.
- **`app` schema — Go owns** via `goose` migrations: `org_meta` (PK = Better Auth `organization.id`) + all app tables. Business data attaches to the org via `org_meta`, so the two migration tools never fight over one table. App tables FK to `auth.organization.id` (read-only target).
- **`billing` schema — cloud-only, proprietary**, applied only by the cloud deployment. **FK direction is cloud → core only** (`billing.subscriptions.org_id → app.org_meta.id`); the core *never* references `billing`, so the OSS build compiles and runs without it.
- **Migration order is a CI-gated invariant:** Better Auth (`auth`) runs before app migrations that FK to `organization.id`. *(Open question: confirm Better Auth's custom-`auth`-schema support on the pinned version; pin it and add a contract test asserting the column names the Go API reads.)*

```
  user  (Better Auth)        ── account, session (Better Auth-owned)
       │
   ┌───┴──────────────┬──────────────────┬───────────────────┐
   ▼                  ▼                  ▼                   ▼
  AUTH SCHEMA (Better Auth-owned/migrated)        APP SCHEMA (Go-owned, goose)
  ──────────────────────────────────────          ─────────────────────────────
  user ─ account ─ session ─ jwks                  org_meta  (PK = organization.id;
   │                                                 plan_tier cache, allow_external_sharing
   └─ member (org,user, ROLE owner|admin|member)      DEFAULT false, default_visibility)
   └─ invitation (email, role, expiry)              org_usage (org_id PK; members_count,
   └─ organization (slug, created_by=owner) ──┐       sites_count)  ← counter rows for caps
                       ▲ (FK target, read-only)│
        ┌──────────────────────────────────────┴──────────────┐
        ▼                                                      ▼
   sites ──────────────────► site_versions (immutable, content-addressed)
   (org_id, slug, access_mode  (org_id, site_id, version_no, status,
    public|password|allowlist|   r2_prefix, content_hash, size, created_by)
    org_only, current_version_id UNIQUE(site,version_no) · UNIQUE(site,content_hash)
    ◄── deferrable FK)
     │  │  │
     │  │  └──────────────► domains (org_id, hostname GLOBALLY UNIQUE, verify status, TLS state)
     │  └─────────► site_access_policy + allowlist_entries
     │               (org_id, site_id, mode, email/email-domain, is_external,
     │                claimed_at -- grant CLAIMED only when a verified Shipped account signs in)
     ▼
   deploy_tokens / api_keys (org_id, kind, token_hash, scopes, site_id?)
   audit_log (org_id, actor_user, actor_token, action, target, metadata, ip)

  BILLING SCHEMA (cloud-only, proprietary; FK → app.org_meta, core never refs billing)
  ─────────────────────────────────────────────────────────────────────────────────
   subscriptions (org_id, stripe_customer_id, stripe_subscription_id,
     plan_tier (free|business|enterprise), seats, status,
     cancel_at_period_end, current_period_end, org_status)
   processed_stripe_events (event_id PK)  ← webhook dedupe

  EDGE PROJECTIONS (rebuildable from Postgres; written only by Go on publish)
   KV  route:<host> → {org_id, site_id, version_id, access_mode, schema_version}
   D1  allowlist projection (site_id, email)   ← large allowlists
```

`sites.current_version_id → site_versions.id` is `DEFERRABLE INITIALLY DEFERRED` (breaks the insert-time cycle). Every tenant-scoped table carries a **denormalized `org_id`** with composite indexes leading on `org_id`.

**RLS as a tenant-isolation backstop via `SET LOCAL` (refined).** There is no Supabase `authenticated` role or `auth.uid()`, and the Go API uses a **pooled** connection — so naïve Supabase RLS would be a no-op. The fix:
- A dedicated **`shipped_app` login role that is NOT `BYPASSRLS`** and is not `postgres`/`service_role`. Every tenant table is `ENABLE` **and `FORCE ROW LEVEL SECURITY`** so even the table owner is subject; migrations run as a separate owner/admin role.
- On every request the Go API opens a tx and runs `SET LOCAL app.current_user_id = $1; SET LOCAL app.current_org_id = $2;` from the **verified** JWT. `SET LOCAL` is transaction-scoped → **safe under Supavisor transaction-mode pooling** (validate in the Phase-0 spike that GUCs don't leak across pooled txns).
- **Policies are simple and subquery-free:** `USING (org_id = current_setting('app.current_org_id', true)::uuid)` — no joins/helper-function lookups on the hot path. RLS is the isolation *backstop*; the **Go API is the primary authz layer**.
- **Confused-deputy guard:** never authorize from the `org_id`/`role` *claim* alone — for sensitive writes the API re-checks that the target resource belongs to the active org and that membership/role is current. Claims are a fast hint, not a grant.
- **Authorization split (reqs 3–4), enforced in the Go API + a DB CHECK/trigger:** toggling `org_meta.allow_external_sharing` and writing `member.role` requires admin/owner; per-site sharing is writable by the site owner OR an org admin, but a CHECK/trigger **rejects `public`/external grants when the org policy is false**.
- **Quota race safety (hard caps):** `count(*)`-then-insert races. Instead, within the create tx, **`SELECT … FOR UPDATE` the org's `org_usage` row**, check the cap, increment, then insert — serializes per-org creates without global locks. Domain claims rely on the `hostname` unique constraint. **Downgrades that exceed the new cap → read-only-over-limit state, never destructive deletion.**
- **Membership mutations:** invites/accept handled by **Better Auth's Organization plugin**; `auto_join_by_domain` is a service-role function (atomic `FOR UPDATE`, single-use, email-bound, idempotent, freemail-blocklisted, writes `audit_log`).

### 5.4 Org roles & sharing-policy authorization matrix (reqs 3 & 4)

> **Confirmed by user:** members **may** set per-site sharing on their **own** sites, but a member **cannot share to anyone outside the org unless the org's `allow_external_sharing` policy permits it** — and only admins/owners own that policy + role assignment. The matrix below reflects this exactly.

| Capability | owner (creator) | admin | member |
|---|:--:|:--:|:--:|
| Create site / deploy | ✓ | ✓ | ✓ |
| Set sharing on **own** site (within org policy) | ✓ | ✓ | ✓ |
| Set sharing on **any** org site | ✓ | ✓ | ✗ |
| Share **outside the org** (public / external email) | only if `allow_external_sharing` | only if `allow_external_sharing` | only if `allow_external_sharing` |
| Toggle `allow_external_sharing` (org policy) | ✓ | ✓ | ✗ |
| Promote/demote `admin` role | ✓ | ✓ | ✗ |
| Manage billing / delete org / transfer ownership | ✓ | ✗ | ✗ |

- **Creator = `owner`** by default (Better Auth sets the creator's role); `owner` is a superset of `admin`.
- **`allow_external_sharing` default = false** → a brand-new org is fully internal until an admin opts in.
- When an admin flips it **false**, a reconcile job revokes existing external grants + public visibility and writes the edge deny-list (see §6/§10) so already-shared external links stop working.

---

## 6. Edge Auth & Sharing Tiers

**The edge applies auth *narrowly*, by access mode — not a JWT check on every request.** The serving Worker is a thin router; the Go API origin is the real authz layer (§3). Content is served from the **separate PSL domain `*.shippedusercontent.com`** (+ custom domains) so hostile tenant JS can never reach the `app.shipped.app` session — the load-bearing isolation decision.

| Access mode | Who enforces | Identity / token at edge | Phase |
|---|---|---|---|
| `public` (listed/unlisted) | nobody (open); unlisted = unguessable host | **none** — cacheable | **1** |
| `password` | Worker checks password → host-scoped signed cookie | none (no identity) | **2** |
| `allowlist` | viewer identity vs allowlist | host-scoped token from the authz exchange | **2** |
| `org-only` | viewer must be an org member | host-scoped token from the authz exchange | **2** |

**Cookie/domain topology**
- **App session** (`app.shipped.app`): **Better Auth** session cookie, `__Host-` prefix → host-only, `Secure; HttpOnly; SameSite=Lax`, **no `Domain=`**. Never sent to the content domain.
- **Host-scoped content token** (gated sites, Phase 2): issued by `app.shipped.app/authz`, scoped to the exact content host, short TTL. The Worker verifies *this* token (its own secret/JWKS) — **never the operator dashboard JWT**.
- **`shippedusercontent.com` on the PSL** stops `evil.…` from setting a `Domain=…` cookie landing on a sibling (cookie tossing/fixation).

**Decision flow**
```
Request → host.shippedusercontent.com/path  (Serving Worker)
  ├─ KV route:<host> → {org_id, site_id, version_id, access_mode}   (miss → Go cold path, warm KV)
  ├─ access_mode=public   → Cache API → stream R2          ✅  (NO JWT — the 95% path)
  ├─ access_mode=password → valid host-scoped cookie? serve : prompt → verify → Set-Cookie  [P2]
  └─ access_mode=allowlist | org-only:                                                       [P2]
        valid host-scoped token (sig + exp + bound to THIS host)?
          yes → serve   (private: Cache-Control: private, no-store; NEVER caches.default)
          no  → 302 → app.shipped.app/authz?return=<host>
                       Better Auth session? (else Google/email sign-up — ACCOUNT REQUIRED) →
                       AUTHORIZE against LIVE tables (Go API):
                         org-only → viewer ∈ member(site.org_id) (re-check)
                         allowlist→ viewer's VERIFIED email/sub ∈ allowlist_entries
                                    (claim pending grant on first match; KV inline / D1 for large)
                         external/public requires org_meta.allow_external_sharing==true
                       issue host-scoped token (bound to host, short TTL) →
                       set as cookie on the content host (or one-time ?token= the Worker exchanges)
                       → re-enter Worker (now authorized)   ✅
```
Password gate is served from a **platform-controlled origin tenant JS cannot render or script** (anti-phishing — §10).

**Revocation story:** the common case is just the **short token/JWT TTL** (5–15 min) — minted from the revocable Better Auth DB session, so killing the session stops re-mint. **Hard revocation** (ban, org removal) adds a **KV denylist / per-subject `min_iat`** checked at the authz exchange — built in **Phase 4**, not speculatively.

**Edge projection pipeline (connective tissue, Phase 1+):** the **Go API** writes the KV/D1 projection on publish (`route:<host>` + access_mode + allowlist) — it is the **only writer**, the Worker reads only. Postgres is authoritative and the projection is **fully rebuildable** from it (carry a `schema_version`; a reconciler re-pushes on drift; DR drill in §13). When an admin flips `allow_external_sharing`→false or unshares, the Go API rewrites the affected routes within the propagation window.

---

## 7. Deploy UX, CLI & Runtime APIs

### 7.1 `shipped deploy` sequence
```
DEV            CLI                         GO API                    R2
 │  deploy ./dist                            │                        │
 │──────────────►│ walk + sha256 each file → manifest + deploy digest │
 │               │ POST /deployments/prepare (manifest, digest) Bearer shpd_… ─►│ authorize token
 │               │                           │ dedup SCOPED to caller org │ HEAD blobs/<org>/<sha>
 │               │◄── {missing:[sha…], presigned PUT/multipart (keys derived from token org+sha)}
 │               │ upload ONLY missing blobs DIRECT to R2 (parallel parts) ─────►│ store
 │               │ POST /deployments (digest, ETags) ─► CompleteMultipart
 │               │             SERVER-VERIFY each blob's sha256==key; write immutable row + manifest
 │◄── preview_url│◄── (preview host enforces SAME access tier)
 │               │ POST /sites/{id}/publish {deployment_id} → set current_deployment_id (auth)
 │◄── LIVE URL ──│◄── → projection writer → KV route:<host> (epoch bump; reconciler backstops)
 (rollback = publish an older deployment_id = re-flip pointer; ~seconds)
```
Content-addressing is computed client-side; only missing blobs upload; presigned keys are **derived from the authenticated token's org + the server-validated sha256** (never request-body identifiers); server **re-verifies stored bytes hash == key** before `ready`; publish (pointer flip) requires a stricter scope than upload; pointer flip is Postgres-authoritative, KV is a reconcilable cache.

### 7.2 Folder drag-and-drop (first-class, MVP — explicit user requirement)
The dashboard accepts a **dropped folder** of HTML/CSS/assets, not just individual files: the browser walks the directory tree via the `DataTransferItem`/`webkitGetAsEntry()` (drag-drop) and `<input webkitdirectory>` (picker) APIs, computes per-file SHA-256 in a Web Worker, and hits the **same `/deployments/prepare` → presigned-upload → `/deployments` → publish contract** as the CLI. Difference: authenticated with the **user's Better Auth session/JWT** (RLS-enforced via `SET LOCAL app.*`), uploading blobs directly to R2 (CORS allow-listed for `app.shipped.app`). MVP requires a pre-built `dist/` (matches static-now + Quick's no-build ethos); a per-site `spa:true` toggle enables index.html fallback.

### 7.3 Runtime APIs (one auth model, phased)
Every runtime call hits `api.shipped.app` with a **site-runtime token** (issued by the `app.shipped.app/authz` exchange, claims `{org_id, site_id, viewer_sub, access_mode, scopes}`, `aud=api.shipped.app`). Go verifies and forces all access through `org_id`/`site_id` via RLS (`SET LOCAL`). **Off by default, opt-in per site.**

| API | Scoping | Phase |
|---|---|---|
| Identity API | viewer claims; only authorized-viewer-visible data | **v1** |
| Rate limiting | per-site **Durable Object** counters (never KV); IP/Turnstile for anon | **v1 (gates all)** |
| Collection DB (CRUD) + realtime | rows tagged org+site+collection, RLS; Supabase Broadcast-from-DB keyed by site | **v1 / later** |
| File uploads | presigned R2 `uploads/<org>/<site>/…`, quota-checked | **later** |
| LLM / image proxy | keys server-side; **off by default on public; hard per-site $ cap + DO rate limit; Turnstile** | **later** |
| WebSockets / SSR | Durable Objects / Workers-for-Platforms, untrusted mode, per-tenant CPU cap | **later** |

Static→dynamic is **additive**: same manifest, routing, auth, RLS; dynamic just adds a user-Worker behind the same Host.

---

## 8. ORM & Schema Ownership

**Rule: each table has exactly one writer-owner; every other runtime consumes a *generated* artifact, never a hand-written duplicate.** There is no single migration engine over the union — that fought Better Auth. Instead:

- **Better Auth OWNS + migrates** `user/session/account/verification/jwks/organization/member/invitation` (the `auth` schema). The Go API treats these **read-mostly** (membership/role for authz) and **never migrates them**. App data attaches via the separate **`org_meta`** table keyed by `organization.id`, so the two tools never fight over one table.
- **Go OWNS the `app` schema** via versioned SQL migrations (**`goose`**). From that SQL: **sqlc → Go types** (compile-checked, no reflection; cleanest `SET LOCAL` wrapper). The DB stays Go's private detail.
- **The dashboard consumes the API, not the DB.** From the Go API's **OpenAPI** spec → a generated typed TS client. The frontend's contract is the API, decoupling it from the schema. **No Drizzle / Prisma / `supabase-js` in the dashboard** (it never opens a PG connection for business data; Better Auth is the only TS↔PG path, for its own tables).
- **KV/D1 is a rebuildable projection**, written only by the Go API on publish, read-only to the Worker. The Go↔TS struct for the KV value is the one genuine cross-language *data* contract: define it once in **`contracts/`** (JSON Schema → codegen both sides) with a `schema_version` field and a **round-trip test in CI**.
- **RLS policies, GRANTs, the org-policy CHECK/trigger, and `auto_join_by_domain`** are hand-written, reviewed `goose` migrations (diff tools can't capture RLS). Never edit prod schema by hand.
- **Migration order** (CI-gated invariant): Better Auth (`auth`) before app migrations that FK to `organization.id`. Namespaced schemas avoid collisions.

**Identity → DB, the security crux (no Supabase `authenticated` role):**
- **Default (RLS-enforced):** the Go API verifies the **EdDSA JWT via JWKS**, then per request in a tx: `SET LOCAL app.current_user_id = …; SET LOCAL app.current_org_id = …`. Policies read these GUCs. Transaction-scoped → safe under **Supavisor transaction-mode pooling**. Connect as the **non-`BYPASSRLS` `shipped_app` role** with `FORCE ROW LEVEL SECURITY` so RLS is never silently skipped.
- **Deploy path:** **not** blanket service-role. Derive `app.current_*` from the verified deploy token (`org_id` + service-principal), run with `SET LOCAL` so **RLS still constrains every deploy write to the token's org**. Re-derive `site_id→org_id` from the DB and assert == token org before every mutation; never trust request-body identifiers.
- **True `BYPASSRLS`:** only cross-tenant system jobs (GC, projection rebuild, usage rollups, and the cloud-only billing reconciler), a **separate pool/role**, explicit `org_id` filters.
- **CI lint:** fail any request-scoped handler that opens a `BYPASSRLS`/service-role connection; integration test that org A cannot read/deploy to org B.

---

## 9. Billing & Enterprise

**Stripe, one Customer per org**, hybrid seat + usage + flat fee. Thin `BillingProvider` Go interface for future MoR wrap. Stripe Tax covers remittance. **All of this lives in the cloud-only module (§14); the self-host build ships without it and is unlimited.**

> **Phasing (refined):** billing is **Phase 3** — core development is *not* gated on Stripe. In **Phase 0** the core defines a **`QuotaProvider` interface and ships a no-op/unlimited implementation**, so cloud can inject the real (counter-enforcing) provider later without retrofitting any call site. The quota counters live in `app.org_usage` (core), but the *enforcement policy + Stripe* live in `cloud/`.

**Tiers are member-count bands with HARD CAPS** (each cap blocks the action and opens the right CTA — upgrade, or contact-sales at the top):

| Dimension | Free | **Business** | **Enterprise** | Contact Sales |
|---|---|---|---|---|
| **Members / org (HARD CAP)** | **≤ 5** | **6–99** (cap 99) | **100–1,000** (cap 1,000) | **> 1,000** (no self-serve) |
| Pricing | $0 | self-serve, **per-seat** | self-serve per-seat **or invoiced** | custom / invoiced |
| **Sites / user** | **≤ 10** | 100 | 1,000 | unlimited |
| Deploys/mo | 100 | 5,000 | 50,000 | custom |
| Bandwidth/mo | 10 GB (402 hard-stop) | 250 GB | 2 TB pooled | committed |
| Egress overage | — | $0.20/GB | $0.15/GB | negotiated |
| Storage | 1 GB | 100 GB | 500 GB | custom |
| Runtime calls/mo | 0 | 250k | 5M pooled | custom |
| Custom domains | 0 | 5 | 50 | unlimited |
| Sharing tiers | a+b+c | + password/unlisted | + IP allowlist | + custom |
| Domain auto-join · SSO/SAML | no · no | yes · no | yes · **yes** | yes · yes |
| Audit logs · Versioning | no · last 1 | 30-day · last 25 | export · last 100 | export · unlimited |
| Support/SLA | community | email | priority | 99.9% SLA, TAM, MSA/DPA |

**The hard caps & their gates (req 4 + new bands):** checked **synchronously in the Go control plane (`cloud/quota`)** at the cost-creating action — `POST /sites` (count the user's owned sites) and member-add/invite-accept (count the org's members), read against the org's **live `subscriptions.plan_tier`**:
- Free: `sites_per_user ≤ 10`, `members_per_org ≤ 5` → exceed → `402` → **upgrade to Business**.
- Business: `members_per_org ≤ 99` → 100th member → `402 … {next_tier:"enterprise"}` → **upgrade to Enterprise**.
- Enterprise: `members_per_org ≤ 1000` → 1001st member → `402 … {next_tier:"contact_sales", sales_url}` → modal shows **Contact Sales** (no checkout).

The API returns **`402 quota_exceeded { limit, current, max, plan_tier, next_tier, upgrade_url|sales_url }`**; the dashboard opens the subscription modal (or sales CTA), the CLI prints the URL. Enforcement is **server-side only** (never UI-trusted); a guarded `SELECT … FOR UPDATE` count (or a partial constraint) prevents two concurrent creates from both slipping past a cap.

**Stripe payment, upgrade & webhook flow** — *the entitlement (`plan_tier`) is written to the DB ONLY by a signature-verified webhook, never by the browser's success redirect.*

```
 UPGRADE (first paid, or tier change)
 ─────────────────────────────────────
 dashboard (Vercel)        Go API: cloud/billing            Stripe                 Postgres / KV
   │ 402 quota_exceeded → modal "Upgrade to Business"
   │ POST /billing/checkout {org_id, target_tier, seats}
   │────────────────────────►│ authz: caller is owner/admin (billing perm)
   │                         │ ensure Stripe Customer for org (create→store stripe_customer_id)
   │                         │ Checkout.Session.create(mode=subscription,
   │                         │   line_items=[{price: PRICE_BUSINESS, quantity: seats}],
   │                         │   client_reference_id=org_id, metadata={org_id,target_tier},
   │                         │   success_url, cancel_url, idempotency_key) ──────►│ creates Session
   │◄────────── checkout_url ─┤◄──────────────────────────────────────────────────┤
   │ redirect user to Stripe Checkout ───────────────────────────────────────────►│ user pays
   │                                                                               │
   │  (success_url shows "processing…" — NOT yet entitled; UI polls plan_tier)     │
   │                                                                               │
 WEBHOOK (the source of truth)                                                      │
   │           POST /webhooks/stripe  ◄── checkout.session.completed,              │
   │                                      customer.subscription.created/updated ───┤
   │                         │ 1. verify Stripe-Signature (webhook secret) else 400
   │                         │ 2. INSERT event.id INTO processed_stripe_events
   │                         │      ON CONFLICT → 200 & return (idempotent dedupe)
   │                         │ 3. resolve org_id (client_reference_id/metadata/customer)
   │                         │ 4. read sub → {tier from price, seats=qty, status,
   │                         │      current_period_end}
   │                         │ 5. UPSERT subscriptions(org_id, stripe_customer_id,
   │                         │      stripe_subscription_id, plan_tier, seats, status,
   │                         │      current_period_end)  ◄── PAID TIER SAVED HERE ──►│ row updated
   │                         │ 6. push plan_tier+org_status → KV (edge) ────────────►│ KV set
   │                         │ 7. 200 OK (fast; heavy work async)
   │◄── UI poll sees plan_tier=business → close modal, unblock the action ──────────┤
```

**Lifecycle webhooks → DB state (Stripe is the source of truth; we mirror):**
- `customer.subscription.updated` — seat/tier change, scheduled cancel → update `subscriptions` (tier, seats, `cancel_at_period_end`).
- `customer.subscription.deleted` — downgrade to **Free**: `plan_tier='free'`, `status='canceled'`; if the org now exceeds Free caps, set `org_status='over_limit'` (read-only/no-new-members + banner) — **never delete data**.
- `invoice.paid` → `status='active'`, extend `current_period_end`. `invoice.payment_failed` → `status='past_due'`; after dunning, `org_status='past_due'` (KV) restricts new actions + shows billing banner.
- **Tier upgrades after the first payment** use Stripe `Subscription.update` (new Price, `proration_behavior=create_prorations`) or the **Billing Portal** for self-service seat/plan/payment-method/cancel; every change is confirmed by the corresponding `customer.subscription.updated` webhook before the DB flips.

**Entitlement reads:** `subscriptions.plan_tier` (Postgres) is authoritative; mirrored to **JWT `app_metadata.plan_tier`** (refreshed each session) for routing and **KV `org:<id>:{plan,status}`** for the edge. The synchronous hard-cap check reads the live `subscriptions` row (short-TTL cached). **Security:** the webhook endpoint is unauthenticated-but-signature-verified, runs only in `cloud/billing`, uses a restricted Stripe key, and the browser success redirect grants *nothing* — only the webhook mutates entitlement, so a user can't forge an upgrade.

**Metering (each at the layer that owns ground truth):** members from Better Auth `member` rows + sites from the `sites` table (live counts) → Stripe subscription-items via transactional outbox (allowlist viewers don't consume seats); bandwidth at the **edge Worker → Analytics Engine** (never KV counters) → Go rollup → Stripe Meter; runtime calls counted in Go with a hard LLM $ ceiling + circuit breaker. Plan tier cached in JWT for routing, but **suspension/downgrade enforced immediately via a fast KV `org_status` flag** written on webhook. All meters idempotent (event-id dedupe). Enterprise: **Better Auth SSO/SAML plugin** (UUID + domain-claim, never email), append-only audit + SIEM export, Cloudflare for SaaS custom hostnames, Stripe Invoicing (NET-30/60, ACH/PO), DPA + SOC2.

---

## 10. Security Hardening (launch-blocking)

The strongest controls are at the serving edge; the two highest-risk surfaces route around them.

- **[CRITICAL] Deploy path keeps RLS in force.** No blanket service-role on deploy/runtime. Derive `app.*` GUCs from the verified deploy token; `SET LOCAL app.org_id/user_id/role`; re-derive `site_id→org_id` and assert == token org before any mutation; never trust request-body identifiers. Connect as a non-`BYPASSRLS` role. CI lint blocks service-role on request handlers; test org A ✗ deploy to org B.
- **[CRITICAL] External-sharing policy is enforced in depth, not just UI.** The `allow_external_sharing=false` rule is enforced at (1) the Go API (authz on access-mode + grant writes), (2) the DB (CHECK/trigger rejecting `access_mode='public'`/`is_external` grants under the policy), and (3) the edge `/authz` exchange (re-checks the projected org policy before authorizing any external/public viewer; routes revoked when an admin disables it). UI-only enforcement is insufficient — assume a malicious member calls the API directly.
- **[HIGH] Allowlist grants require a verified Shipped account (req 2).** A grant for `alice@x.com` is honored only for a signed-in user whose email is **verified** on their Better Auth account (`emailVerified=true`; Google is verified, email/password must confirm). Never match an unverified email — otherwise an attacker self-registers a victim's address to claim a grant. Pre-registration grants stay `pending` until claimed; surface "claimed by" in the audit log.
- **[HIGH] Only admins mutate org policy & roles (req 4).** Role-promotion and `allow_external_sharing` writes are gated by `has_org_role(org,'admin')` at the API and via Better Auth Organization access-control; never client-trusted. The creator is seeded as `owner` server-side, not from any client field.
- **[CRITICAL] Anonymous runtime tokens must not drive paid endpoints.** LLM/image proxy **off by default for public sites**; opt-in with mandatory low per-site daily $ cap + **hard per-site rate limit in a Durable Object** (anon `viewer_sub` is rotatable); require **Turnstile/PoW** before issuing anon tokens with expensive scopes; never echo provider error bodies.
- **[HIGH] Per-org blob storage — no cross-tenant dedup.** Key = `blobs/<org_id>/<sha256>`; dedup scoped to caller's org (global existence check = content-confirmation oracle). Worker builds the R2 key from the resolved org_id of the host, never client-supplied path. Makes GDPR hard-delete safe.
- **[HIGH] Broker hardening (the "$35k class").** No free-form `return` URL — resolve canonical content host server-side from `site_id`, redirect only to that exact host+path. **Deliver the token via server `Set-Cookie` on a content-host callback, never a URL query/fragment param** (no Referer/history/log leak). Single-use non-fixatable nonce; enforce `__Host-`; reject `aud` != request host.
- **[HIGH] Preview/deploy-hash URLs enforce the site's access tier** — the hash only hides the URL. Offer a separate, time-boxed, revocable "share preview" grant. `Referrer-Policy: no-referrer` on all content.
- **[HIGH] Revocation under staleness — mandatory deny-list.** KV `revoked:<site|user|org>` checked on **every** gated request, fail-closed. Short edge-token TTL (2–5 min). On member removal / unshare / external-policy disable: (1) write deny-list key, (2) **revoke the user's Better Auth sessions/JWTs** so they can't re-mint, (3) purge cached responses. Residual window < 1 min, documented.
- **[HIGH] Cache-key isolation.** Never cache org/allowlist/password responses in a shared namespace — only truly public. Gated responses carry `Cache-Control: private, no-store`. Invariant test enforces it.
- **[MEDIUM]** CSP is not the isolation control (domain/PSL/origin separation is); minimally-scoped runtime tokens; **block service-worker registration on content origins**. · Presigned-upload integrity (keys derived from token + server-validated manifest; verify stored hash == key; `if-none-match` write-once). · Subdomain/slug takeover (re-verify DNS-TXT/DCV periodically; atomic purge on delete; reserved-slug blocklist `www/app/api/admin`). · Deploy-token hardening (single-site least-privilege default; prefer GitHub-OIDC short-lived exchange; store hashes; instant revoke). · Markdown XSS (sanitize by default; never collect the site password on the tenant origin). · Denial-of-wallet (key anon limits on IP+Turnstile; per-org egress spend caps + 402 across all tiers; freemail blocklist for auto-join).

---

## 11. Repository layout (greenfield — what to create)

Monorepo (Turborepo or plain workspaces + Go modules). **License boundary baked into the directory layout** (so the self-host build trivially excludes non-OSS code — see §14):
```
shipped/
├── LICENSE                # FSL-1.1-Apache-2.0 — governs everything EXCEPT cloud/ and ee/
├── apps/
│   ├── dashboard/         # [FSL] Next.js App Router (app.shipped.app): Better Auth (/api/auth/*, Org+JWT+SSO)
│   │                      #   owns identity tables; /authz exchange (P2); UI calls Go API via generated
│   │                      #   OpenAPI client — NO direct Postgres for business data
│   └── docs/              # marketing/docs (optional)
├── services/
│   └── api/               # [FSL] Go: chi (api.shipped.app) = SYSTEM OF RECORD; verifies EdDSA JWT;
│       │                  #   deploy orchestration, KV/D1 projection writer, authz. cloud/ via interfaces.
│       ├── internal/{deploy,edge,db(sqlc),authz,quota(QuotaProvider iface; no-op default)}
│       └── openapi/        #   OpenAPI spec → dashboard TS client
├── edge/
│   └── serving-worker/    # [FSL] Cloudflare Worker (*.shippedusercontent.com): R2+KV+D1; public=no-JWT router,
│                          #   gated=host-scoped-token verify (P2)
├── cli/                   # [FSL] Go: `shipped` binary (GoReleaser), shares services/api deploy code
├── contracts/            # [FSL] cross-language data contracts (KV value shape): JSON Schema → Go + TS,
│                          #   schema_version + CI round-trip test
├── cloud/                 # [PROPRIETARY, cloud-only — NOT in self-host build]
│   ├── billing/           #   Stripe integration + webhooks, subscription modal backend, BillingProvider impl
│   └── quota/             #   real QuotaProvider: hard-cap gate (Free 5u/10s · Business · Enterprise) + 402
├── ee/                    # [SHIPPED ENTERPRISE EDITION LICENSE] SSO/SAML, audit export, advanced RBAC, custom domains
├── db/
│   ├── migrations/app/    # [FSL] Go-owned app schema — goose migrations + hand-written RLS/GRANT/trigger
│   ├── auth-schema/       #   Better-Auth-generated SQL (generate-only; Better Auth migrates the auth schema)
│   └── sqlc.yaml
├── deploy/                # [FSL] docker-compose.yml + Helm chart for one-command self-host
└── packages/
    ├── auth/              # shared Better Auth config (providers, org access-control rules, JWKS)
    └── tsconfig/eslint    # shared config
```
The OSS core depends on `cloud/`/`ee/` only through **interfaces with no-op/unlimited default implementations** (e.g. `QuotaProvider`); the self-host build compiles those defaults, the cloud build wires in the real ones (Go build tags / DI; TS dynamic import behind `SHIPPED_CLOUD`). **CI asserts the core has zero references into `cloud/`/`ee/`/`billing` schema.**

**Cloud deployment targets:** `apps/dashboard` → **Vercel** (`app.shipped.app`); `edge/serving-worker` → **Cloudflare** Workers on `*.shippedusercontent.com` with R2 + KV/D1 bindings; `services/api` → a container runtime (Cloud Run / Fly / Railway) on `api.shipped.app`; Postgres → Supabase. (Self-host runs the dashboard + API as containers via `deploy/` and substitutes MinIO + a self-host serving path for R2/Workers.)

---

## 12. Phased Roadmap

Front-load the irreversible decisions (RLS tenant context, open-core boundary, schema-ownership seams); do **not** gate core development on Stripe or on cross-domain viewer-auth.

- **Phase 0 — Foundations & contracts (de-risk the seams).** Monorepo + FSL headers + open-core boundary (`billing` as a separate schema/module the OSS build excludes). Postgres + **goose** migrations; **`shipped_app` non-BYPASSRLS role** + `FORCE RLS`. Better Auth installed (Google OAuth, Organization + JWT/**EdDSA** plugins, live JWKS). Wire **sqlc + OpenAPI codegen** and the **`contracts/` KV shape**. Define the **`QuotaProvider` interface** (core ships a no-op/unlimited impl). **Spike the one risky integration: Go verifies a Better Auth EdDSA JWT via cached JWKS with `kid` refresh + alg pinning** (reject `alg:none`/`HS256`). Validate **Supavisor transaction-mode pooling + `SET LOCAL`** doesn't leak GUCs.
- **Phase 1 — Core publish/serve loop (the heart, no billing).** Upload bundle → Go API → R2 → immutable `site_version` → Go writes KV/D1 projection. Worker serves `*.shippedusercontent.com/<slug>` from R2 via KV — **public only, no edge JWT**. Dashboard: Google login → create org → create site → deploy → live URL (UI via the OpenAPI client; no direct PG), with **polished sign-up/sign-in and system light/dark** (Tailwind + shadcn/ui + `next-themes`, §4). **RLS enabled with `SET LOCAL` tenant context from day one** (don't retrofit isolation). Hold the **"KV is rebuildable from Postgres"** invariant. Versioning + rollback (free from immutable versions). Reserved-slug list; structured logs w/ correlated `request_id`.
- **Phase 2 — Access control & domains.** Access modes: `public` → `password` (host-scoped cookie, no identity) → `allowlist`/`org-only` (the **cross-domain `/authz` viewer exchange** from §1/§6 — the sleeper-hard piece; **allowlist requires a verified Shipped account**; external/public gated in depth by `allow_external_sharing`). Org roles/invitations via Better Auth org plugin + member-management UI; creator=`owner`, admin-only policy/role changes. Custom domains via **Cloudflare for SaaS** (DNS-TXT + TLS). Unlisted + expiration TTL.
- **Phase 3 — Cloud billing & quotas (proprietary, cloud-only).** Stripe subscriptions + webhook lifecycle (**entitlement persisted by signed webhook**, §9); map tiers to the Phase-0 `QuotaProvider`; transactional **`org_usage` FOR UPDATE** hard-cap enforcement (Free 5u/10s → Business <100 → Enterprise 100–1,000 → Contact Sales); over-limit/downgrade → **read-only, not destructive**; 402 → subscription modal. OSS core still runs with the no-op provider.
- **Phase 4 — Hardening & scale.** KV **denylist / `min_iat`** hard-revocation; audit logs; end-to-end tracing (Vercel→Worker→Go→PG); edge rate limiting + denial-of-wallet caps; upload abuse/malware scanning + takedown/quarantine; R2 version GC; **RLS policy test suite**; SSO/SAML (Better Auth, UUID-keyed) + SCIM; runtime APIs (Identity, rate-limit, collection-DB, LLM proxy with §10 guardrails); security review; **backup/restore + KV/D1 rebuild-from-Postgres DR drill**.

---

## 13. Verification & Bootstrap

Stand up and prove each layer end-to-end before building atop it. Each row is a falsifiable check.

| # | Layer | Bootstrap | Proof |
|---|---|---|---|
| 1 | Domains/PSL | Register `shippedusercontent.com`, submit to PSL; wildcard DNS + cert | Browser refuses a `Domain=shippedusercontent.com` cookie set from `a.…` reaching `b.…` |
| 1b | JWKS/JWT spike (P0) | Mint a Better Auth **EdDSA** JWT; point the Go verifier at JWKS | Go **accepts** the EdDSA JWT, **rejects `alg:none` and `HS256`**, and refreshes JWKS on `kid` rotation (rate-limited) |
| 2 | DB + RLS | `goose` app migrations as `shipped_app` (non-BYPASSRLS) + `FORCE RLS`; Better Auth migrates `auth` schema | As `shipped_app` with `SET LOCAL app.current_org_id=A`: org B rows invisible for select/update/delete; **`FORCE RLS` blocks the owner path too**; deploy-token for A can't write B; a member can't write `org_meta`/`member.role` |
| 2b | Pooling + `SET LOCAL` | Supavisor transaction-mode pool | Tenant GUCs don't leak across pooled transactions (interleave two orgs' txns) |
| 3 | Auth + roles | Better Auth (Google + email/magic, email-verify) + Organization + JWT/EdDSA | Google sign-up works; JWT shows `user_id`/`org_id`/`role`; member can't toggle `allow_external_sharing` or promote admins (403); with policy=false the **API** rejects public/external grants (not just UI) |
| 4 | Storage + deploy | R2 + Go prepare/publish + CLI | `shipped deploy ./fixture` → only-changed-blob upload; server rejects blob whose bytes ≠ claimed hash; rollback in seconds |
| 5 | Public serve (P1) | Serving Worker (R2+KV) on `*.shippedusercontent.com` | Public site loads with **no JWT**; cache hit < 20ms |
| 6 | Gated viewer-auth (P2) | `/authz` exchange on `app.shipped.app` | Allowlist: an **un-registered** invited email is forced to sign up first, then (verified) sees the site, non-listed 403; org member sees, non-member 403; password gate from platform origin; host-scoped token bound to one host (replay at host B fails) |
| 7 | Revocation (P4) | Short token TTL + KV denylist/`min_iat` | Remove a member → re-auth at `/authz` fails; revoked Better Auth session can't re-mint; denylist propagates < 60s |
| 8 | Projection rebuild | Go is the only KV/D1 writer | Publish reflects at edge; **wipe KV/D1 and rebuild the routing projection from Postgres → serving recovers** (DR drill) |
| 9 | Stripe payment + entitlement (P3) | `cloud/billing`: Checkout Session + `/webhooks/stripe` (Stripe CLI `stripe trigger`) | `POST /billing/checkout` returns a Checkout URL (only owner/admin); `checkout.session.completed` webhook **writes `billing.subscriptions.plan_tier`** + pushes KV; **success redirect alone does NOT entitle** (forged redirect → still Free); replayed `event.id` ignored; `subscription.deleted` → Free + `org_status=over_limit`, no data loss |
| 9b | Hard-cap bands (P3) | `cloud/quota` `QuotaProvider` on `POST /sites` + member-add, `org_usage` `FOR UPDATE` | Free: 11th site / 6th member → `402{next_tier:business}` → modal; Business: 100th member → `402{next_tier:enterprise}`; Enterprise: 1001st → `402{next_tier:contact_sales}` (no checkout); **N concurrent creates at a cap-10 org → exactly 10 succeed** |
| 10 | Open-core build | Build core **without** `cloud/`+`ee/`+`billing` schema | Phase-1 e2e (login → create site → deploy → public serve) passes; **CI proves zero core→cloud refs**; LICENSE files present per directory |
| 11 | Contract round-trip | `contracts/` KV shape (Go ↔ TS) | Go writes a KV value, Worker (TS) parses it against the schema and back; CI fails on drift; `schema_version` honored |
| 12 | Local dev | `supabase start` (Postgres) + Better Auth + Wrangler `--local` + `*.shipped.test` | Public serve works offline; the P2 `/authz` cross-domain handoff (Google → host-scoped token) reproducible offline |
| 13 | Self-host stack | `docker compose up` from `deploy/` (Postgres + dashboard + Go API + **MinIO** for R2) | Whole stack boots offline; login → create site → deploy → **public serve** works with **no Stripe, no limits** |

**Highest-leverage first actions (Phase 0):** (1) the **JWKS/JWT spike** (#1b) + **Supavisor `SET LOCAL` pooling check** (#2b) — the two integrations that, if wrong, invalidate the auth and isolation models; (2) the **schema-ownership seams** (`contracts/` round-trip #11, migration order, `org_meta` keyed by `organization.id`) — expensive to retrofit; (3) the **open-core boundary** (#10) — CI proving zero core→`cloud/` references keeps the OSS build honest from commit one.

---

## 14. Open-Source & SaaS (Open-Core) Model + Licensing

Shipped follows the **Supabase/PostHog open-core playbook**: a public, source-available codebase that anyone can self-host, with revenue from the hosted cloud + enterprise features — *not* from selling licenses to self-hosters.

### 14.1 Licensing (chosen: FSL → Apache)
- **Core repo → `FSL-1.1-Apache-2.0`** (Functional Source License, Sentry's). Grants **everything except "Competing Use"** — you may self-host, modify, and use Shipped internally (including at a for-profit company), but you **may not offer Shipped (or a derivative) to third parties as a commercial/hosted service**. Each release **auto-converts to Apache 2.0 two years later**, so the project still becomes truly open over time. This is the practical, adoption-friendly reading of "self-host but can't make money off it."
  - *Note on terminology:* FSL is **source-available**, not an OSI-"Open Source" license — by definition no OSI license can forbid commercial/SaaS use. If a real OSI badge is required later, the fallback is **AGPLv3** (allows competition but forces source-sharing of networked forks); we deliberately chose FSL over AGPL to actually block resale.
- **`cloud/` → proprietary, all-rights-reserved**, never published in a runnable self-host build. Holds Stripe billing + the quota gate (the only place the 10/5 limits exist).
- **`ee/` → "Shipped Enterprise Edition License"** (PostHog-style, source-visible but use-restricted, license-key-gated): SSO/SAML, audit-log export, advanced RBAC, custom domains.
- **Contributions: DCO sign-off** (lightweight) on the FSL core; a **CLA** only if/when we want to keep dual-licensing rights. Trademark "Shipped" reserved (forks must rename to redistribute), per the Supabase/PostHog norm.

### 14.2 Why this satisfies both goals
| Goal | Mechanism |
|---|---|
| Anyone can self-host | FSL grants self-host + internal use for free; `deploy/` ships Docker Compose + Helm |
| They can't "make money" off Shipped | FSL's **Competing-Use** ban (can't resell it as a service); trademark stops rebranded clones |
| We run a paid SaaS | `cloud/` (proprietary) adds quotas + Stripe on our hosted deployment only |
| We sell to enterprises | `ee/` license-keyed features (SSO, audit, RBAC, custom domains) |
| Community trust / eventual openness | FSL converts to Apache 2.0 after 2 years per release |

### 14.3 How the build split actually works (so limits can't be patched out of *our* cloud, and self-host stays clean)
- The OSS core calls billing/quota through a Go **`Quota`/`Billing` interface** (and a TS boundary behind `process.env.SHIPPED_CLOUD`). The **default OSS implementation is "unlimited / no-op."**
- The **cloud build** compiles `cloud/quota` + `cloud/billing` (via Go build tags + DI / Next.js dynamic import) to supply the real 10-sites-per-user, 5-members-per-org gate and Stripe. This code lives in the private `cloud/` tree and is built only in our deploy pipeline — a self-hoster never receives it, so there is nothing to patch out, and our cloud isn't relying on a client-side check.
- **The free-tier limits are therefore a cloud-pricing concern, enforced server-side in `cloud/quota` (Go), surfaced as `402 quota_exceeded` → Stripe subscription modal** (see §9). Self-host = the no-op impl = unlimited.

### 14.4 Open-source operational must-haves (MVP)
Public mono-repo with clear `LICENSE` per directory + SPDX headers; `CONTRIBUTING.md` + DCO bot; reproducible **one-command self-host** (`docker compose up` / Helm) that boots Postgres + dashboard + Go API + a local object store (MinIO as an R2/S3-compatible stand-in for self-host) + a self-host serving path; `.env.example` with `SHIPPED_CLOUD=false`; security policy + responsible-disclosure; and CI that builds **both** the OSS-only image (asserts `cloud/`+`ee/` absent) and the cloud image.

---

## 15. Post-launch backlog (deferred from Phase 4)

Phase 4 deliberately scoped down to the **security/ops hardening** that is achievable and testable inside the repo with no new external accounts or vendor contracts. The items below were on the original §12 Phase-4 wish-list but are **intentionally NOT yet built** — each needs external services, paid runtime infrastructure, or a vendor relationship, so they are post-launch (mostly enterprise) work rather than launch-blocking. They are listed here so the omission is a documented decision, not a gap.

### 15.1 What Phase 4 *shipped* (the hardening core)
For reference, so the backlog is read against what exists. Phase 4 delivered, with tests, and kept all Phase 0–3 tests green:

- **Audit logging.** Writes to the existing `app.audit_log` table (`org_id, actor_user, actor_token, action, target, metadata jsonb, ip, created_at`) on security-relevant mutations (member add/remove, role change, policy/access-mode change, publish/unshare, billing lifecycle), correlated by `request_id`, read back through an org-scoped, RLS-enforced audit API.
- **Hard revocation / denylist.** The Go API (via the projection / Cloudflare-KV writer) writes `revoked:user:<id>` / `revoked:site:<id>` / `revoked:org:<id>` → `{ min_iat }` keys on the **three** real token-revocation triggers: **member removal**, **site unshare / access-tightening**, and **`allow_external_sharing` disable**. Writes are **idempotent** (`max(existing, new)` min_iat) and **rebuildable** — a stale denylist only fails **closed** (extra re-auth), never opens access. Both the **serving Worker** (gated path) and the **`/authz` exchange** reject an edge token whose `iat` predates any matching `min_iat` (302 → `/authz` re-auth). Short edge-token TTL (15m) is the backstop; the denylist makes revocation immediate. **Billing suspension / over_limit is NOT a token revocation** — it would hard-cut existing viewers, contradicting the §9 read-only `over_limit` model. Instead, billing writes the per-org **`org_status:<org_id>` KV flag** (via the same projection writer, `OrgStatusWriter`); the edge then serves a **platform block page** (read-only) for a suspended/over-limit org while the org keeps all its data and existing tokens stay valid.
- **Edge rate-limiting + denial-of-wallet caps.** Per-subject/per-site limits and hard caps fail closed before any expensive work.
- **Content security headers** on served content (CSP as defense-in-depth — *not* the isolation control, which remains domain/PSL/origin separation per §10 — plus `Referrer-Policy: no-referrer`, frame/`nosniff`/permissions controls, and `Cache-Control: private, no-store` on gated responses).
- **RLS policy test suite** — a table-driven integration suite asserting the `FORCE RLS`, subquery-free, `org_id`-keyed policies (org A cannot read/write org B; `shipped_app` is non-`BYPASSRLS`; GUC isolation across pooled transactions).
- **R2 version GC** — reaps blobs/versions unreferenced by any live deployment pointer (per-org keyspace, GDPR-hard-delete-safe).
- **DR rebuild** — a CLI/ops path over the existing `store.RebuildProjection` that wipes and rebuilds the KV/D1 routing + denylist projection from authoritative Postgres (the §13 #8 DR drill).

### 15.2 Deferred — NOT yet built, and why

| Deferred item | Why deferred (what it needs) | Lands in |
|---|---|---|
| **SSO / SAML** — Better Auth SSO plugin, **UUID-keyed** IdP config | Needs real IdP accounts (Okta/Entra/Google Workspace) + per-customer metadata exchange; enterprise sales-gated, license-key feature | `ee/`, post-launch |
| **SCIM** — directory-driven user/group provisioning + deprovisioning | Pairs with SSO; needs an IdP SCIM connector and a long-lived provisioning token surface | `ee/`, post-launch |
| **Runtime APIs — collection DB + realtime** (rows tagged org+site+collection under RLS; Supabase Broadcast-from-DB keyed by site) | A whole stateful data product (write APIs, realtime fan-out, quotas) beyond static serving; large surface, separate hardening pass | runtime APIs, §7.3 `later` |
| **Runtime APIs — file uploads** (presigned R2 `uploads/<org>/<site>/…`, quota-checked) | Per-site upload quota accounting + abuse controls not yet in place | runtime APIs, §7.3 `later` |
| **Runtime APIs — LLM / image proxy** with the §10 **denial-of-wallet** guardrails (server-side keys; off-by-default on public; hard per-site $ cap + DO rate limit; Turnstile/PoW; never echo provider error bodies) | Needs paid provider keys + spend-cap billing plumbing + Turnstile; the §4 edge rate-limit/DoW caps shipped in Phase 4 are the *substrate*, but the metered proxy itself is post-launch | runtime APIs, §7.3 `later` |
| **WebSockets / Durable Objects / Workers-for-Platforms dynamic runtime** (untrusted user-Workers behind the same Host, per-tenant CPU cap) | Real dynamic-compute isolation + per-tenant resource accounting; substantial new trust boundary | runtime APIs, §7.3 `later` |
| **Third-party malware / abuse scanning vendor + automated takedown / quarantine** | Requires a scanning-vendor contract/API and a takedown workflow; the serving plane (PSL isolation, no-service-worker on content origins, per-org keyspace) already contains hostile content, so this is an abuse-ops add-on, not a launch blocker | abuse-ops, post-launch |
| **Per-site configurable CSP UI** | CSP ships as a sane fixed default in Phase 4 (defense-in-depth); a per-site policy editor is a product-surface enhancement, not a security control | product, post-launch |
| **Usage-based runtime billing** (metering the runtime APIs above) | Depends on the runtime APIs existing first; extends `cloud/billing` with metered Stripe usage once there is runtime usage to meter | `cloud/`, post-launch |
| **Full OpenTelemetry tracing backend** (end-to-end Vercel → Worker → Go → PG spans into a collector/backend) | Needs a hosted tracing backend + collector deployment. Phase 4 ships **structured logs with a correlated `request_id`** propagated across the API and onto audit rows (the trace *substrate*); exporting OTel spans to a backend is the deferred piece | ops, post-launch |

These deferrals do not weaken the launch posture: the authz boundary (Go API + RLS), the edge isolation (PSL/origin separation), hard revocation, audit, and the rate-limit / denial-of-wallet caps are all in place. The deferred items are additive enterprise and dynamic-runtime capabilities layered on top.
