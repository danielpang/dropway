# Dropway deployment guide (Vercel + Fly.io + Cloudflare)

How to deploy every Dropway component to production with:

- **Vercel** for the dashboard (Next.js + Better Auth)
- **Fly.io** for the Go API and the MCP server
- **Cloudflare** for the serving Worker and R2 object storage (plus Workers KV)
- A **managed Postgres** (Fly Postgres, Supabase, or Neon) for the database, since none of the three platforms above provide it

> Scope note: the `services/serve` content server, `deploy/docker-compose.yml`, Caddy, and Mailpit are the **self-host** path. They are NOT used in this setup; the Cloudflare Worker replaces `serve`, and a real SMTP provider replaces Mailpit.

---

## 1. Component map

| Component | Source | Target | Listens on |
|---|---|---|---|
| Dashboard (Next.js + Better Auth) | `apps/dashboard` | Vercel | 3000 (Vercel-managed) |
| Go API (control plane) | `services/api` | Fly.io app `dropway-api` | `PORT` 8080 |
| MCP server | `services/mcp` | Fly.io app `dropway-mcp` | `MCP_PORT` 8092 |
| Serving Worker (content) | `edge/serving-worker` | Cloudflare Workers | edge |
| Object storage (blobs + manifests) | n/a | Cloudflare R2 bucket | — |
| Route projection (`route:<host>`) | n/a | Cloudflare Workers KV | — |
| Database (app + identity schemas, RLS) | `db/migrations/app` | managed Postgres | 5432 |
| Transactional email | n/a | SMTP provider (Resend / Postmark / SES) | — |

**Data flow:** the Go API is the sole writer of R2 (content blobs + manifests) and of the KV route projection. The Worker is read-only against both. The browser uploads blobs **directly** to R2 via API-signed presigned URLs.

---

## 2. Pick your domains first

Everything is wired by URL, so decide these up front (examples used throughout):

| Purpose | Example | Hosted by |
|---|---|---|
| Dashboard | `https://app.dropway.dev` | Vercel |
| Go API | `https://api.dropway.dev` | Fly.io |
| MCP server | `https://mcp.dropway.dev` | Fly.io |
| Served sites (wildcard) | `https://*.dropwaycontent.com` | Cloudflare Worker |

The content domain (`dropwaycontent.com`) must be a registrable, public-suffix domain on a Cloudflare zone so the Worker can answer `*.dropwaycontent.com` with a wildcard cert. Sites are served at `<orgSlug>--<siteSlug>.dropwaycontent.com`.

### Generate the two long-lived secrets

