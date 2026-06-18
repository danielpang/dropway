<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Self-hosting Dropway

One command brings up the open-core data plane: **Postgres**, an **R2/S3-compatible
object store (MinIO)**, and a one-shot **goose** migration runner, entirely offline,
with **no Stripe, no quotas, and no caps** (`DROPWAY_CLOUD=false` selects the
no-op/unlimited `QuotaProvider`).

> The proprietary `cloud/` (Stripe + the 10-sites/5-members quota gate) and `ee/`
> modules are **not** part of this build. Self-host is unlimited. The FSL *license*,
> not a runtime limit, is what prevents reselling Dropway as a service.

## Quickstart (one command)

```sh
# from the repo root
cp deploy/.env.example deploy/.env        # safe local-dev defaults; rotate before exposing
docker compose -f deploy/docker-compose.yml up
```

That will:

1. Start **Postgres 16** (the single source of truth: `app` + `identity` schemas, `FORCE RLS`).
2. Start **MinIO** (S3 API on `:9000`, web console on `:9001`) and create the blob bucket.
3. Run **goose** (`migrate` service) to apply `db/migrations/app/` as the privileged
   owner role and provision the **non-superuser, non-BYPASSRLS `dropway_app`** runtime role.

When `migrate` prints `app migrations applied + dropway_app password set`, the data
plane is ready.

## What's in the box

| Service        | Purpose                                                        | Ports |
|----------------|---------------------------------------------------------------|-------|
| `api`          | Go control-plane: system of record + authz boundary           | 8080  |
| `dashboard`    | Next.js + Better Auth (control plane + OAuth authorization server) | 3000  |
| `serve`        | Go content server (`*.<CONTENT_DOMAIN>`), serves sites + `llms.txt` | 8090  |
| `mcp`          | OAuth-protected MCP server for LLM access to sites             | 8092  |
| `postgres`     | System of record; `app` schema (Go-owned) + RLS               | 5432  |
| `minio`        | R2/S3-compatible object store for blobs + deploy manifests     | 9000 (S3), 9001 (console) |
| `createbucket` | One-shot: ensures the `dropway-blobs` bucket exists            | none  |
| `migrate`      | One-shot: `goose up` of the app schema + sets runtime password | none  |

The **Go API** (`services/api`), the **dashboard** (`apps/dashboard`), the **content
server** (`services/serve`), and the **MCP server** (`services/mcp`) all slot into this
compose file behind the env contract documented in [`.env.example`](./.env.example)
(`DATABASE_URL`, the `S3_*` block, and the `JWKS_URL` / `JWT_ISSUER` / `JWT_AUDIENCE`
trio the Go verifiers use).

## Serving sites: the content domain

