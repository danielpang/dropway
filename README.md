<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Dropway

### A folder of files → a live, access-controlled URL in one command.

Dropway turns any static site — an HTML/CSS report, a data dashboard, a React or
Vite build, a docs site, an AI-generated page — into a **live, versioned, shareable
URL** in seconds. Drag a folder into the dashboard, or run `dropway deploy ./dist`,
and you get a real URL you control: share it with the whole internet, your whole
company, a few specific people, or behind a password — and change your mind anytime.

It's **open source and self-hostable**, with an optional hosted SaaS at [dropway.dev](https://dropway.dev). Think

```sh
dropway deploy ./dist
# → https://acme-quarterly-report.dropwaycontent.com  (acme org-only, versioned, rollback-able)
```

---

## Why teams need it

**Building something is easy now. Sharing it — to exactly the right people, safely —
is still annoying.** You've generated a report, a prototype, a dashboard, or a page,
and your options are all bad:

- **Email a zip / screenshot** → no live URL, instantly stale, impossible to update.
- **Spin up S3 + CloudFront / a Vercel project** → IAM, build config, a new project
  per artifact, and *no per-link access control* — it's public or it's nothing.
- **Paste into a wiki** → loses the real layout, interactivity, and your CSS/JS.
- **Internal file shares** → no link you can send someone outside the team, no expiry,
  no audit of who saw what.

None of these let you say *"share this with **these three people**,"* or *"anyone at
my company,"* or *"public, but password-protected,"* — and then **revoke it**, **roll
back** a bad version, or put it on a **custom domain**.

Dropway is that missing layer: **one command to publish, with sharing and access
control as first-class features.**

## What you get

- **One-command deploy** — folder → live URL. No pipeline, no config (CLI *or*
  drag-and-drop in the dashboard). Pre-built static output just works.
- **Four sharing tiers, per site** — **public**, **password-protected**, **specific
  people** (email allowlist), or **anyone in your org**. Default-deny; you opt in.
- **Multi-tenant orgs** — teams, roles (owner/admin/member), and an org-wide policy
  that can forbid sharing outside the company entirely.
- **Immutable, versioned deploys** — every deploy is content-addressed; **instant
  rollback** to any previous version.
- **Custom domains** + **expiring share links** + a full **audit log** of who shared
  and accessed what.
- **Immediate revocation** — remove a member or unshare a site and their access is
  cut at the edge right away, not whenever a token happens to expire.
- **Safe to serve untrusted content** — tenant HTML/JS is served from a separate
  Public-Suffix-List domain, so one site can never reach another's (or your) session.
- **LLM-friendly access** *(coming soon)* — **public** sites auto-serve an
  [`llms.txt`](https://llmstxt.org/) index and welcome AI crawlers (GPTBot, ClaudeBot,
  PerplexityBot, …), so agents can discover and read your content. **Gated** sites
  (org-only / allowlist / password) stay off-limits to crawlers — LLMs reach them
  **only** through the authenticated **Dropway MCP server**, so your access control
  holds for AI exactly as it does for people.
- **No surprise bandwidth bills** — content is served from Cloudflare R2 (free egress),
  so heavy traffic doesn't translate into a heavy invoice.
- **Open source + self-hostable** — run the whole thing yourself, unlimited, for free.

## Who it's for

- **Engineers & data/analytics teams** sharing generated reports, notebooks-as-HTML,
  benchmark dashboards, and one-off tools — internally or with a client.
- **Designers & PMs** sharing static prototypes, design specs, and review builds with
  the exact stakeholders who should see them, password-protected if needed.
- **AI app / agent builders** that generate websites and need to hand a user a real,
  access-controlled URL programmatically.
- **Companies** that need *governed* sharing — "internal by default, external only if
  an admin allows it," with roles, audit, custom domains, and instant revocation.

## How it works (at a glance)

```
  dropway deploy ./dist  ─▶  Go API (system of record + authz)  ─▶  R2 (content-addressed blobs)
                                       │ writes a rebuildable routing projection
                                       ▼
   browser ─▶ Cloudflare edge Worker (*.dropwaycontent.com) ─▶ streams your site
              public = no login, cacheable · gated = host-scoped token from /authz
```

A **Next.js dashboard** (with Better Auth: Google / email / magic-link) is the control
plane; a **Go API** is the system of record and the authorization boundary; a
**Cloudflare Worker** serves content at the edge; **Postgres** (with row-level security
per org) is the source of truth. The full design — domains, RLS data model, edge auth,
deploy flow, billing — is in **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)**, and the
component + request-flow **[diagrams are in `docs/diagrams/`](docs/diagrams/)**.

---

## Run it locally

