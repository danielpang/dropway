<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# TypeScript SDK + org API keys — engineering requirements

Requirements and design for two coupled deliverables:

1. **`@dropway/sdk`** — a first-party TypeScript SDK that can
   programmatically create sites and upload (deploy + publish) new versions,
   without a browser or an interactive OAuth flow.
2. **Org-scoped API keys** — a new credential type the SDK authenticates
   with: created per org, attributed to the member who created it
   (`created_by`), shown in full exactly once at creation, and stored only
   as a hash afterwards.

The SDK is the customer of the keys; the keys are the reason the SDK can
exist. They ship together but are separable milestones (keys land first).

## Background — what exists today

Every writer today authenticates with a short-lived Better Auth EdDSA JWT
and hits the same `/v1` control plane (`services/api/internal/router/router.go`):

- The dashboard uses the browser session; the CLI
  (`cli/internal/auth/oauth.go`) and MCP server
  (`services/mcp/internal/auth/auth.go`) both run OAuth 2.1 + PKCE and
  forward the user's bearer token. There is **no long-lived credential
  path anywhere** — nothing a CI job or a server-side script can hold.
- The JWT is verified in `internal/auth/jwks.go` (EdDSA-pinned, issuer +
  audience enforced); `internal/middleware/auth.go` stashes claims;
  `internal/middleware/rlstx.go` opens the request transaction and sets
  `app.current_org_id` / `app.current_user_id` for Postgres RLS. Every
  tenant table is `FORCE ROW LEVEL SECURITY`.
- Site creation and deployment are already a clean machine-usable loop:
  `POST /v1/sites` → `POST /v1/sites/{id}/deployments/prepare` (manifest
  in, presigned PUT URLs out) → direct-to-R2 blob uploads →
  `POST /v1/sites/{id}/deployments` (finalize; server re-hashes
  everything) → `POST /v1/sites/{id}/publish`. A complete reference
  client for this loop exists in Go:
  `services/mcp/internal/apiclient/apiclient.go` (`Client.Deploy`). The
  SDK is essentially a TypeScript port of that file.
- A dormant table `app.deploy_tokens` (hashed bearer tokens, scopes
  array, optional site scope) exists in the baseline migration with RLS
  policies but **no server-side code paths** — no sqlc queries, no
  handlers, nothing that mints or verifies one. It was scoped as the
  "CLI / CI deploy path" credential (per its `db/sqlc/schema.sql`
  comment): the client half was even built in anticipation — the CLI
  reads a `DROPWAY_TOKEN` env var as a CI bearer
  (`cli/internal/cmd/deploy.go`) and the audit trail reserved
  `actor_token` provenance (`internal/audit/audit.go`) — but the server
  half was never wired, and interactive OAuth login shipped for the CLI
  instead. API keys are that half-finished feature, finished properly.
- The control plane is described by OpenAPI 3.1
  (`services/api/openapi/openapi.yaml`); the dashboard already generates
  its client types from it with `openapi-typescript` — the SDK reuses
  that precedent.

## Goals

- A CI job or server script holding only an API key can: create a site,
  deploy files to it, publish, roll back, list its org's sites, and set
  basic access modes — the same `/v1` contract the MCP server exercises.
- Keys are org-scoped: one key acts inside exactly one org, under that
  org's RLS tenant, quotas, and kill switches.
- Keys are attributable: every key records `created_by` (the member who
  minted it), and actions taken with a key are attributed to that user
  for `NOT NULL` ownership columns (`sites.owner_user_id`,
  `site_versions.created_by`) and stamped with the key id in the audit
  log (`audit_log.actor_token` already exists for exactly this).
- The secret is displayed **once**, in the creation response, and never
  again — the server stores only a hash. List/read endpoints return
  metadata plus a non-secret display prefix.
- The CLI works headless: setting `DROPWAY_API_KEY` bypasses the
  interactive OAuth flow, so `dropway deploy` runs in CI with no browser
  and no stored session.
