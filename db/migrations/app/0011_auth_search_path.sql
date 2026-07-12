-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0011_auth_search_path.sql
--
-- Pin the auth connection's search_path at the ROLE level so it works over a
-- Supabase TRANSACTION-mode pooler.
--
-- Better Auth (the dashboard) keeps its tables in the `identity` schema and
-- issues UNqualified queries (user, session, member, organization, …). It used
-- to make those resolve by sending the `options=-c search_path=identity` startup
-- parameter on its pg Pool. Supabase's transaction pooler (the only endpoint
-- Vercel can reach: ...:6543 over IPv4) REJECTS startup `options` with
-- `08P01 unsupported startup parameter in options: search_path`, so every auth
-- query failed once the dashboard moved onto that pooler.
--
-- Setting the search_path on the role instead is transaction-pooler safe: it is
-- applied by the backend on connect, not passed as a startup parameter. `identity`
-- goes first so Better Auth's unqualified tables resolve to (and, on a fresh
-- migrate, are created in) `identity`; the Supabase defaults ("$user", public,
-- extensions) are preserved after it. The Go API is unaffected because every one
-- of its queries is schema-qualified (app.*, billing.*, identity.member).
--
-- Guarded: if the `postgres` role is absent or this owner role lacks privilege to
-- alter it (some self-host setups), the change is skipped with a NOTICE rather
-- than failing the migration. Self-host over a direct/session connection does not
-- need this (startup `options` works there); it is a Supabase-pooler concern.

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    EXECUTE format(
      'ALTER ROLE postgres IN DATABASE %I SET search_path = identity, "$user", public, extensions',
      current_database()
    );
  EXCEPTION
    WHEN insufficient_privilege OR undefined_object THEN
      RAISE NOTICE 'skipping auth search_path pin (role "postgres" not alterable here): %', SQLERRM;
  END;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    EXECUTE format(
      'ALTER ROLE postgres IN DATABASE %I RESET search_path',
      current_database()
    );
  EXCEPTION
    WHEN insufficient_privilege OR undefined_object THEN
      RAISE NOTICE 'skipping auth search_path reset (role "postgres" not alterable here): %', SQLERRM;
  END;
END $$;
-- +goose StatementEnd