You need **Docker** (with Compose). One command builds and starts the whole stack —
the Go API, the Next.js dashboard, a bundled Postgres, a bundled MinIO (an R2/S3
stand-in), and the schema migrations:

```sh
git clone https://github.com/your-org/dropway.git && cd dropway
cp deploy/.env.example deploy/.env                  # safe local-dev defaults
docker compose -f deploy/docker-compose.yml up --build
```

| Service | URL |
|---|---|
| **Dashboard** (sign up here) | http://localhost:3000 |
| **API** (Go control-plane) | http://localhost:8080 — `GET /healthz` |
| Postgres / MinIO (bundled) | `localhost:5432` / console http://localhost:9001 |

**One time, after the first start** — Better Auth owns the identity tables, so create them:

```sh
docker compose -f deploy/docker-compose.yml exec dashboard \
  pnpm dlx @better-auth/cli@1.3.4 migrate --yes
```

Then open **http://localhost:3000**, sign up, create an org, create a site, and deploy
your first folder. Self-host is **unlimited** — no caps, no Stripe, no account needed.

### Use your own Postgres / object store

The bundled Postgres and MinIO are optional Compose profiles. To point at Supabase /
an external Postgres or Cloudflare R2 / S3, drop the profile from `COMPOSE_PROFILES`
and set the matching `DATABASE_URL` / `S3_*` vars in `deploy/.env`:

```sh
COMPOSE_PROFILES= DATABASE_URL=postgres://… S3_ENDPOINT=https://… \
  docker compose -f deploy/docker-compose.yml up --build api dashboard migrate
```

### Where your sites are served (the content domain)

Published sites are served at `<org-slug>--<app-slug>.<CONTENT_DOMAIN>` — org-namespaced,
so two orgs can both have an app named `blog` without colliding. Three vars in
`deploy/.env` control the URL the dashboard and CLI hand back:

| Var | Local default | Production |
|---|---|---|
| `CONTENT_DOMAIN` | `localhost` | your domain, e.g. `dropwaycontent.com` |
| `CONTENT_SCHEME` | `http` | `https` |
| `CONTENT_PORT` | `8090` | *(empty — standard `:443`)* |

The local defaults make every deploy a clickable **`http://<org>--<app>.localhost:8090/`** —
`*.localhost` resolves to `127.0.0.1` in every browser with **zero DNS setup**. To serve
on your own domain, point a **wildcard DNS record + TLS cert** (`*.your-domain`) at the
content server and set:

```sh
CONTENT_DOMAIN=apps.example.com   # a domain you control
CONTENT_SCHEME=https
CONTENT_PORT=                      # empty → standard :443
```

These vars affect only the *displayed* URL — `serve` matches the `Host` header and strips
the port, so the stored route stays the bare host. Full details (wildcard DNS/cert, the
Public-Suffix-List isolation rule, and the `https` requirement for gated sites) are in
**[`deploy/README.md`](deploy/README.md#serving-sites-the-content-domain)**.

### Develop without Docker

```sh
go build ./... && go test ./...     # the Go core (API + CLI)
go run ./services/api/cmd/api       # start the API
corepack enable && pnpm install     # the TS workspace
pnpm dev                            # run the dashboard
```

Full local-dev reference (build flavors, the edge Worker, migrating by hand) lives in
**[`status.md`](status.md)** and **[`deploy/README.md`](deploy/README.md)**.

---

## Open source + hosted (open-core)

Dropway follows the **Supabase / PostHog** model: a source-available codebase anyone
can self-host for free, plus an optional hosted SaaS for convenience and scale.

- The **core** is under the **[Functional Source License (FSL-1.1-Apache-2.0)](LICENSE)** —
  self-host, modify, and use it internally for free; you just can't resell it as a
  competing hosted service. Each release **becomes Apache 2.0 after two years**.
- The **`cloud/`** module (Stripe billing + usage quotas) is proprietary and **never
  ships in the self-host build**, so self-host has no limits. **`ee/`** holds
  license-gated enterprise features (SSO/SAML, audit export, custom domains).

## Docs & status

- **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)** — the full system design.
- **[`docs/diagrams/`](docs/diagrams/)** — component + sequence diagrams (sign-up,
  sign-in, deploy, gated access).
- **[`status.md`](status.md)** — build status, monorepo map, and the complete local-run
  reference.
- **[`CONTRIBUTING.md`](CONTRIBUTING.md)** — contributions welcome under a DCO sign-off.

## License

Core: **[FSL-1.1-Apache-2.0](LICENSE)** (→ Apache 2.0 after two years). `cloud/` and
`ee/` are governed by their own licenses. The "Dropway" name and logo are reserved
trademarks; forks must rename to redistribute.