- Admin-manageable: create/revoke from the dashboard org settings and
  from `/v1`, admin-or-owner only, with live role re-checks (the
  `store/members.go` confused-deputy pattern).

## Non-goals (v1)

- **No OAuth client-credentials grant.** Static keys are the v1 shape;
  a Better Auth M2M flow can supersede them later without breaking the
  SDK (the SDK takes "a bearer credential" either way).
- **No fine-grained scopes UI.** The table carries a `scopes` column for
  forward-compatibility, but v1 issues every key with the full
  `sites:*` grant and does not expose scope selection.
- **No per-site-restricted keys** in the UI (column exists, unwired —
  same posture as `deploy_tokens` had).
- **No key rotation endpoint** (revoke + create covers it).
- **No hand-written ergonomics beyond the site/deploy path.** The SDK
  covers the **entire `/v1` OpenAPI surface** with generated typed
  calls, but only sites + deployments get the curated orchestration
  layer (`dw.sites.deploy(...)` etc.); everything else is exposed
  through the generated client as-is.
- **No self-host restriction.** Keys and SDK are core (FSL), not
  cloud-gated — self-hosters get the whole feature, per the open-core
  boundary.

## Data model — `app.api_keys`

A new table, superseding the dormant `app.deploy_tokens` (which has no
readers/writers and is dropped in the same migration; see *Migration
plan*). Follows house conventions: snake_case, uuid PKs via
`gen_random_uuid()`, `timestamptz` with `created_at`, org FK to
`app.org_meta`, RLS tenant isolation.

```sql
CREATE TABLE app.api_keys (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
    created_by   uuid NOT NULL,                 -- identity user id of the minting member
    name         text NOT NULL,                 -- human label, e.g. "GitHub Actions"
    key_hash     text NOT NULL UNIQUE,          -- sha256 hex of the full secret
    key_prefix   text NOT NULL,                 -- non-secret display handle, e.g. "dw_live_3fk9"
    scopes       text[] NOT NULL DEFAULT ARRAY['sites:*']::text[],
    site_id      uuid REFERENCES app.sites(id) ON DELETE CASCADE,  -- optional restriction; unwired in v1
    last_used_at timestamptz,
    expires_at   timestamptz,                   -- NULL = non-expiring (v1 default)
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    revoked_by   uuid
);

CREATE INDEX api_keys_org_idx ON app.api_keys (org_id, created_at DESC);

ALTER TABLE app.api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.api_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY api_keys_tenant_isolation ON app.api_keys
    USING (org_id = current_setting('app.current_org_id')::uuid);
```

Notes:

- **`created_by` is attribution, not ownership of the key.** The key
  belongs to the org; any admin/owner can list and revoke it. But the
  key *acts as* `created_by` — see *Authentication path* — which is what
  makes `owner_user_id` / `created_by` columns and the audit trail
  truthful ("this site was created by Dana's CI key").
- **`key_hash` is plain SHA-256 hex, not bcrypt.** Site passwords use
  bcrypt (`internal/pwhash`) because they're low-entropy. API keys are
  256-bit random values: brute-force is not a threat, and verification
  must be an indexed `WHERE key_hash = $1` lookup on every API request —
  bcrypt's per-verify cost and salting preclude that. This is the
  industry-standard trade for high-entropy tokens.
- **Auth-path lookup runs outside the tenant transaction.** The whole
  point of the lookup is to *discover* the org, so it can't run under
  the RLS GUC. The resolver uses a dedicated sqlc query executed on the
  pool before `rlstx` opens the tenant transaction (precedent: the
  password-exchange endpoint also authenticates before tenant context
  exists). All management queries (list/create/revoke) run normally
  under RLS.

## Key format and lifecycle

- **Format**: `dw_live_<43 base62 chars>` — 256 bits of
  `crypto/rand` entropy. The fixed `dw_live_` prefix makes keys
  recognizable in the auth middleware (distinguishes them from JWTs in
  the same `Authorization: Bearer` header), greppable in leaked code,
  and registrable with GitHub secret scanning (stretch goal).
