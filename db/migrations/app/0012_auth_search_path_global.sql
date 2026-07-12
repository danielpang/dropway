-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0012_auth_search_path_global.sql
--
-- Corrects 0011: pin the auth search_path on the `postgres` role GLOBALLY (no
-- `IN DATABASE`) rather than per-database.
--
-- Why the change:
--   * 0011 used `ALTER ROLE postgres IN DATABASE <db> SET search_path`, which is
--     stored in `pg_db_role_setting`. It is invisible to the usual
--     `SELECT rolconfig FROM pg_roles` check and, in practice, did not take effect
--     on Supabase's dedicated PgBouncer backends. The role-GLOBAL form (stored in
--     `pg_roles.rolconfig`) is what actually applied to the pooler connections.
--   * 0011's guard swallowed a privilege error with a NOTICE, so a skip looked
--     like success. Here a skip is a WARNING so it is visible in migration logs.
--
-- `identity` goes first so Better Auth's UNqualified tables (user, session,
-- member, organization, …) resolve to it; the Supabase defaults ("$user", public,
-- extensions) are preserved after it. The Go API is unaffected: all its queries
-- are schema-qualified (app.*, billing.*, identity.member).
--
-- Idempotent (ALTER ROLE ... SET re-applies cleanly). NOTE: role search_path is
-- applied by the backend on CONNECT, so existing pooled server connections must be
-- recycled (restart the Supabase pooler/DB) before it takes effect on live traffic.

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  ALTER ROLE postgres SET search_path = identity, "$user", public, extensions;
EXCEPTION
  WHEN insufficient_privilege OR undefined_object THEN
    RAISE WARNING 'could not pin auth search_path on role "postgres" (%): set it manually where Better Auth connects', SQLERRM;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
  ALTER ROLE postgres RESET search_path;
EXCEPTION
  WHEN insufficient_privilege OR undefined_object THEN
    RAISE WARNING 'could not reset auth search_path on role "postgres" (%)', SQLERRM;
END $$;
-- +goose StatementEnd
