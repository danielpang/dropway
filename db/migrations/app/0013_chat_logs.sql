-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0013_chat_logs.sql
--
-- Share This Session: a chat log is a first-class org object — an append-only
-- conversation history (turns + LLM action annotations) imported from Claude
-- Code / ChatGPT / Cursor / plain text. Site attachment is OPTIONAL and
-- re-pointable: attached, the log renders as the site's "How this was made"
-- panel under the site's own access control; unattached it is an org-internal
-- library entry with no viewer surface.
--
-- New tables:
--   chat_logs     – the aggregate: nullable, re-pointable site_id (one
--                   attached log per site via a partial unique index),
--                   panel_enabled (hide the pill without detaching), and a
--                   next_seq allocator so message seq numbers stay monotonic
--                   across pruning (the free tier's rolling window deletes
--                   oldest rows; numbers are never reused).
--   chat_messages – one message per row. kind 'chat' is a conversation turn;
--                   kind 'action' is an LLM-authored annotation about work
--                   performed (meta jsonb: {action, tool, paths}). version_id
--                   stamps the site's current deploy version at append time
--                   (NULL when unattached) so the viewer groups by version.
--
-- Extended tables:
--   org_meta – chat_logs_enabled kill switch (mirrors mcp_enabled/ai_enabled).
--
-- RLS scopes both new tables to their org like every other tenant table.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE app.chat_logs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id       uuid REFERENCES app.sites (id) ON DELETE SET NULL,
    title         text NOT NULL DEFAULT '',
    source_tool   text NOT NULL DEFAULT 'other',
    panel_enabled boolean NOT NULL DEFAULT true,
    next_seq      integer NOT NULL DEFAULT 1,
    created_by    uuid NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- One attached log per site; unattached logs (site_id NULL) are unbounded.
CREATE UNIQUE INDEX chat_logs_site_key ON app.chat_logs USING btree (site_id) WHERE site_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Library listing: an org's logs, newest first.
CREATE INDEX chat_logs_org_created_idx ON app.chat_logs USING btree (org_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.chat_messages (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    chat_log_id uuid NOT NULL REFERENCES app.chat_logs (id) ON DELETE CASCADE,
    seq         integer NOT NULL,
    version_id  uuid REFERENCES app.site_versions (id) ON DELETE SET NULL,
    created_by  uuid NOT NULL,
    role        text NOT NULL,
    kind        text NOT NULL DEFAULT 'chat',
    content     text NOT NULL,
    meta        jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chat_messages_log_seq_key UNIQUE (chat_log_id, seq),
    CONSTRAINT chat_messages_role_check CHECK (role IN ('user', 'assistant')),
    CONSTRAINT chat_messages_kind_check CHECK (kind IN ('chat', 'action'))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Backs the version_id FK: without it, every cascaded site_versions delete
-- (draft GC) runs the referential-integrity SET NULL as a full sequential
-- scan of app.chat_messages (as table owner, bypassing RLS → across all orgs).
CREATE INDEX chat_messages_version_idx ON app.chat_messages USING btree (version_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Org-level kill switch for the whole chat-log feature (governance toggle,
-- free on every tier — mirrors mcp_enabled / ai_enabled).
ALTER TABLE app.org_meta ADD COLUMN chat_logs_enabled boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.chat_logs ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.chat_logs FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY chat_logs_tenant_isolation ON app.chat_logs
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.chat_logs TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.chat_messages ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.chat_messages FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY chat_messages_tenant_isolation ON app.chat_messages
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.chat_messages TO dropway_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS chat_logs_enabled;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.chat_messages;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.chat_logs;
-- +goose StatementEnd