- **`key_prefix`** stored for display is `dw_live_` + the first 4 chars
  of the random part (e.g. `dw_live_3fk9…`) — enough for a user to match
  a leaked key to a row, useless for recovery.
- **Shown once.** `POST /v1/api-keys` is the only response that ever
  contains the full secret. The server hashes it, stores the hash,
  returns `{ ...metadata, key: "dw_live_..." }`, and discards the
  plaintext. Every other read returns metadata + `key_prefix` only. The
  dashboard renders the one-time reveal with copy-to-clipboard and an
  explicit "you won't see this again" affordance.
- **Revocation is immediate and terminal.** `revoked_at` set → the very
  next request with that key gets 401. No un-revoke.
- **`last_used_at`** updates at most once per 5 minutes per key (cheap
  `UPDATE ... WHERE last_used_at < now() - interval '5 minutes'`), so
  the column is useful for "is this key dead?" hygiene without turning
  every GET into a write.

## Authentication path (Go API)

A new branch in the auth middleware, keyed off the token shape:

1. `internal/middleware/auth.go` inspects the bearer token. Prefix
   `dw_live_` → API-key path; otherwise → existing JWT verifier
   (unchanged).
2. API-key path: SHA-256 the presented token, single indexed lookup
   `SELECT ... FROM app.api_keys WHERE key_hash = $1 AND revoked_at IS
   NULL AND (expires_at IS NULL OR expires_at > now())`. Miss → 401
   (same body as a bad JWT; no oracle distinguishing "unknown" from
   "revoked").
3. Fail-closed liveness checks, mirroring the JWT path's posture:
   - `org_meta.org_status = 'active'` (suspended / over_limit orgs are
     dead to keys, same as to sessions);
   - `created_by` still has **live membership** in the org
     (`store/members.go` `MemberRole`). If the minting member left or
     was removed, the key stops working — a departed employee's keys
     must not outlive them. Dashboard surfaces these as "inactive
     (creator left org)" and admins re-mint under a current member.
4. On success, synthesize the same `*auth.Claims` the JWT path produces
   (`sub = created_by`, `org_id`, `role` = the live role from step 3),
   plus the key id in context. Everything downstream — `rlstx` tenant
   GUCs, handlers, quota checks, the collaboration guard
   (`requireSiteEditor`) — works unmodified because it consumes Claims,
   not JWTs.
5. Handlers that write `audit_log` stamp `actor_token = key.id`
   alongside `actor_user = created_by`.

**Role ceiling**: whatever the creator's live role is, key requests are
additionally capped at `member`-level permissions. Keys can create sites
and deploy; they cannot revoke other keys, change org policy, manage
members, or flip kill switches, even when `created_by` is an owner. A
leaked CI key must never be an org-takeover. (Implementation: the
synthesized Claims carry the real role for attribution, but the context
flag "authenticated via key" makes `IsAdminRole`-gated handlers refuse
with 403 `admin_required_interactive`.)

**Kill switch**: `org_meta` gains `api_keys_enabled boolean NOT NULL
DEFAULT true` — the same governance pattern as `mcp_enabled` /
`ai_enabled`. Off → every key in the org 401s; management endpoints
still work so admins can see and revoke.

## Key management API

All under `/v1`, session-JWT-only (keys cannot manage keys — see role
ceiling), admin-or-owner with live role re-check:

- `POST /v1/api-keys` — `{ name }` → `201 { id, org_id, name,
  key_prefix, created_by, created_at, key }`. **Only appearance of
  `key`.** Audit-logged (`api_key.created`).
- `GET /v1/api-keys` — list for the active org: metadata + `key_prefix`
  + `last_used_at` + creator identity; never the hash, never the secret.
- `DELETE /v1/api-keys/{id}` — sets `revoked_at`/`revoked_by`;
  idempotent; audit-logged (`api_key.revoked`).

Dashboard: an **API keys** section in org settings (admin-visible)
backed by these endpoints — create with name, one-time reveal modal,
list with prefix / creator / created / last-used, revoke with confirm.
The OpenAPI spec (`services/api/openapi/openapi.yaml`) gains these
paths and the `dw_live_` bearer scheme; the dashboard's generated
client picks them up via the existing `gen:api` flow.

