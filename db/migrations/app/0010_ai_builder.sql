-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0010_ai_builder.sql
--
-- AI website builder: chat sessions whose LLM (via OpenRouter) edits site files
-- inside a disposable sandbox. Each completed turn lands as an ordinary
-- immutable site_version (created_via='ai') that is never auto-published; the
-- user reviews it on a time-limited preview host and publishes by hand.
--
-- New tables:
--   ai_sessions  – one chat per site edit stream; sandbox id/expiry are cached
--                  here so a dead machine is lazily recreated on next message.
--   ai_messages  – full conversation transcript (OpenAI message shape in jsonb),
--                  per-session monotonic seq doubles as the SSE Last-Event-ID.
--   ai_usage     – append-only cost ledger, one row per OpenRouter generation.
--                  session_id is SET NULL on session deletion: billing rows
--                  outlive the conversation that produced them.
--
-- Extended tables:
--   site_versions – created_via ('deploy'|'ai') + preview_expires_at (NULL = no
--                   active preview; draft GC pins 'ai' versions for a retention
--                   window so an expired preview can be re-created cheaply).
--   host_routes   – gains kind ('canonical'|'custom'|'preview'); preview rows
--                   pin a version_id and carry expires_at, and join the
--                   KV-rebuildable-from-Postgres invariant.
--   org_meta      – ai_enabled kill switch (mirrors mcp_enabled) and the
--                   owner-adjustable monthly AI spend cap in USD.
--
-- RLS scopes all new tables to their org like every other tenant table.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE app.ai_sessions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id            uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    created_by         uuid NOT NULL,
    status             text NOT NULL DEFAULT 'active',
    model              text NOT NULL,
    sandbox_id         text,
    sandbox_expires_at timestamptz,
    base_version_id    uuid REFERENCES app.site_versions (id) ON DELETE SET NULL,
    latest_version_id  uuid REFERENCES app.site_versions (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    last_activity_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ai_sessions_status_check
        CHECK (status IN ('active', 'running', 'idle', 'archived', 'failed'))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Session lists per site and the per-org active-session concurrency count.
CREATE INDEX ai_sessions_org_site_idx ON app.ai_sessions USING btree (org_id, site_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Back the base/latest version FKs so version GC deletes never seq-scan.
CREATE INDEX ai_sessions_base_version_idx ON app.ai_sessions USING btree (base_version_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX ai_sessions_latest_version_idx ON app.ai_sessions USING btree (latest_version_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.ai_messages (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES app.ai_sessions (id) ON DELETE CASCADE,
    seq        integer NOT NULL,
    role       text NOT NULL,
    content    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ai_messages_session_seq_key UNIQUE (session_id, seq),
    CONSTRAINT ai_messages_role_check
        CHECK (role IN ('system', 'user', 'assistant', 'tool'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.ai_usage (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                   uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    session_id               uuid REFERENCES app.ai_sessions (id) ON DELETE SET NULL,
    model                    text NOT NULL,
    openrouter_generation_id text NOT NULL,
    prompt_tokens            bigint NOT NULL DEFAULT 0,
    completion_tokens        bigint NOT NULL DEFAULT 0,
    cost_usd                 numeric(12,6) NOT NULL,
    reported_to_billing_at   timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ai_usage_generation_key UNIQUE (openrouter_generation_id),
    CONSTRAINT ai_usage_cost_usd_check CHECK (cost_usd >= 0)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Spend-cap sums and the billing-period usage display.
CREATE INDEX ai_usage_org_created_idx ON app.ai_usage USING btree (org_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Back the session FK for the SET NULL on session deletion.
CREATE INDEX ai_usage_session_idx ON app.ai_usage USING btree (session_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Cloud meter-retry sweep: unreported rows only.
CREATE INDEX ai_usage_unreported_idx ON app.ai_usage USING btree (created_at)
    WHERE reported_to_billing_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.site_versions
    ADD COLUMN created_via text NOT NULL DEFAULT 'deploy'
        CONSTRAINT site_versions_created_via_check
            CHECK (created_via IN ('deploy', 'ai'));
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_versions ADD COLUMN preview_expires_at timestamptz;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.host_routes
    ADD COLUMN kind text NOT NULL DEFAULT 'canonical'
        CONSTRAINT host_routes_kind_check
            CHECK (kind IN ('canonical', 'custom', 'preview'));
-- +goose StatementEnd
-- +goose StatementBegin
-- Preview rows pin the exact draft version they serve; cascade so version GC
-- cannot leave a route pointing at deleted content (the KV key is cleaned by
-- the ops sweep, and the edge 410s on expires_at regardless).
ALTER TABLE app.host_routes
    ADD COLUMN version_id uuid REFERENCES app.site_versions (id) ON DELETE CASCADE;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.host_routes ADD COLUMN expires_at timestamptz;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX host_routes_version_idx ON app.host_routes USING btree (version_id);
-- +goose StatementEnd
-- +goose StatementBegin
-- Expired-preview sweep.
CREATE INDEX host_routes_preview_expiry_idx ON app.host_routes USING btree (expires_at)
    WHERE kind = 'preview';
-- +goose StatementEnd

-- +goose StatementBegin
-- Preview hosts pin a specific version; resolve_host must surface the pinned
-- version_id instead of the site's live pointer so the /authz exchange for a
-- gated preview host mints a token for the draft actually being served.
CREATE OR REPLACE FUNCTION app.resolve_host(p_host text) RETURNS TABLE(host text, site_id uuid, org_id uuid, slug text, access_mode text, version_id uuid)
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
    AS $$
    SELECT hr.host, s.id, s.org_id, s.slug, s.access_mode,
           COALESCE(hr.version_id, s.current_version_id)
    FROM app.host_routes hr
    JOIN app.sites s ON s.id = hr.site_id
    WHERE hr.host = p_host;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.org_meta ADD COLUMN ai_enabled boolean NOT NULL DEFAULT true;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_meta ADD COLUMN ai_monthly_cap_usd numeric(10,2) NOT NULL DEFAULT 20.00;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.ai_sessions ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.ai_sessions FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY ai_sessions_tenant_isolation ON app.ai_sessions
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.ai_sessions TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.ai_messages ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.ai_messages FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY ai_messages_tenant_isolation ON app.ai_messages
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.ai_messages TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.ai_usage ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.ai_usage FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY ai_usage_tenant_isolation ON app.ai_usage
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.ai_usage TO dropway_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.resolve_host(p_host text) RETURNS TABLE(host text, site_id uuid, org_id uuid, slug text, access_mode text, version_id uuid)
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
    AS $$
    SELECT hr.host, s.id, s.org_id, s.slug, s.access_mode, s.current_version_id
    FROM app.host_routes hr
    JOIN app.sites s ON s.id = hr.site_id
    WHERE hr.host = p_host;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS ai_monthly_cap_usd;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS ai_enabled;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS app.host_routes_preview_expiry_idx;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS app.host_routes_version_idx;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.host_routes DROP COLUMN IF EXISTS expires_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.host_routes DROP COLUMN IF EXISTS version_id;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.host_routes DROP COLUMN IF EXISTS kind;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_versions DROP COLUMN IF EXISTS preview_expires_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_versions DROP COLUMN IF EXISTS created_via;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.ai_usage;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.ai_messages;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.ai_sessions;
-- +goose StatementEnd
