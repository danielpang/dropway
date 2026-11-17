# Review instructions

## What Important means here

Reserve Important for findings that would break behavior, leak data
across organizations, or block a rollback: incorrect logic, database
queries not scoped to the caller's organization, a missing or weakened
access check on a gated site, secrets or PII in logs or error
messages, migrations that are not backward compatible or have no
working Down, and core code that depends on `cloud/` or `ee/`. Style,
naming, and refactoring suggestions are Nit at most.

Behavior claims need a `file:line` citation in the source, not an
inference from naming.

## Cap the nits

Report at most five Nits per review.

## Do not report

- Anything CI already enforces: lint, formatting, type errors, `go vet`,
  workflow and shell lint, and drift in generated code
- Generated files under `apps/dashboard/lib/api-generated/`,
  `packages/sdk/src/generated/` and `services/api/internal/store/db/`,
  plus `pnpm-lock.yaml` and `go.sum`
- Demo sites under `examples/` and fixtures under `testdata/`
- Test-only code that intentionally violates production rules

## Always check

- Database queries are scoped to the caller's organization, and the
  tenant context is set before any query runs
- New tenant tables carry `org_id`, an index leading on `org_id`, both
  `ENABLE` and `FORCE ROW LEVEL SECURITY`, and the standard
  `org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid`
  policy with no subquery
- Migrations have a `-- +goose Down` that reverses the Up, do not edit a
  migration already on `main`, and leave the previous API version working
  (no dropped or renamed column, no new `NOT NULL` without a default)
- A schema change in `db/migrations/app/` is mirrored in
  `db/sqlc/schema.sql`
- New or changed API routes appear in `services/api/openapi/openapi.yaml`,
  enforce authentication and the caller's role, and have a test
- Log lines and error messages don't include tokens, API keys, session
  cookies, email addresses, or request bodies
- A change to access gating or route resolution in `services/serve` is
  matched in `edge/serving-worker`, or the pull request says why not
- A change to the deploy manifest digest is made in Go
  (`internal/manifest`), the dashboard and the SDK together
- Core code (`services/`, `cli/`, `internal/`, `apps/`, `edge/`) does not
  reference the `billing` schema
- A change to a user-visible dashboard flow adds or updates a Playwright
  spec under `apps/dashboard/e2e/`
