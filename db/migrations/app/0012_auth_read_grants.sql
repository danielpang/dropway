-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0012_auth_read_grants.sql
--
-- Better Auth (the dashboard) OWNS + creates the auth.* identity tables (user /
-- session / account / organization / member / invitation / jwks) as the privileged
-- auth-schema owner. The Go API reads some of them — auth.member (role re-check) and
-- auth.invitation (members-cap preflight) — as the restricted, non-BYPASSRLS
-- `shipped_app` role for authorization, so it needs SELECT.
--
-- We grant SELECT on any auth tables that already exist AND set ALTER DEFAULT
-- PRIVILEGES so tables Better Auth creates LATER are auto-readable. This migration
-- runs as the app-migration OWNER (DATABASE_OWNER_URL), which is the SAME role Better
-- Auth connects as (BETTER_AUTH_DATABASE_URL), so the default privileges apply to its
-- creations. The usual order is: app migrations (incl. this) → the Better Auth CLI
-- migrate. shipped_app already has USAGE on the schema (migration 0001); it gets only
-- SELECT here — never write/DDL, which stays with Better Auth.

-- +goose Up
-- +goose StatementBegin
GRANT SELECT ON ALL TABLES IN SCHEMA auth TO shipped_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA auth GRANT SELECT ON TABLES TO shipped_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA auth REVOKE SELECT ON TABLES FROM shipped_app;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT ON ALL TABLES IN SCHEMA auth FROM shipped_app;
-- +goose StatementEnd
