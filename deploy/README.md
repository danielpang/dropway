<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Self-hosting Shipped

One command brings up the open-core data plane — **Postgres**, an **R2/S3-compatible
object store (MinIO)**, and a one-shot **goose** migration runner — entirely offline,
with **no Stripe, no quotas, and no caps** (`SHIPPED_CLOUD=false` → the
no-op/unlimited `QuotaProvider`). See [`docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)
§13 (row 13) and §14 for the full self-host story.

> The proprietary `cloud/` (Stripe + the 10-sites/5-members quota gate) and `ee/`
> modules are **not** part of this build. Self-host is unlimited; the FSL *license*,
> not a runtime limit, is what prevents reselling Shipped as a service.

## Quickstart (one command)

```sh
# from the repo root
cp deploy/.env.example deploy/.env        # safe local-dev defaults; rotate before exposing
docker compose -f deploy/docker-compose.yml up
```

That will:

1. Start **Postgres 16** (the single source of truth — `app` + `auth` schemas, `FORCE RLS`).
2. Start **MinIO** (S3 API on `:9000`, web console on `:9001`) and create the blob bucket.
3. Run **goose** (`migrate` service) to apply `db/migrations/app/` as the privileged
   owner role and provision the **non-superuser, non-BYPASSRLS `shipped_app`** runtime role.

When `migrate` prints `app migrations applied + shipped_app password set`, the data
plane is ready.

## What's in the box

| Service        | Purpose                                                        | Ports |
|----------------|---------------------------------------------------------------|-------|
| `postgres`     | System of record; `app` schema (Go-owned) + RLS               | 5432  |
| `minio`        | R2/S3-compatible object store for blobs + deploy manifests     | 9000 (S3), 9001 (console) |
| `createbucket` | One-shot: ensures the `shipped-blobs` bucket exists            | —     |
| `migrate`      | One-shot: `goose up` of the app schema + sets runtime password | —     |

The **Go API** (`services/api`) and the **dashboard** (`apps/dashboard`) are owned by
other parts of the repo; once present they slot into this compose file behind the same
env contract documented in [`.env.example`](./.env.example) (`DATABASE_URL`, the
`S3_*` block, and the `JWKS_URL` / `JWT_ISSUER` / `JWT_AUDIENCE` trio the Go verifier
uses).

## Serving sites: the content domain

Published sites are served by the **content server** (`services/serve` — the
Cloudflare-free serving plane) at an **org-namespaced host**:

```
<org-slug>--<app-slug>.<CONTENT_DOMAIN>
```

Putting the org slug in the host keeps the global route namespace unambiguous: two
orgs can both deploy an app named `blog` and each still gets its own origin. The double
dash (`--`) keeps it a **single DNS label**, so one wildcard cert (`*.<CONTENT_DOMAIN>`)
covers every site.

Three vars in [`.env`](./.env.example) decide the URL the dashboard and CLI hand back:

| Var | What it sets | Local default | Production |
|---|---|---|---|
| `CONTENT_DOMAIN` | registrable domain sites are served under | `localhost` | e.g. `shippedusercontent.com` |
| `CONTENT_SCHEME` | scheme in the displayed live URL | `http` | `https` |
| `CONTENT_PORT`   | explicit port in the displayed live URL | `8090` | *(empty → none)* |

**Why `localhost` locally:** `*.localhost` resolves to `127.0.0.1` in every browser with
**zero DNS setup**, so a deploy's live URL is a clickable
`http://<org>--<app>.localhost:8090/` out of the box — nothing to add to `/etc/hosts`.

**Pointing it at your own domain:** set `CONTENT_DOMAIN` to a domain you control, give it
a **wildcard DNS record + TLS cert** (`*.your-domain` → the content server), and switch
to `https`:

```sh
CONTENT_DOMAIN=apps.example.com
CONTENT_SCHEME=https
CONTENT_PORT=                  # empty → standard :443
```

For per-tenant **origin isolation** (so one tenant's JS can never reach another tenant's
— or your dashboard's — cookies), serve content from a **separate registrable domain on
the [Public Suffix List](https://publicsuffix.org/)**, never a subdomain of your
dashboard. See [`docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) §3.

Two things to know:

- **These vars affect only the *displayed* URL.** The stored route key
  (`app.host_routes.host`) is always the bare host; `serve` resolves by the request's
  `Host` header and strips any `:port`, so the displayed port never has to match the
  stored host.
- **Gated sites need `https`.** `org_only` / `allowlist` / `password` sites set a
  `__Host-edge` cookie, which browsers accept only over `https`. With the local `http`
  default, view a **public** site in the browser; gated tiers need a real TLS origin
  (`CONTENT_SCHEME=https`).

## Two database roles (why there are two URLs)

Migrations and runtime use **different roles**, on purpose (ARCHITECTURE.md §5):

- **`DATABASE_OWNER_URL`** — the privileged **owner/admin** role. Used **only** by the
  `migrate` step to create schemas, tables, policies, and the runtime role.
- **`DATABASE_URL`** — the **`shipped_app`** runtime role: non-superuser,
  **non-BYPASSRLS**. Every tenant table is `FORCE ROW LEVEL SECURITY`, so this
  connection is fully subject to the per-tenant policies. The Go API runs
  `SET LOCAL app.current_org_id = …` per transaction; rows from other orgs are
  invisible. Keep the password in `DATABASE_URL` in sync with
  `SHIPPED_APP_DB_PASSWORD`.

## Running migrations by hand (without compose)

```sh
go install github.com/pressly/goose/v3/cmd/goose@latest
export PATH="$PATH:$(go env GOPATH)/bin"

# app schema (open-core)
goose -dir db/migrations/app postgres \
  "postgres://postgres:postgres@localhost:5432/shipped?sslmode=disable" up

# set the runtime role's password (owner connection)
psql "postgres://postgres:postgres@localhost:5432/shipped?sslmode=disable" \
  -c "ALTER ROLE shipped_app WITH PASSWORD 'shipped-app-dev-secret';"
```

The cloud-only `db/migrations/billing/` is **never** applied to a self-host database.

## Tearing down

```sh
docker compose -f deploy/docker-compose.yml down          # stop
docker compose -f deploy/docker-compose.yml down -v        # stop + wipe pgdata/miniodata volumes
```

## Production self-host notes

- **Rotate every secret** in `.env` — the defaults are dev-only.
- Point `JWKS_URL` / `JWT_ISSUER` / `JWT_AUDIENCE` at your real dashboard and API origins.
- Swap the local MinIO `S3_*` values for managed object storage if you prefer.
- Back up the `pgdata` volume; the KV/edge routing projection is fully rebuildable
  from Postgres, so Postgres is the only stateful thing you must protect.
- **Dev projection writer is in-memory.** When no Cloudflare KV creds are set, the API
  uses the local (dev) projection writer. Its route map can be mirrored to
  `PROJECTION_FILE`, but the **denylist (`revoked:*`) and org-status (`org_status:*`)
  projections are kept ONLY in memory and are NOT persisted across restarts** — after
  a restart they start empty. This is acceptable by design: both fail **closed** (a
  missing denylist entry just forces an extra re-auth; a missing org-status is
  re-derived on the next billing webhook) and are fully **rebuildable from Postgres**,
  so a restart never opens access or loses durable state. Production uses Cloudflare
  KV, which is durable.
