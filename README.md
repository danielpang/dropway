<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Shipped

**A folder of files → a live, access-controlled URL in one command.**

Shipped is a multi-tenant, Quick-style static-site share platform. Drop a folder
(or run `shipped deploy ./dist`) and get an immutable, versioned, access-controlled
URL — no pipeline, no config. It takes Shopify [Quick](https://shopify.engineering/quick)'s
ergonomic promise external and multi-tenant: per-site ownership, three sharing tiers
(public / password / allowlist / org-only), and orgs/enterprise as first-class tenants,
serving untrusted tenant HTML/JS safely from a separate Public-Suffix-List domain.

> **Read the full design first:** [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) is the
> approved architecture — domains, data model + RLS, edge auth, deploy flow, billing,
> phased roadmap, and the open-core licensing model.

## Open-core model

Shipped follows the Supabase / PostHog open-core playbook: **open source + a hosted SaaS.**

- **Core** (`apps/`, `services/`, `cli/`, `internal/`, `edge/`, `contracts/`, `db/migrations/app/`,
  `deploy/`, `packages/`) is **source-available under [FSL-1.1-Apache-2.0](LICENSE)**
  (Sentry's Functional Source License). You may self-host, modify, and use Shipped
  internally for free — but you **may not** offer it to third parties as a competing
  paid/hosted service. Each release **auto-converts to Apache 2.0 after two years**.
- **`cloud/`** is **proprietary and cloud-only** — the Stripe billing integration and the
  quota gate (the only place the Free 5-members/10-sites caps exist). It is **never shipped
  in the self-host build**, so self-host is **unlimited**. The *license*, not a runtime
  limit, is what prevents reselling.
- **`ee/`** holds **Shipped Enterprise Edition** features (SSO/SAML, audit export, advanced
  RBAC, custom domains) under a separate, license-key-gated EE license.

The OSS core depends on `cloud/` / `ee/` **only through interfaces with no-op/unlimited
default implementations** (e.g. the Go `QuotaProvider`). CI proves the OSS build has
**zero references** into `cloud/` or `ee/` — see the `open-core-boundary` job in
[`.github/workflows/ci.yml`](.github/workflows/ci.yml).

## Status — what's built, what's deferred

Phases 0–4 are in the tree. **Phase 4 shipped the security/ops hardening:** audit logging
into `app.audit_log` (correlated by `request_id`); **hard revocation** via a Cloudflare-KV
denylist (`revoked:user|site|org` → `{min_iat}`, written by the Go API on the three
token-revocation triggers — member removal / site unshare / access-tighten /
`allow_external_sharing` disable — idempotent and rebuildable, checked by both the serving
Worker and the `/authz` exchange — fail-closed); **billing suspension / over_limit** sets the
per-org **`org_status` KV flag** instead (the edge serves a read-only platform block page —
*not* a token revocation, so existing viewers aren't hard-cut, per the §9 read-only model);
edge rate-limiting + denial-of-wallet caps; content security headers; an **RLS policy test
suite**; **R2 version GC** (with an age guard so an in-flight deploy's just-uploaded blobs are
never reaped); and the **DR rebuild** path (`store.RebuildProjection` re-derives the KV/D1
projection from Postgres).

**Intentionally NOT yet built** (post-launch / enterprise — each needs external accounts,
paid runtime infra, or a vendor relationship; full rationale in
[`docs/ARCHITECTURE.md` §15](docs/ARCHITECTURE.md)):

- **SSO/SAML** (Better Auth SSO plugin, UUID-keyed) and **SCIM** provisioning — `ee/`.
- **Runtime APIs** — collection DB + realtime, file uploads, the **LLM/image proxy** with the
  §10 denial-of-wallet guardrails, and websockets / Durable Objects / Workers-for-Platforms
  dynamic runtime.
- **Third-party malware / abuse scanning vendor** + automated takedown / quarantine.
- **Per-site configurable CSP UI** (Phase 4 ships a fixed sane default).
- **Usage-based runtime billing** (depends on the runtime APIs landing first).
- **Full OpenTelemetry tracing backend** — Phase 4 ships structured logs with a correlated
  `request_id`; exporting OTel spans to a collector/backend is deferred.

## Monorepo map

```
shipped/
├── apps/
│   └── dashboard/        [FSL] Next.js (app.shipped.app): Better Auth (Google/email/magic
│                               + Organization + JWT/EdDSA), /authz exchange (P2). Calls the
│                               Go API via a generated OpenAPI client — never touches Postgres.
├── services/
│   └── api/              [FSL] Go (api.shipped.app) = system of record + authz boundary.
│                               Verifies EdDSA JWTs, orchestrates deploys, writes the KV/D1
│                               projection. cloud/ wired in via build tags / DI.
├── edge/
│   └── serving-worker/   [FSL] Cloudflare Worker (*.shippedusercontent.com): R2 + KV + D1.
│                               Public path = JWT-free cacheable router; gated tiers in P2.
├── cli/                  [FSL] Go `shipped` binary — folder → live URL, shares deploy code.
├── contracts/            [FSL] @shipped/contracts — the one cross-language data contract:
│                               the KV route value shape (JSON Schema → Go + TS, round-trip test).
├── internal/             [FSL] Shared Go libs: auth (EdDSA JWT verify), quota (Provider iface),
│                               httpx, middleware.
├── packages/             [FSL] Shared TS workspace config: tsconfig, eslint-config.
├── db/
│   ├── migrations/app/      [FSL] Go-owned `app` schema — goose migrations + hand-written
│   │                              RLS / GRANT / external-sharing trigger.
│   └── migrations/billing/  [proprietary, cloud-only] subscriptions + Stripe-event dedupe.
├── deploy/               [FSL] docker-compose + .env.example + one-command self-host guide.
├── cloud/               [PROPRIETARY, cloud-only] billing (Stripe) + quota (the hard caps).
├── ee/                  [EE LICENSE] enterprise features.
├── docs/ARCHITECTURE.md       The approved architecture.
└── .github/workflows/ci.yml   go · open-core-boundary · sql (RLS) · web typecheck.
```

**Schema ownership** (ARCHITECTURE.md §5/§8): the **`auth`** schema is owned and migrated by
Better Auth (the dashboard); the **`app`** schema is Go-owned via goose; the **`billing`**
schema is cloud-only and FK's into `app` (the core never references billing). Every tenant
table carries a denormalized `org_id` and is under `FORCE ROW LEVEL SECURITY`; the Go API
connects as a **non-BYPASSRLS `shipped_app`** role and sets `app.current_org_id` per
transaction.

## Dev quickstart

### 1. Data plane (Postgres + object store + migrations)

```sh
cp deploy/.env.example deploy/.env        # safe local-dev defaults
docker compose -f deploy/docker-compose.yml up
```

This boots Postgres 16, MinIO (an R2/S3-compatible store), and runs the goose app
migrations — including the non-BYPASSRLS `shipped_app` runtime role. Full walkthrough:
[`deploy/README.md`](deploy/README.md).

To run migrations by hand:

```sh
go install github.com/pressly/goose/v3/cmd/goose@latest
goose -dir db/migrations/app postgres \
  "postgres://postgres:postgres@localhost:5432/shipped?sslmode=disable" up
```

### 2. Go API + CLI

```sh
go build ./...                 # build the core
go test ./...                  # run tests
go run ./services/api/cmd/api  # start the API (reads DATABASE_URL etc. from env)
go run ./cli/cmd/shipped       # the deploy CLI
```

### 3. Dashboard + TS workspace

```sh
corepack enable
pnpm install
pnpm -r typecheck              # type-check all workspace packages
pnpm dev                       # run the dashboard (apps/dashboard)
```

### Build flavors

- **OSS / self-host (default):** `go build ./...` — unlimited, no Stripe, no caps.
- **Cloud (internal):** `go build -tags cloud ./...` — wires in `cloud/quota` + `cloud/billing`.

## Contributing

Contributions to the FSL core are welcome under a lightweight **DCO sign-off** — see
[`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

The core is licensed under [FSL-1.1-Apache-2.0](LICENSE). `cloud/` and `ee/` are governed
by their own licenses (see [`cloud/LICENSE`](cloud/LICENSE) and [`ee/LICENSE`](ee/LICENSE)).
The "Shipped" name and logo are reserved trademarks; forks must rename to redistribute.