```sh
# → BETTER_AUTH_SECRET  (dashboard / Vercel, §7) — Better Auth signing/encryption secret
openssl rand -hex 32
# → EDGE_SIGNING_KEY    (Go API / Fly, §5) — Ed25519 edge-token seed.
#   MUST be stable across restarts/instances, or minted gated tokens break.
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

| Output of | Set as env var | On service |
|---|---|---|
| `openssl rand -hex 32` | `BETTER_AUTH_SECRET` | dashboard (Vercel) |
| `openssl rand -base64 32 \| tr ...` | `EDGE_SIGNING_KEY` | Go API (Fly) |

Tools you'll need locally:

- `flyctl`, `vercel` (install per their docs)
- `goose` — `go install github.com/pressly/goose/v3/cmd/goose@v3.22.0` (the version the repo pins). It lands in `$(go env GOPATH)/bin`, so add that to your PATH.
- `psql` — e.g. `brew install libpq` (keg-only; add `/opt/homebrew/opt/libpq/bin` to PATH). Optional: the one DB step that needs it can be run in the Supabase SQL editor instead.
- `wrangler` — **already a pinned devDependency** of the worker workspace; run `pnpm install` once, then invoke it via the workspace (no global install): `pnpm --filter @dropway/serving-worker exec wrangler ...` (or `cd edge/serving-worker && pnpm exec wrangler ...`).

---

## 3. Postgres (do this first)

The API and MCP connect at runtime as a **non-superuser, non-BYPASSRLS** role (`dropway_app`) because every tenant table is FORCE RLS. You need two connection strings:

- **Owner/privileged** — applies migrations and owns the Better Auth `identity` schema.
- **Runtime `dropway_app`** — used by the API and MCP.

Steps:

1. Provision a managed Postgres 16 (Fly Postgres, Supabase, or Neon).
2. Apply the app migrations as the owner. This creates the `app` schema, RLS policies, and the `dropway_app` role:
   ```sh
   goose -dir db/migrations/app postgres "$OWNER_DSN" up
   ```
3. Set the runtime role's password and capture its DSN:
   ```sh
   psql "$OWNER_DSN" -c "ALTER ROLE dropway_app WITH PASSWORD 'a-strong-password';"
   # DATABASE_URL = postgres://dropway_app:a-strong-password@HOST:5432/dropway?sslmode=require
   ```
4. The Better Auth `identity` schema is migrated later in **step 7** (the dashboard step), with the **owner** DSN — see "One-time Better Auth migration" there.

> Serverless note: the dashboard runs on Vercel (serverless), so point its Better Auth DB at a **pooled** connection (Supabase pooler, Neon pooled endpoint, or PgBouncer) to avoid exhausting connections. Fly services can use the direct/primary DSN.

---

## 4. Cloudflare R2 + KV

> Run `wrangler` from the worker workspace: `cd edge/serving-worker && pnpm exec wrangler ...` (it's the pinned devDependency, no global install). The commands below assume you're in `edge/serving-worker/`.

1. **R2 bucket** for content (one private bucket holds everything, content-addressed):
   ```sh
   pnpm exec wrangler r2 bucket create dropway-content
   # Optional, only if you'll run `wrangler dev` locally (the preview bucket):
   pnpm exec wrangler r2 bucket create dropway-content-development
   ```
   Keys the API writes: `blobs/<org_id>/<sha256>` and `manifests/<org_id>/<site_id>/<version_id>.json`.
2. **R2 S3 credentials.** The Go API talks to R2 over the S3 API, so it needs an
   access key + secret. In the Cloudflare dashboard: **R2 → Overview → "Manage R2 API
   Tokens" → Create API token**, scope it **Object Read & Write** on the
   `dropway-content` bucket, and Create. Cloudflare shows three values **once**:
   - **Access Key ID** → `S3_ACCESS_KEY_ID`
   - **Secret Access Key** → `S3_SECRET_ACCESS_KEY` (copy it now; it's not shown again)
   - the **S3 endpoint** `https://<ACCOUNT_ID>.r2.cloudflarestorage.com` (the `<ACCOUNT_ID>` is also on the R2 page)

   The rest are fixed/derived:
   - `S3_ENDPOINT = https://<ACCOUNT_ID>.r2.cloudflarestorage.com`
   - `S3_REGION = auto`, `S3_FORCE_PATH_STYLE = true`, `S3_BUCKET = dropway-content`
   - `S3_PUBLIC_ENDPOINT = https://<ACCOUNT_ID>.r2.cloudflarestorage.com` (the host the **browser** PUTs presigned uploads to)

   > This R2 S3 token is **separate** from the `CF_API_TOKEN` created in step 5 of this section (that one is for Workers KV, not R2).
3. **R2 bucket CORS**: allow the dashboard origin to `PUT` (drag-and-drop deploy uploads go browser → R2 directly):
   ```json
   [{ "AllowedOrigins": ["https://app.dropway.dev"],
      "AllowedMethods": ["PUT"],
      "AllowedHeaders": ["*"] }]
   ```
4. **Workers KV namespace** for the route projection:
   ```sh
   pnpm exec wrangler kv namespace create ROUTES
   pnpm exec wrangler kv namespace create ROUTES --preview   # only for `wrangler dev`
   # (optional, recommended) a separate namespace for rate-limit counters + org status:
   pnpm exec wrangler kv namespace create LIMITS
   ```
5. **Cloudflare API token** for the Go API to write KV + (optionally) manage custom hostnames. Needs: *Workers KV Storage: Edit* (and *SSL and Certificates: Edit* + zone access only if you use the Cloudflare-for-SaaS custom-domain feature). Capture:
   - `CF_ACCOUNT_ID`, `CF_KV_NAMESPACE_ID` (the ROUTES namespace id), `CF_API_TOKEN`
   - `CF_ZONE_ID` only if enabling custom domains.

---

## 5. Go API on Fly.io (`services/api`)

The repo already has `services/api/Dockerfile` (static binary, `EXPOSE 8080`). Create a Fly app that builds it.

`fly.toml` (at repo root, or pass `--config`):

```toml
app = "dropway-api"
primary_region = "iad"

[build]
  dockerfile = "services/api/Dockerfile"
  # For the hosted/cloud build (Stripe billing), uncomment:
  # [build.args]
  #   DROPWAY_BUILD_TAGS = "cloud"

[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = false
  min_machines_running = 1

[[http_service.checks]]
  method = "GET"
  path = "/healthz"
  interval = "15s"
  timeout = "2s"
```

