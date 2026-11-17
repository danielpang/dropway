<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Dropway: notes for coding agents

Dropway hosts static sites per organization. A Go API is the system of record, a Next.js
dashboard is the control plane, and sites are served by a Go content server (self-host) or a
Cloudflare Worker (hosted). [`CONTRIBUTING.md`](CONTRIBUTING.md) holds the contribution rules
(DCO sign-off, migration rules); this file holds the commands and the verification routine.

## Layout

| Path | What it is |
|---|---|
| `services/api` | Go control-plane API; OpenAPI spec in `services/api/openapi/openapi.yaml` |
| `services/serve`, `services/mcp` | Go content server and MCP server |
| `cli/`, `internal/` | Go CLI and shared Go packages |
| `apps/dashboard` | Next.js dashboard with Better Auth; Playwright specs in `e2e/` |
| `edge/serving-worker` | Cloudflare Worker that serves hosted sites |
| `packages/sdk`, `packages/room-runtime`, `contracts` | TypeScript packages |
| `db/migrations/app` | goose migrations for the Go-owned `app` schema |
| `db/sqlc` | sqlc schema mirror and queries; output in `services/api/internal/store/db` |
| `cloud/`, `ee/` | Proprietary; the core must never import them (see below) |
| `deploy/` | Self-host compose stack |

## Commands

Run the ones that match what you changed before saying the work is done.

**Go** (any `*.go`, `go.mod`, `go.sum`)

```sh
go build ./... && go vet ./... && go test ./...
go build -tags cloud ./...
```

Integration tests need real Postgres or MinIO and the `integration` tag, for example
`go test -tags integration ./services/mcp/cmd/mcp/...`. CI's `mcp`, `serve` and
`hosted-collaboration` jobs show the exact services and env each one needs.

**TypeScript** (`apps/`, `edge/`, `packages/`, `contracts/`)

```sh
pnpm install --frozen-lockfile
pnpm --filter @dropway/sdk build      # the dashboard imports the built SDK
pnpm -r typecheck
pnpm -r lint
pnpm --filter @dropway/dashboard test
pnpm --filter @dropway/sdk test
pnpm --filter @dropway/serving-worker test
pnpm --filter @dropway/room-runtime test
```

**Generated code** (after editing `openapi.yaml`, `db/sqlc/schema.sql` or `db/sqlc/query.sql`)

```sh
pnpm --filter @dropway/dashboard gen:api
pnpm --filter @dropway/sdk gen:api
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate -f db/sqlc/sqlc.yaml
```

Commit the regenerated files. CI fails if regenerating changes anything.

**Full stack and browser tests**

```sh
cp deploy/.env.example deploy/.env    # only if deploy/.env is missing
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
docker compose -f deploy/docker-compose.yml --env-file deploy/.env exec -T dashboard \
  pnpm dlx @better-auth/cli@latest migrate --yes
pnpm --filter @dropway/dashboard test:e2e
```

The dashboard is ready when `http://localhost:3000/api/auth/ok` returns 200. The API is on
`:8080`, the content server on `:8090`, MCP on `:8092`.

**Deploy scripts and workflows** (`scripts/deploy/`, `.github/workflows/`)

```sh
shellcheck scripts/deploy/*.sh
python3 -m unittest discover -s scripts/deploy/tests -v
actionlint
```

## Invariants

- **Open-core boundary.** `services/`, `cli/`, `internal/`, `apps/` and `edge/` must not
  import `cloud/` or `ee/` except through a build-tagged seam. The `open-core-boundary` CI
  job enforces it.
- **Tenant isolation.** Every tenant table carries `org_id` and forced row-level security.
  The full rule is under "Migration rules" in `CONTRIBUTING.md`.
- **Digest parity.** The deploy manifest digest must be byte-identical across Go
  (`internal/manifest`), the dashboard and the SDK. Each side has a parity test.
- **Commits are signed off** (`git commit -s`).

## Verification

Tests, typecheck and lint are the floor. A change is verified when someone has watched it
work. The project skills in `.claude/skills/` hold the procedures:

| Skill | Use it when |
|---|---|
| `verify` | A change alters behavior a user, API client or visitor would see. Run before reporting the work as done. |
| `verify-migration` | The diff touches `db/migrations/` or `db/sqlc/schema.sql`. |
| `verify-api-contract` | The diff touches `openapi.yaml`, `db/sqlc/`, or either generated output. |
| `ship` | Before opening a pull request. Chains review, cleanup and the checks above. |

When you report, say what you ran and what you saw. If a check could not run (no Docker, no
credentials), say so plainly; do not describe unrun checks as passing.
