---
name: verify-api-contract
description: Regenerate Dropway's generated API types and sqlc code and confirm the committed copies match their sources. Use whenever the diff touches services/api/openapi/openapi.yaml, db/sqlc/, apps/dashboard/lib/api-generated/, packages/sdk/src/generated/ or services/api/internal/store/db/.
---

# Verify generated code matches its source

Three generated outputs are committed. Each must equal what its generator produces from the
current source; hand edits and forgotten regenerations both show up as a diff.

| Source | Generated output |
|---|---|
| `services/api/openapi/openapi.yaml` | `apps/dashboard/lib/api-generated/schema.ts` |
| `services/api/openapi/openapi.yaml` | `packages/sdk/src/generated/schema.ts` |
| `db/sqlc/schema.sql`, `db/sqlc/query.sql` | `services/api/internal/store/db/` |

## 1. Regenerate

```sh
pnpm install --frozen-lockfile
pnpm --filter @dropway/dashboard gen:api
pnpm --filter @dropway/sdk gen:api
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate -f db/sqlc/sqlc.yaml
```

The sqlc version must match the `sqlc v…` line at the top of the generated Go files. If you
upgrade it, change the version here, in `CLAUDE.md` and in the `codegen` CI job together.
The first sqlc run compiles the tool and takes a few minutes.

## 2. Check for drift

```sh
git status --short apps/dashboard/lib/api-generated packages/sdk/src/generated \
  services/api/internal/store/db
```

- **No output:** the committed copies are current.
- **Changes after you edited a source:** expected. Keep them and commit them with the source.
- **Changes when you did not edit a source:** someone edited generated code by hand or
  skipped regeneration. Move the intent into the source file, regenerate, and tell the user.

## 3. Check the contract still holds

A regenerated type that compiles is not yet a working contract.

- Run `go build ./... && go test ./services/api/...` and `pnpm -r typecheck`.
- For each changed OpenAPI operation, confirm the Go handler's request and response shapes
  match the spec, including status codes and which fields are required.
- Flag breaking changes for published clients (the SDK, the CLI, the MCP server): removed
  operations or fields, a field that became required, a changed type or enum. These need a
  deliberate decision from the user, not a silent regenerate.
- If `db/sqlc/schema.sql` changed, confirm a migration under `db/migrations/app/` makes the
  same change, and run the `verify-migration` skill.

## 4. Report

Say which generators ran, whether there was drift and what you did about it, and list any
breaking contract changes.