Set secrets (these are environment variables; use `fly secrets set`):

```sh
fly secrets set \
  DATABASE_URL='postgres://dropway_app:...@HOST:5432/dropway?sslmode=require' \
  S3_ENDPOINT='https://<ACCOUNT_ID>.r2.cloudflarestorage.com' \
  S3_REGION='auto' S3_FORCE_PATH_STYLE='true' \
  S3_BUCKET='dropway-content' \
  S3_PUBLIC_ENDPOINT='https://<ACCOUNT_ID>.r2.cloudflarestorage.com' \
  S3_ACCESS_KEY_ID='<r2 access key>' S3_SECRET_ACCESS_KEY='<r2 secret>' \
  CF_ACCOUNT_ID='<account id>' CF_KV_NAMESPACE_ID='<ROUTES ns id>' CF_API_TOKEN='<cf token>' \
  EDGE_SIGNING_KEY='<base64url ed25519 seed from step 2>' \
  JWKS_URL='https://app.dropway.dev/api/auth/jwks' \
  JWT_ISSUER='https://app.dropway.dev' \
  JWT_AUDIENCE='https://api.dropway.dev' \
  MCP_PUBLIC_URL='https://mcp.dropway.dev' \
  CONTENT_DOMAIN='dropwaycontent.com' CONTENT_SCHEME='https' CONTENT_PORT='' \
  DASHBOARD_URL='https://app.dropway.dev' \
  --app dropway-api
```

`S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` are the R2 token values from §4 step 2; `CF_API_TOKEN` is the Workers-KV token from §4 step 5.

**`MCP_PUBLIC_URL` on the API is REQUIRED for the MCP write tools** (`create_site`, `set_site_access`, `deploy_site`). Those tools run on the MCP server but mutate through this API, forwarding the user's OAuth token — whose audience is the MCP resource (`MCP_PUBLIC_URL`), not `JWT_AUDIENCE`. The API only accepts that forwarded audience when `MCP_PUBLIC_URL` is set (it adds it, and its trailing-slash form, to the verifier's allowed audiences); leave it unset and the read tools still work but every MCP write returns **401**. Use the **same** value as the MCP server (§6) and the dashboard (§7) — byte-identical, no trailing slash.

**Custom domains (optional):** if you're enabling the per-site Cloudflare-for-SaaS feature, also set the zone id (otherwise skip it — the dashboard hides that UI when it's absent):

```sh
fly secrets set CF_ZONE_ID='<dropwaycontent.com zone id>' --app dropway-api
```

Deploy, then attach the hostname:

```sh
fly deploy --app dropway-api
fly certs add api.dropway.dev --app dropway-api    # then point DNS (§9)
```

> Optional cloud/billing build: build with `DROPWAY_BUILD_TAGS=cloud` and also set `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_BUSINESS`, `STRIPE_PRICE_ENTERPRISE`, and `ENFORCE_STORAGE_QUOTA` (default off). The OSS build ignores all of these and ships unlimited.

---

## 6. MCP server on Fly.io (`services/mcp`)

Second Fly app (`dropway-mcp`). Its committed manifest is **`fly.mcp.toml`** at the
repo root (build context is the repo root, like the API — one Go module spans the
whole repo).

Secrets — the MCP server reads tenant sites from the RLS-scoped DB **and content
blobs from the same R2 bucket as the API**, verifies OAuth JWTs against the dashboard
JWKS, and advertises the dashboard as the OAuth authorization server. It **fails to
boot** unless `DATABASE_URL`, `JWKS_URL`, `MCP_PUBLIC_URL`, `DASHBOARD_URL`, and the
`S3_*` (at least `S3_BUCKET`) are all present:

```sh
fly secrets set \
  DATABASE_URL='postgres://dropway_app:...@HOST:5432/dropway?sslmode=require' \
  JWKS_URL='https://app.dropway.dev/api/auth/jwks' \
  JWT_ISSUER='https://app.dropway.dev' \
  MCP_PUBLIC_URL='https://mcp.dropway.dev' \
  DASHBOARD_URL='https://app.dropway.dev' \
  S3_ENDPOINT='https://<ACCOUNT_ID>.r2.cloudflarestorage.com' \
  S3_REGION='auto' S3_FORCE_PATH_STYLE='true' \
  S3_BUCKET='dropway-content' \
  S3_ACCESS_KEY_ID='<r2 access key>' S3_SECRET_ACCESS_KEY='<r2 secret>' \
  API_URL='https://api.dropway.dev' \
  --app dropway-mcp

# Deploy from the repo root (the build context):
fly deploy --config fly.mcp.toml --app dropway-mcp
fly certs add mcp.dropway.dev --app dropway-mcp
```

