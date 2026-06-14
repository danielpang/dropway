-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0001_schemas_and_roles.sql
--
-- Bootstraps the two namespaced schemas and the runtime login role.
--
-- Schema ownership (see ARCHITECTURE.md §5 / §8):
--   * auth  -- OWNED + migrated by Better Auth (user/session/account/jwks/
--             organization/member/invitation). We only ensure it EXISTS so that
--             app FKs to auth.organization(id) have a target; Better Auth runs
--             its own migrations into it. We never create/alter tables here.
--   * app   -- OWNED by the Go API via these goose migrations.
--
-- Role model (see ARCHITECTURE.md §5 RLS):
--   * These migrations are applied by a privileged OWNER/admin role (the schema
--     owner, e.g. the `postgres`/migration role from the migration DATABASE_URL).
--     That owner role is the table owner.
--   * The Go API connects at RUNTIME as `shipped_app`, a NON-superuser,
--     NON-BYPASSRLS login role. Because every app.* tenant table is later put in
--     `FORCE ROW LEVEL SECURITY` (0003), even the table owner is subject to RLS
--     during normal queries -- `shipped_app` is doubly so. This is the
--     tenant-isolation backstop underneath the Go API's primary authz layer.
--
-- The `shipped_app` password is NOT set here -- secrets stay out of migrations.
-- Provision it out-of-band, e.g.:
--     ALTER ROLE shipped_app WITH PASSWORD :'shipped_app_password';
-- (psql variable from the SHIPPED_APP_DB_PASSWORD env var; see deploy/.env.example),
-- or rely on the connection's auth method (IAM / scram from the secret store).

-- +goose Up
-- +goose StatementBegin
CREATE SCHEMA IF NOT EXISTS app;
-- +goose StatementEnd

-- +goose StatementBegin
-- auth is Better-Auth-owned; we only guarantee the schema exists as an FK target.
CREATE SCHEMA IF NOT EXISTS auth;
-- +goose StatementEnd

-- +goose StatementBegin
-- Create the non-superuser, non-BYPASSRLS runtime login role if it is absent.
-- Guarded so re-running (or running against a managed PG where the role is
-- pre-provisioned, e.g. Supabase) is idempotent. NOSUPERUSER + NOBYPASSRLS are
-- spelled explicitly to document the security intent; LOGIN makes it a usable
-- connection role. Password is intentionally omitted (set out-of-band).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'shipped_app') THEN
        CREATE ROLE shipped_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The runtime role must be able to enter the schemas to reach the tables.
-- Table-level DML grants (SELECT/INSERT/UPDATE/DELETE) are issued per-table in
-- 0003, alongside the RLS policies that constrain them.
GRANT USAGE ON SCHEMA app TO shipped_app;
-- +goose StatementEnd

-- +goose StatementBegin
-- Read access to the Better-Auth-owned schema (membership/role lookups for
-- authz). The Go API reads auth.* but never migrates it.
GRANT USAGE ON SCHEMA auth TO shipped_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
REVOKE USAGE ON SCHEMA auth FROM shipped_app;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE USAGE ON SCHEMA app FROM shipped_app;
-- +goose StatementEnd

-- +goose StatementBegin
-- Drop only the app schema we own. We never drop `auth` (Better Auth owns it),
-- and we leave the `shipped_app` role in place (it may be shared / externally
-- provisioned, and roles are cluster-global). Operators can DROP ROLE manually.
DROP SCHEMA IF EXISTS app CASCADE;
-- +goose StatementEnd
