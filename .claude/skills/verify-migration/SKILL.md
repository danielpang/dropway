---
name: verify-migration
description: Check a database migration against Dropway's migration rules and prove it applies and rolls back. Use whenever the diff adds or edits a file under db/migrations/ or changes db/sqlc/schema.sql.
---

# Verify a migration

The rules come from "Migration rules" in `CONTRIBUTING.md` and
`db/migrations/app/README.md`. A mistake here can leak one tenant's rows to another or leave
production unable to roll back, so check every item and report each violation as `file:line`.

## 1. Read the new migration files

List them with `git diff --name-only main...HEAD -- db/migrations` plus untracked files.

For each file under `db/migrations/app/`:

- **Name** is `NNNN_name.sql`, and `NNNN` is exactly one more than the current highest.
  Check `main` again before merging; two branches often claim the same number.
- **No edits to applied migrations.** Only new files may appear. Never edit
  `0001_baseline.sql` or any migration already on `main`.
- **Both sections exist:** `-- +goose Up` and `-- +goose Down`. The Down section must undo
  everything Up did, in reverse order. An empty Down, or a comment saying it cannot be
  reversed, needs the user's explicit agreement.
- **Schema ownership.** Only the `app` schema is touched. Nothing creates or alters objects
  in `identity` (owned by Better Auth). Nothing in the core references `billing`.
- **Rolling deploys.** Migrations run before the new API is released, so the previous API
  version must keep working against the new schema. Flag any dropped or renamed column or
  table, any new `NOT NULL` column without a default, and any tightened constraint that
  existing rows or the old code could violate. These need a two-step rollout.
- **Locks.** On a table that may be large, flag index builds without `CONCURRENTLY` and
  rewrites that hold a long exclusive lock.

For each **new tenant table**, all of these must hold:

- An `org_id` column, denormalized onto the table itself.
- A composite index whose leading column is `org_id`.
- Both `ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY`.
- A policy whose predicate is exactly
  `org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid`, with no
  subquery. The `NULLIF` is what makes an unset context deny instead of erroring.
- `GRANT`s to `dropway_app` for only the operations the API needs.

## 2. Check the mirrors

- `db/sqlc/schema.sql` mirrors the Up-side DDL of every migration (no goose annotations, no
  DROPs). Update it in the same change.
- If the schema changed, run the `verify-api-contract` skill so the sqlc output is
  regenerated.
- A new tenant table should be added to the RLS isolation assertion in the `sql` job of
  `.github/workflows/ci.yml`: seed one row per org, then assert org A cannot see org B's row.

## 3. Apply it and roll it back

Use a throwaway Postgres, never a shared or production database.

```sh
docker run -d --name dropway-migrate-check -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=dropway -p 55432:5432 pgvector/pgvector:pg16
export DSN='postgres://postgres:postgres@localhost:55432/dropway?sslmode=disable'
go install github.com/pressly/goose/v3/cmd/goose@latest
goose -dir db/migrations/app postgres "$DSN" up
goose -dir db/migrations/app postgres "$DSN" redo     # Down then Up for the newest file
goose -dir db/migrations/app postgres "$DSN" status
docker rm -f dropway-migrate-check
```

Run `redo` once for each new migration, newest first, using `goose down` to step back
between them. To confirm Down restores the schema exactly, compare
`pg_dump --schema-only` taken before `up` of the new file and after its `down`.

If Docker is not available, say that the apply and rollback check did not run. CI's `sql` job
applies migrations but does not run Down, so a broken Down is only caught here.

## 4. Report

List each violation as `file:line` with the rule it breaks, fix the ones that are yours to
fix, and state the result of the apply and rollback run with its output.