Published sites are served by the **content server** (`services/serve`, the
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
| `CONTENT_DOMAIN` | registrable domain sites are served under | `localhost` | e.g. `dropwaycontent.com` |
| `CONTENT_SCHEME` | scheme in the displayed live URL | `http` | `https` |
| `CONTENT_PORT`   | explicit port in the displayed live URL | `8090` | *(empty → none)* |

**Why `localhost` locally:** `*.localhost` resolves to `127.0.0.1` in every browser with
**zero DNS setup**, so a deploy's live URL is a clickable
`http://<org>--<app>.localhost:8090/` out of the box, with nothing to add to `/etc/hosts`.

**Pointing it at your own domain:** set `CONTENT_DOMAIN` to a domain you control, give it
a **wildcard DNS record + TLS cert** (`*.your-domain` → the content server), and switch
to `https`:

```sh
CONTENT_DOMAIN=apps.example.com
CONTENT_SCHEME=https
CONTENT_PORT=                  # empty → standard :443
```

For per-tenant **origin isolation** (so one tenant's JS can never reach another tenant's
cookies, or your dashboard's), serve content from a **separate registrable domain on
the [Public Suffix List](https://publicsuffix.org/)**, never a subdomain of your
dashboard.

Two things to know:

- **These vars affect only the *displayed* URL.** The stored route key
  (`app.host_routes.host`) is always the bare host; `serve` resolves by the request's
  `Host` header and strips any `:port`, so the displayed port never has to match the
  stored host.
- **Gated sites need `https`.** `org_only` / `allowlist` / `password` sites set a
  `__Host-edge` cookie, which browsers accept only over `https`. With the local `http`
  default, view a **public** site in the browser. Gated tiers need a real TLS origin
  (`CONTENT_SCHEME=https`).

## LLM access: the MCP server

The `mcp` service (`services/mcp`) is an **OAuth-protected, remote (Streamable HTTP)
[Model Context Protocol](https://modelcontextprotocol.io/) server**. It lets an
**authorized** LLM agent list and read a tenant's deployed sites (**including gated
content**) scoped to one org by the same Postgres RLS as everything else (it connects
as the non-`BYPASSRLS` `dropway_app` role via `DATABASE_URL`). Public sites are reachable
by crawlers without MCP via the `serve`/Worker-emitted `llms.txt`; gated sites are
reachable by an LLM **only** through an authorized MCP connection.

**It comes up with the stack.** `docker compose -f deploy/docker-compose.yml up` starts
`mcp` on `:8092` alongside the rest. Users connect their AI tool from the dashboard
(**Settings → LLM access (MCP) → Connect**). The first connection runs a browser OAuth
2.1 flow (Dynamic Client Registration → sign in → "Authorize MCP access") against the
dashboard, which is the authorization server. No API keys are exchanged.

**Env contract** (in [`.env.example`](./.env.example)). The audience/issuer must line up
across services or token verification fails:

| Var | What it sets | Local default | Production |
|---|---|---|---|
| `MCP_PORT` | port the server listens on | `8092` | `8092` (behind your TLS edge) |
| `MCP_PUBLIC_URL` | the server's external URL, used as the OAuth **resource/audience** | `http://localhost:8092` | `https://mcp.dropway.dev` |
| `NEXT_PUBLIC_MCP_URL` | the URL the dashboard's Connect modal displays | `http://localhost:8092` | `https://mcp.dropway.dev` |
| `API_URL` (on `mcp`) | the Go API the write tools call (forwarding the user's token) | `http://api:8080` | your internal api URL |

**Read vs write tools.** The read tools (`list_sites`, `list_files`, `read_file`,
`download_site`) run off the RLS-scoped store. The write tools (`create_site`,
`set_site_access`, `deploy_site`, which upload files + publish) call the Go API over HTTP with
the forwarded token, so they reuse the API's quota, admin re-check (`set_site_access` is
owner/admin only), deploy verification, edge-route projection, revocation, and audit. The
API stays the only projection writer. For the
API to accept the forwarded token it reads `MCP_PUBLIC_URL` and adds it to the verifier's
accepted audiences. Unset `API_URL` on the `mcp` service and it runs **read-only** (the
write tools aren't registered).

How the pieces agree (all enforced, and a mismatch is a 401):

- **`MCP_PUBLIC_URL`** is the OAuth **resource** the client requests and the **audience**
  (`aud`) the MCP server pins. The dashboard registers it in the OAuth provider's
  `validAudiences` (read from the same value), so the provider mints a **JWT** access
  token (not opaque) whose `aud` matches. Keep `MCP_PUBLIC_URL` **byte-identical** on the
  `mcp` and `dashboard` services, and make it the URL clients actually connect to.
- **`JWT_ISSUER`** is the `iss` the MCP server expects; the dashboard's `jwt()` plugin
  stamps it onto OAuth access tokens (the same value the Go API already verifies).
- **`JWKS_URL`** (compose default `http://dashboard:3000/api/auth/jwks`) is where the MCP
  server fetches the signing keys to verify tokens.
- The MCP server re-checks **`org_meta.mcp_enabled`** on every request, so an admin
  turning MCP off in Settings cuts off access immediately, even for already-issued
  tokens, independent of TTL.

**Production:** give `mcp.dropway.dev` a DNS record + TLS (front it with your edge/load
balancer terminating TLS to `:8092`), set `MCP_PUBLIC_URL=https://mcp.dropway.dev` and
`NEXT_PUBLIC_MCP_URL=https://mcp.dropway.dev`, and point `MCP_DASHBOARD_URL` at your
dashboard origin (the browser-facing authorization server).

## Two database roles (why there are two URLs)

Migrations and runtime use **different roles**, on purpose:

- **`DATABASE_OWNER_URL`** is the privileged **owner/admin** role. Used **only** by the
  `migrate` step to create schemas, tables, policies, and the runtime role.
- **`DATABASE_URL`** is the **`dropway_app`** runtime role: non-superuser,
  **non-BYPASSRLS**. Every tenant table is `FORCE ROW LEVEL SECURITY`, so this
  connection is fully subject to the per-tenant policies. The Go API runs
  `SET LOCAL app.current_org_id = …` per transaction, so rows from other orgs are
  invisible. Keep the password in `DATABASE_URL` in sync with
  `DROPWAY_APP_DB_PASSWORD`.

## Running migrations by hand (without compose)

```sh
go install github.com/pressly/goose/v3/cmd/goose@latest
export PATH="$PATH:$(go env GOPATH)/bin"

# app schema (open-core)
goose -dir db/migrations/app postgres \
  "postgres://postgres:postgres@localhost:5432/dropway?sslmode=disable" up

# set the runtime role's password (owner connection)
psql "postgres://postgres:postgres@localhost:5432/dropway?sslmode=disable" \
  -c "ALTER ROLE dropway_app WITH PASSWORD 'dropway-app-dev-secret';"
```

The cloud-only `db/migrations/billing/` is **never** applied to a self-host database.

## Tearing down

```sh
docker compose -f deploy/docker-compose.yml down          # stop
docker compose -f deploy/docker-compose.yml down -v        # stop + wipe pgdata/miniodata volumes
```

## Production self-host notes

- **Rotate every secret** in `.env`. The defaults are dev-only.
- Point `JWKS_URL` / `JWT_ISSUER` / `JWT_AUDIENCE` at your real dashboard and API origins.
- Swap the local MinIO `S3_*` values for managed object storage if you prefer.
- Back up the `pgdata` volume; the KV/edge routing projection is fully rebuildable
  from Postgres, so Postgres is the only stateful thing you must protect.
- **Dev projection writer is in-memory.** When no Cloudflare KV creds are set, the API
  uses the local (dev) projection writer. Its route map can be mirrored to
  `PROJECTION_FILE`, but the **denylist (`revoked:*`) and org-status (`org_status:*`)
  projections are kept ONLY in memory and are NOT persisted across restarts**. After
  a restart they start empty. This is acceptable by design: both fail **closed** (a
  missing denylist entry just forces an extra re-auth, and a missing org-status is
  re-derived on the next billing webhook) and are fully **rebuildable from Postgres**,
  so a restart never opens access or loses durable state. Production uses Cloudflare
  KV, which is durable.
