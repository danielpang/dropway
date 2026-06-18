# app schema migrations

These are the [goose](https://github.com/pressly/goose) migrations for the `app`
schema (the Go API's system of record). They are applied by the `migrate` step in
`deploy/docker-compose.yml` as the privileged owner role (`DATABASE_OWNER_URL`).

## History starts at a squashed baseline

`0001_baseline.sql` is a **squashed baseline**, not the original first migration.
Before the first tagged release, while there was no production database to
upgrade, the original 13 incremental migrations (`0001_schemas_and_roles` through
`0013_org_status`) were collapsed into this single file for legibility: each table
is shown in its final form instead of being reconstructed by replaying a dozen
`ALTER`s.

The baseline was generated from a `pg_dump --schema-only` of a database with all
13 migrations applied, then **verified to be byte-for-byte identical**: applying
the baseline to a fresh database and diffing its schema dump against the
13-migration dump produced zero differences (including RLS policies, grants, and
the `SECURITY DEFINER` function `search_path` hardening).

## Adding new migrations

Just add `0002_*.sql`, `0003_*.sql`, and so on as normal. Never edit `0001_baseline.sql`
once anything has been deployed from it; append a new migration instead. The next
squash (if ever) should likewise happen only when there are no databases that
would be left mid-history.

## Existing developer databases

A database created before the squash is at goose version 13; goose treats the
baseline (version 1) as already-applied and runs nothing, so it keeps working. To
get a clean version history, drop and recreate the database so it builds from the
baseline. (Pre-launch, just recreate it.)

Note: the `identity` schema is owned and migrated by Better Auth (the dashboard), not
by these files. The baseline only ensures the `identity` namespace exists as an FK
target and grants the runtime role read access.