## The SDK — `@dropway/sdk`

A new workspace package at `packages/sdk` (the `pnpm-workspace.yaml`
glob already covers it), published to npm as `@dropway/sdk`.

**Runtime and shape**:

- TypeScript 5.7 (root `tsconfig.base.json`), dual ESM + CJS output,
  Node ≥ 18 (global `fetch`, `node:crypto` for SHA-256). **Zero runtime
  dependencies** — the deploy loop needs only `fetch`, hashing, and
  file reads.
- **Two layers, one package.** The base layer is a fully generated,
  typed client for the **whole `/v1` OpenAPI spec**
  (`services/api/openapi/openapi.yaml`, via `openapi-typescript` +
  a thin typed `fetch` wrapper, same toolchain as the dashboard) —
  every path, request, and response in the spec is callable as
  `dw.api.GET("/v1/skills", …)`-style operations, so sites, skills,
  chats, domains, and access policies are all reachable from day one
  with zero per-endpoint maintenance. On top sits a small hand-written
  ergonomic layer (`dw.sites.*`) for the headline flow — the multi-step
  deploy loop that a generated client cannot express. The spec is the
  single source of truth; the SDK build regenerates and fails on drift.
- Full coverage doesn't repeal the key role ceiling: session-only and
  admin-gated operations (key management, org policy, member admin)
  exist in the generated layer but return 403 under key auth, exactly
  as they would for any keyed caller. The SDK documents this per the
  spec's security annotations rather than hiding the endpoints.
- Works in any environment with `fetch` for the API calls; the
  filesystem convenience (`deployDir`) is Node-only and lazily imports
  `node:fs`, so bundlers targeting edge runtimes can tree-shake it.

**Public surface (v1)** — the ergonomic layer; anything not shown here
is still available through the generated `dw.api` layer:

```ts
import { Dropway } from "@dropway/sdk";

const dw = new Dropway({ apiKey: process.env.DROPWAY_API_KEY });
// baseUrl option for self-hosters; defaults to the hosted API.

// Create
const site = await dw.sites.create({ slug: "launch-page", accessMode: "public" });

// Upload + publish in one call (the common CI path)
const deploy = await dw.sites.deploy(site.id, {
  files: { "index.html": htmlString, "app.js": bytes },  // or:
  // dir: "./dist",                                       // Node-only
  publish: true,                                          // default true
});
// → { versionId, versionNo, previewUrl, liveUrl }

// Lower-level lifecycle, mirroring the API
await dw.sites.publish(site.id, { versionId });  // also = rollback
await dw.sites.list();
await dw.sites.get(site.id);
await dw.sites.setAccess(site.id, { accessMode: "org_only" });
```

**`deploy()` is a faithful port of `apiclient.go Client.Deploy`**, and
must preserve its invariants:

1. Build the manifest client-side: walk input, SHA-256 each file,
   normalize to clean relative paths, infer content types.
2. `prepare` → receive `missing` + presigned PUT URLs; upload only
   missing blobs, **directly to object storage, with no Authorization
   header and no content-type** (the presigned SigV4 URL is the
   credential and the signature breaks otherwise) — concurrency-limited
   (default 8), per-blob retry with backoff.
3. Compute the deploy digest exactly as `internal/manifest.Digest` does
   (sha256 over sorted `"<sha256>  <path>\n"` lines) and `finalize`.
4. `publish` unless `publish: false`.

The digest algorithm gets a documented test vector shared between the
Go and TS implementations (a fixture in `testdata/`) so the two can
never silently diverge.

**Errors**: one typed `DropwayError` hierarchy mapped from status codes,
with `QuotaExceededError` carrying the parsed 402 `ExceededError` body
(`{limit, current, max, plan_tier, next_tier, upgrade_url}`) — CI logs
should say "site cap reached (10/10 on free), upgrade at …", not "402".
401 on a revoked key says so plainly. Retries: idempotent GETs and blob
PUTs retry on 5xx/network with jittered backoff; control-plane POSTs do
not auto-retry, except that finalize is safe to retry because
`site_versions_site_content_hash_key` makes deploys idempotent per
content hash.

