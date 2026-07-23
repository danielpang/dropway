-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0017_org_memory.sql
--
-- Org memory ("your agent knows your company"): a per-org store of durable
-- facts distilled from AI-builder transcripts, shared chat logs, skills, and
-- published site content, retrieved into the builder's context and exposed via
-- the API/MCP/CLI. Embeddings live in pgvector; content is the source of truth
-- and embeddings are derived data (re-embeddable per row via embedding_model).
--
-- New tables:
--   org_memories       – one row per durable fact. Deduped per org on the
--                        normalized-content hash; pinned rows are always
--                        injected; disabled rows are never retrieved but are
--                        kept so extraction can't resurrect them.
--   org_memory_ingests – extraction watermarks (one row per processed source),
--                        making extraction idempotent, incremental, and
--                        crash-safe (resume from through_seq).
--   org_content_chunks – embedded chunks of published site versions and skill
--                        files for cross-site/skill retrieval. Rows ride their
--                        source's lifecycle via ON DELETE CASCADE.
--
-- Extended tables:
--   org_meta – memory_enabled kill switch (mirrors ai_enabled/mcp_enabled but
--              defaults FALSE: the feature rolls out opt-in per org).
--
-- RLS scopes all new tables to their org like every other tenant table.

-- +goose Up
-- +goose StatementBegin
-- pgvector provides the vector type + cosine operators + HNSW indexes. The
-- extension installs into the default schema (public on self-host, extensions
-- on Supabase); the type is resolvable from app-schema queries either way.
CREATE EXTENSION IF NOT EXISTS vector;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.org_memories (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    kind            text NOT NULL DEFAULT 'fact'
                        CHECK (kind IN ('fact', 'preference', 'style', 'correction', 'manual')),
    content         text NOT NULL,
    -- sha256 of the normalized content; the per-org unique key below makes
    -- re-extraction of the same fact an update, never a duplicate.
    content_hash    text NOT NULL,
    embedding       vector(1536),
    embedding_model text NOT NULL,
    source_kind     text NOT NULL DEFAULT 'manual'
                        CHECK (source_kind IN ('ai_session', 'chat_log', 'site_version', 'manual')),
    -- No FK: a memory outlives the session/chat/version it was learned from.
    source_id       uuid,
    -- Attribution for externally added rows ('cursor', 'claude-code', 'cli', …),
    -- mirroring chat_logs.source_tool. NULL for extracted/dashboard rows.
    source_tool     text,
    pinned          boolean NOT NULL DEFAULT false,
    disabled        boolean NOT NULL DEFAULT false,
    created_by      uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    last_used_at    timestamptz,
    CONSTRAINT org_memories_org_hash_key UNIQUE (org_id, content_hash)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- List/UI ordering: pinned first, then most recently refreshed.
CREATE INDEX org_memories_org_idx ON app.org_memories USING btree (org_id, pinned DESC, updated_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
-- ANN retrieval. Queries always carry the org filter (RLS + explicit WHERE);
-- at expected per-org cardinality (≤ thousands) recall stays high.
CREATE INDEX org_memories_embedding_idx ON app.org_memories USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.org_memory_ingests (
    org_id      uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    source_kind text NOT NULL CHECK (source_kind IN ('ai_session', 'chat_log', 'site_version', 'skill')),
    source_id   uuid NOT NULL,
    -- Highest transcript seq already extracted (sessions/chats); content
    -- sources use 0/1 as a processed marker.
    through_seq bigint NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, source_kind, source_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.org_content_chunks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    source_kind     text NOT NULL CHECK (source_kind IN ('site_version', 'skill')),
    -- Site chunks pin the immutable version (and cache the site for filters);
    -- skill chunks pin the skill. CASCADE ties chunk lifetime to the source,
    -- so version GC / skill deletion reclaims chunks with no extra sweep.
    version_id      uuid REFERENCES app.site_versions (id) ON DELETE CASCADE,
    site_id         uuid REFERENCES app.sites (id) ON DELETE CASCADE,
    skill_id        uuid REFERENCES app.skills (id) ON DELETE CASCADE,
    path            text NOT NULL,
    chunk_seq       integer NOT NULL,
    content         text NOT NULL,
    embedding       vector(1536),
    embedding_model text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT org_content_chunks_source_check CHECK (
        (source_kind = 'site_version' AND version_id IS NOT NULL AND skill_id IS NULL)
        OR (source_kind = 'skill' AND skill_id IS NOT NULL AND version_id IS NULL)
    )
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX org_content_chunks_version_key ON app.org_content_chunks (version_id, path, chunk_seq) WHERE version_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX org_content_chunks_skill_key ON app.org_content_chunks (skill_id, path, chunk_seq) WHERE skill_id IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX org_content_chunks_org_idx ON app.org_content_chunks USING btree (org_id, source_kind, created_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX org_content_chunks_embedding_idx ON app.org_content_chunks USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
-- Opt-in rollout: memory ships dark and is enabled per org from settings.
ALTER TABLE app.org_meta ADD COLUMN memory_enabled boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.org_memories ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_memories FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY org_memories_tenant_isolation ON app.org_memories
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.org_memories TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.org_memory_ingests ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_memory_ingests FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY org_memory_ingests_tenant_isolation ON app.org_memory_ingests
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.org_memory_ingests TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.org_content_chunks ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_content_chunks FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY org_content_chunks_tenant_isolation ON app.org_content_chunks
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.org_content_chunks TO dropway_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS memory_enabled;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.org_content_chunks;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.org_memory_ingests;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.org_memories;
-- +goose StatementEnd
-- The vector extension is deliberately NOT dropped on Down: other objects may
-- have come to depend on it, and re-creating it is a no-op on re-Up.