- `DASHBOARD_URL` = the OAuth authorization server (the dashboard) — **required** (the server `mustEnv`s it).
- `S3_*` = the **same R2 credentials as the API** (§4 step 2); the MCP reads deploy manifests/blobs to serve site content to the LLM. Required to boot.
- `API_URL` (optional) enables the `create_site` / `set_site_access` write tools; omit it and the MCP server runs read-only.
- `MCP_PORT` defaults to `8092` (matches the `fly.mcp.toml` `internal_port`), so you don't need to set it.

`MCP_PUBLIC_URL` is the OAuth `resource`/audience and must be **byte-identical** to the value the dashboard registers (step 7, `NEXT_PUBLIC_MCP_URL`/`MCP_PUBLIC_URL`).

---

## 7. Dashboard on Vercel (`apps/dashboard`)

This is a pnpm monorepo. In the Vercel project settings:

- **Root Directory:** `apps/dashboard`
- **Framework preset:** Next.js
- **Install command:** `pnpm install` (run from the repo root so workspace deps resolve; enable "Include files outside the root directory" / corepack pnpm)
- Node.js runtime (Better Auth needs Node, not Edge).

Environment variables (Production):

```sh
# Better Auth
BETTER_AUTH_SECRET=<openssl rand -hex 32 from step 2>
BETTER_AUTH_URL=https://app.dropway.dev
NEXT_PUBLIC_BETTER_AUTH_URL=https://app.dropway.dev      # browser auth client reads this
BETTER_AUTH_DATABASE_URL=postgres://<owner-or-pooled>@HOST:5432/dropway?sslmode=require

# Where the dashboard reaches the Go API (server actions AND browser)
API_URL=https://api.dropway.dev
NEXT_PUBLIC_API_URL=https://api.dropway.dev

# Token claims the API/MCP expect (keep consistent across services)
JWT_ISSUER=https://app.dropway.dev
JWT_AUDIENCE=https://api.dropway.dev

# MCP discovery shown in the "Connect an AI tool" modal + registered as an audience
NEXT_PUBLIC_MCP_URL=https://mcp.dropway.dev
MCP_PUBLIC_URL=https://mcp.dropway.dev

# Email (REQUIRED for an internet-facing deploy; turn verification on)
REQUIRE_EMAIL_VERIFICATION=true
MAIL_SMTP_URL=smtp://USER:PASS@smtp.your-provider.com:587
MAIL_FROM=Dropway <no-reply@dropway.dev>

# Google sign-in (optional)
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
```

**One-time Better Auth migration** (creates the `identity` schema/tables). Run it once against the owner DB before first sign-in, from a machine with repo + env:

```sh
cd apps/dashboard
BETTER_AUTH_DATABASE_URL="$OWNER_DSN" BETTER_AUTH_SECRET=... \
  pnpm dlx @better-auth/cli@latest migrate --yes
```

Deploy via the Vercel dashboard or `vercel --prod`, then add `app.dropway.dev` as a Vercel domain.

---

## 8. Serving Worker on Cloudflare (`edge/serving-worker`)

Edit `edge/serving-worker/wrangler.toml`:

- `[[kv_namespaces]] binding = "ROUTES"` → set `id` (and `preview_id`) to the ROUTES namespace from step 4.
- `[[r2_buckets]] binding = "BUCKET"` → `bucket_name = "dropway-content"` (already set).
- `[vars]`:
  - `EDGE_JWKS_URL = "https://api.dropway.dev/.well-known/edge-jwks"`
  - `APP_AUTHZ_URL = "https://app.dropway.dev/authz"`
- (optional) uncomment the `LIMITS` KV binding and set its id to enable edge rate limiting + the per-org suspension flag.
- Uncomment the production route:
  ```toml
  [[routes]]
  pattern = "*.dropwaycontent.com/*"
  zone_name = "dropwaycontent.com"
  ```

Deploy:

```sh
cd edge/serving-worker
pnpm exec wrangler deploy        # or: pnpm --filter @dropway/serving-worker deploy
```

The Worker holds no secrets: it reads R2 + KV through bindings and verifies edge tokens against the API's public JWKS.

---

