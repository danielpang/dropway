-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0007_audit_log_columns.sql
--
-- Phase 4 (security/ops hardening): widen app.audit_log so a row can carry the
-- full provenance of a sensitive mutation (ARCHITECTURE.md §10 audit logging +
-- §2.3 observability "request_id correlated edge→Go→Postgres"):
--
--   * actor_token  — the deploy-token id when the actor authenticated with a
--     deploy token rather than a user session (so a CI-driven change is
--     attributable to the token, not a null user). Nullable; null for a normal
--     user-session actor (actor_user carries the user id then).
--   * request_id   — the per-request correlation id (chi RequestID / the inbound
--     X-Request-Id we now propagate). Threads the audit row to the structured
--     access log + edge/Worker logs for one request.
--   * trace_id     — an optional distributed-trace id (same value as request_id
--     when no external tracer is wired; a cheap hook for end-to-end tracing
--     without a heavy OTel dependency).
--
-- These are purely additive nullable text columns, so the migration is backward
-- compatible: existing rows keep their values and the new columns default to
-- NULL. RLS/GRANT plumbing for app.audit_log already exists (migration 0003); the
-- tenant-isolation policy applies to these columns unchanged.

-- +goose Up

-- +goose StatementBegin
-- The deploy-token id (app.deploy_tokens.id) when a deploy token drove the action.
ALTER TABLE app.audit_log
    ADD COLUMN actor_token uuid;
-- +goose StatementEnd

-- +goose StatementBegin
-- The per-request correlation id (request_id from the structured log middleware /
-- the propagated X-Request-Id header).
ALTER TABLE app.audit_log
    ADD COLUMN request_id text;
-- +goose StatementEnd

-- +goose StatementBegin
-- An optional distributed-trace id (cheap end-to-end tracing hook; equals
-- request_id when no external tracer is configured).
ALTER TABLE app.audit_log
    ADD COLUMN trace_id text;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
ALTER TABLE app.audit_log DROP COLUMN IF EXISTS trace_id;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.audit_log DROP COLUMN IF EXISTS request_id;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.audit_log DROP COLUMN IF EXISTS actor_token;
-- +goose StatementEnd