**Docs & examples**: package README with the CI quickstart (GitHub
Actions example using a repository secret), and a runnable example under
`examples/`.

The constructor falls back to `process.env.DROPWAY_API_KEY` when
`apiKey` isn't passed (throwing a clear error if neither is present), so
SDK and CLI share one configuration convention.

## CLI — headless auth via `DROPWAY_API_KEY`

The CLI's only credential today is the interactive browser OAuth flow
(`cli/internal/auth/oauth.go`) — a dead end in CI, which is exactly
where `dropway deploy` is most wanted. Keys close that gap with a small
client-side change; the server needs nothing beyond the middleware
already scoped above.

- When `DROPWAY_API_KEY` is set, the CLI uses it as the bearer token on
  every request and skips the OAuth flow and any stored session
  entirely. No `dropway login` required.
- **The env var takes precedence over a logged-in session.** CI runs
  must be deterministic even on a machine (or reused runner) where
  someone once logged in interactively; an explicitly set key is the
  stronger signal of intent. `dropway whoami` reports which credential
  source is active ("authenticated via API key dw_live_3fk9… (org
  acme)") so precedence is never a mystery.
- **Env var only; never persisted.** No `--api-key` flag (flags leak
  into shell history and process lists) and the key is never written to
  the CLI's config or OS keychain — it is read from the environment on
  each invocation. This matches the `VERCEL_TOKEN` /
  `NETLIFY_AUTH_TOKEN` / `GITHUB_TOKEN` convention CI users already
  know.
- **One variable, no legacy alias.** The CLI's existing `DROPWAY_TOKEN`
  env-var handling (`cli/internal/cmd/deploy.go`) — the client half of
  the never-finished deploy-tokens feature, whose table is dropped in
  migration 0015 — is **removed** as part of this work, not aliased.
  It never worked end-to-end (nothing server-side ever accepted a
  minted token), so there is nothing to stay compatible with;
  `DROPWAY_API_KEY` is the only documented variable.
- **Role-ceiling errors are translated.** Commands gated on admin roles
  or key management will 403 under a key; the CLI maps
  `admin_required_interactive` to "this command requires an interactive
  login — run `dropway login`" instead of surfacing a bare 403.
- Keyed invocations are subject to the same per-key rate limit; the CLI
  already honors `Retry-After`.

## Rate limiting

There is no per-principal rate limiting today (the in-memory bucket in
`handlers/ratelimit.go` guards only the password-exchange endpoint).
Keys make the API scriptable, so v1 ships a minimal guard:

- Reuse the existing token-bucket limiter, keyed by `api_key.id`, on
  the API-key path only: default **120 requests/min** per key, burst
  240; presigned-URL blob PUTs don't touch the API so bulk uploads stay
  fast. 429 with `Retry-After`; the SDK honors it transparently.
- Same single-process caveat as the existing limiter (fine on the
  current single-machine Fly deployment; promote to Redis when the API
  scales out — noted, not built).
- JWT paths stay unlimited in v1 (unchanged behavior).

Quotas need no new work: keys flow through the same `quota.Provider`
seam and 402 contract as every other writer, and self-host builds keep
`Unlimited{}`. A dormant `ResourceAPIKeysPerOrg` resource is added
(unlimited on every tier) so hosted policy can cap key counts later
without a store or handler change — the `ResourceSkillPerOrg` pattern.

## Security requirements

- Plaintext secrets exist only in: the generation stack frame, the
  single creation response, and the client's hands. Never logged, never
  in audit rows, never in error messages; middleware redacts
  `Authorization` on the key path in any request logging.
- Hash comparison is an indexed equality on SHA-256 of a 256-bit random
  value; no timing oracle exists because the attacker-controlled input
  is hashed before comparison.
- 401 responses are uniform across unknown / revoked / expired /
  disabled-org keys.