## 9. DNS

| Record | Points to |
|---|---|
| `app.dropway.dev` | Vercel (CNAME from the Vercel domain settings) |
| `api.dropway.dev` | Fly app `dropway-api` (`fly certs` shows the target) |
| `mcp.dropway.dev` | Fly app `dropway-mcp` |
| `*.dropwaycontent.com` | handled by the Worker route on the Cloudflare zone (no record needed beyond the zone being on Cloudflare; add a proxied wildcard/`A`+`AAAA` placeholder if your setup requires one) |

---

## 10. Cross-service wiring (the part that's easy to get wrong)

| What | Set on | Value |
|---|---|---|
| User-JWT verification | API, MCP | `JWKS_URL=https://app.dropway.dev/api/auth/jwks` |
| Token issuer/audience | dashboard, API (and `JWT_ISSUER` on MCP) | `JWT_ISSUER=https://app.dropway.dev`, `JWT_AUDIENCE=https://api.dropway.dev` |
| Edge-token verification | Worker | `EDGE_JWKS_URL=https://api.dropway.dev/.well-known/edge-jwks` |
| Edge-token signing | API | `EDGE_SIGNING_KEY` (stable Ed25519 seed) |
| Gated-site redirect | Worker | `APP_AUTHZ_URL=https://app.dropway.dev/authz` |
| Route projection | API writes via `CF_*`; Worker reads via `ROUTES` binding | same KV namespace |
| Content storage | API writes via `S3_*`; Worker reads via `BUCKET` binding | same R2 bucket |
| MCP OAuth resource | MCP (`MCP_PUBLIC_URL`), dashboard (`MCP_PUBLIC_URL`/`NEXT_PUBLIC_MCP_URL`), **and API (`MCP_PUBLIC_URL`, for the MCP write-tool token bridge)** | `https://mcp.dropway.dev` (identical across all three) |
| Dashboard ↔ API | dashboard (`API_URL`/`NEXT_PUBLIC_API_URL`) | `https://api.dropway.dev` |
| OAuth discovery / upgrade links | API | `DASHBOARD_URL=https://app.dropway.dev` |

---

## 11. Smoke test (in order)

1. `curl https://api.dropway.dev/healthz` → `{"status":"ok"}`.
2. `curl https://api.dropway.dev/.well-known/edge-jwks` → a JWKS with an OKP/Ed25519 key.
3. Sign up on `https://app.dropway.dev`, create an org, create a site.
4. Deploy a folder (dashboard drag-and-drop, or the CLI: `dropway login --api https://api.dropway.dev` then `dropway deploy ./dist --new --site demo --send`).
5. Visit `https://<org>--demo.dropwaycontent.com` → the Worker serves it from R2.
6. Set the site to org-only and confirm an unauthenticated visit 302s to `/authz`, and an authorized one is served.
7. Connect the MCP server (`https://mcp.dropway.dev/mcp`) from an MCP client and confirm the OAuth flow, then exercise **both** a read and a write tool: `list_sites` (reads the DB directly — passes even if the API bridge is misconfigured) **and** `create_site` (forwards your token to the API). If `create_site` returns 401 but `list_sites` works, the API is missing `MCP_PUBLIC_URL` (§5) — the read-only check alone will not catch it.

---

## 12. Deploy order + gotchas

- **Order:** Postgres + migrations → R2/KV → API (Fly) → MCP (Fly) → dashboard (Vercel) + Better Auth migrate → Worker (Cloudflare) → DNS → smoke test. The domains are known up front, so set every URL to its final value from the start (no chicken-and-egg).
- **`EDGE_SIGNING_KEY` must be stable** and identical across API instances; if it's unset the API generates an ephemeral key per boot and minted gated tokens break on restart.
- **The API is the only writer** of R2 and KV; never give the Worker write creds.
- **RLS at runtime:** the API/MCP must connect as `dropway_app` (non-superuser, non-BYPASSRLS). Do not point them at the owner role.
- **Serverless DB pooling:** use a pooled Postgres endpoint for the dashboard's Better Auth connection on Vercel.
- **Email:** set `REQUIRE_EMAIL_VERIFICATION=true` only with a real `MAIL_SMTP_URL`, or users can't verify.
- **R2 CORS** must allow `PUT` from the dashboard origin for browser deploys.
- **Custom domains** (the per-site Cloudflare-for-SaaS feature) only work when the API has `CF_ZONE_ID` + `CF_API_TOKEN`; otherwise the dashboard hides that UI.
