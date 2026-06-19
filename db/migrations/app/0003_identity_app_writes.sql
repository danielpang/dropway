-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0003_identity_app_writes.sql
--
-- Single-role deployments run Better Auth on the SAME `dropway_app` role the Go API
-- and MCP server use (BETTER_AUTH_DATABASE_URL pointed at the dropway_app DSN, or
-- unset so it falls back to DATABASE_URL). Better Auth must INSERT/UPDATE/DELETE its
-- own identity rows (verification, session, account, user, jwks, organization, …) —
-- e.g. Google sign-in writes a `verification` row for the OAuth/PKCE state — but the
-- baseline (0001) grants dropway_app only SELECT on identity (it was scoped to the
-- API's read-only authz path). Without write access the sign-in 500s with
-- `permission denied for table verification` (SQLSTATE 42501).
--
-- This lifts dropway_app to full DML on the identity schema. The grant must be run by
-- the OWNER role that owns/creates the identity tables (goose runs as
-- DATABASE_OWNER_URL) so the forward-looking ALTER DEFAULT PRIVILEGES below applies to
-- tables Better Auth's migrate creates LATER as that same owner — mirroring how 0001
-- granted the SELECT default.
--
-- TRADE-OFF: dropway_app is shared with the API/MCP runtime, so they too gain identity
-- write here. The two-role split (a separate privileged owner DSN for Better Auth)
-- keeps that boundary; this is the single-role simplification. DDL is unaffected:
-- dropway_app still lacks CREATE on identity, so `better-auth migrate` must keep
-- running as the owner role.

-- +goose Up
-- +goose StatementBegin
GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA identity TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA identity GRANT INSERT, UPDATE, DELETE ON TABLES TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA identity GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO dropway_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA identity REVOKE INSERT, UPDATE, DELETE ON TABLES FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA identity REVOKE USAGE, SELECT, UPDATE ON SEQUENCES FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA identity FROM dropway_app;
-- +goose StatementEnd