- Revocation and the org kill switch take effect on the next request —
  no cached auth decisions on the key path.
- The role ceiling (member-level, no key-management, no org admin) caps
  blast radius of a leaked key; creator-departure fail-closed caps
  credential lifetime to employment lifetime.
- Stretch: register the `dw_live_` pattern with GitHub secret scanning
  for leaked-key notification.

## Migration plan

The old table goes first, on its own: **migration
`0015_drop_deploy_tokens.sql` ships separately** (its own PR), dropping
`app.deploy_tokens` — no server code has ever read or written it, so
the drop is a no-op in every real database, and keeping two
nearly-identical dormant token tables would only invite drift. That
change also removes the table from the sqlc schema mirror, the
generated `AppDeployToken` model, and the RLS integration test's table
list.

The API-keys work is then one goose migration in `db/migrations/app/`:

1. `CREATE TABLE app.api_keys` + index + RLS policies (above).
2. `ALTER TABLE app.org_meta ADD COLUMN api_keys_enabled boolean NOT
   NULL DEFAULT true`.

Alongside it: mirror the DDL in `db/sqlc/schema.sql`, add queries to
`db/sqlc/query.sql` (resolve-by-hash, insert, list-by-org, revoke,
touch-last-used), regenerate sqlc, add `app.api_keys` to the RLS
integration test's table list, repoint the audit `ActorToken` doc
comments (`internal/audit/audit.go`, the sqlc query comments) at
`app.api_keys.id`, and delete the CLI's legacy `DROPWAY_TOKEN`
env-var handling in favor of `DROPWAY_API_KEY`.

## Testing

- **Store/middleware (Go)**: key resolution hit/miss/revoked/expired;
  creator-departure fail-closed; role ceiling (owner-minted key still
  403s on admin endpoints); suspended-org and kill-switch 401s; RLS —
  keys from org A invisible under org B's tenant context; `last_used_at`
  throttling; uniform-401 assertions.
- **Handlers**: create returns secret once and list never does; revoke
  idempotency; admin-only management with live role re-check; audit rows
  carry `actor_token`.
- **SDK (TS)**: digest test vector parity with Go (shared fixture);
  manifest normalization (path cleaning, content types); deploy loop
  against a mocked API + a MinIO-backed integration test in CI
  (create → deploy → publish → GET the preview URL); 402 and 429
  mapping; retry semantics.
- **End-to-end smoke**: a real CI workflow in this repo deploys a
  fixture site to a dev org using the SDK and an org key — the
  dogfooded proof that the headline works.

## Milestones

1. **Keys backend** — migration, sqlc, middleware branch, role ceiling,
   management endpoints, audit, rate limit, OpenAPI. (Feature-complete
   API-key auth usable with `curl`.)
2. **Dashboard** — org-settings section: create/reveal-once/list/revoke,
   kill switch toggle.
3. **SDK + CLI** — `packages/sdk` scaffold, generated full-spec `/v1`
   client, hand-written `dw.sites` layer with the deploy-loop port,
   errors/retries, tests incl. digest parity; CLI `DROPWAY_API_KEY`
   support (precedence, legacy `DROPWAY_TOKEN` removal, `whoami`
   source reporting, role-ceiling error mapping).
4. **Polish & launch** — examples, README/docs, e2e smoke workflow,
   secret-scanning registration (stretch), changelog entry.

## Open questions

- **Expiry defaults**: v1 keys are non-expiring (`expires_at` nullable,
  unset). Should hosted orgs get an optional max-age policy knob later?
  (Column and check are in place; policy deliberately deferred.)
- **Should `deploy_site`-style MCP tools accept keys?** Not in v1 — MCP
  stays OAuth-only, keeping one interactive and one non-interactive
  credential story. Revisit if agent-hosting platforms need headless
  MCP.
- **Attribution when the creator is deactivated vs. removed**: v1
  treats any loss of live membership as key death. If Better Auth grows
  a "suspended member" state distinct from removal, decide whether keys
  pause or die.
