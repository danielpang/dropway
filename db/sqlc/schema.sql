-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- db/sqlc/schema.sql
--
-- The Up-only DDL that sqlc compiles its type information against. This MIRRORS
-- the Go-owned `app` schema produced by db/migrations/app/*.sql (goose), but is
-- stripped of:
--   * goose annotations (-- +goose Up/Down/StatementBegin/StatementEnd)
--   * every Down / DROP (sqlc must only ever see the final, applied shape)
--   * the role/GRANT/RLS plumbing (irrelevant to query type inference)
--
-- It is NOT applied to any database — goose owns migrations. Keep it in lock-step
-- with db/migrations/app whenever a table/column the queries touch changes.
-- (sqlc → Go types from the Go-owned app schema.)

CREATE SCHEMA IF NOT EXISTS app;

-- org_meta: the app-side anchor for an org. PK == Better Auth organization.id.
CREATE TABLE app.org_meta (
    id                     uuid PRIMARY KEY,
    plan_tier              text NOT NULL DEFAULT 'free',
    allow_external_sharing boolean NOT NULL DEFAULT false,
    default_visibility     text NOT NULL DEFAULT 'org_only',
    created_at             timestamptz NOT NULL DEFAULT now(),
    org_status             text NOT NULL DEFAULT 'active'
                               CHECK (org_status IN ('active', 'suspended', 'over_limit')),
    mcp_enabled            boolean NOT NULL DEFAULT true,
    -- Guards the lazy per-org seeding of default skill folders + preset skills
    -- (migration 0008): set true in the same tx that seeds.
    skills_seeded          boolean NOT NULL DEFAULT false,
    -- AI builder kill switch (mirrors mcp_enabled) + owner-adjustable monthly
    -- AI spend cap in USD (migration 0010).
    ai_enabled             boolean NOT NULL DEFAULT true,
    ai_monthly_cap_usd     numeric(10,2) NOT NULL DEFAULT 20.00,
    -- Chat-log (Share This Session) kill switch (migration 0013).
    chat_logs_enabled      boolean NOT NULL DEFAULT true
);

-- org_usage: per-org counter rows backing the hard-cap quota gate.
CREATE TABLE app.org_usage (
    org_id        uuid PRIMARY KEY REFERENCES app.org_meta (id) ON DELETE CASCADE,
    members_count int NOT NULL DEFAULT 0,
    sites_count   int NOT NULL DEFAULT 0,
    storage_bytes bigint NOT NULL DEFAULT 0 CHECK (storage_bytes >= 0),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- org_blobs: the per-org set of stored, content-addressed blobs (one row per
-- distinct org_id+content_hash). SUM(size_bytes) is the org's dedup-aware storage;
-- org_usage.storage_bytes is the maintained running total.
CREATE TABLE app.org_blobs (
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    content_hash text NOT NULL,
    size_bytes   bigint NOT NULL CHECK (size_bytes >= 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, content_hash)
);

-- sites: a shareable static site owned by a user inside an org.
CREATE TABLE app.sites (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    slug               text NOT NULL,
    owner_user_id      uuid NOT NULL,
    access_mode        text NOT NULL DEFAULT 'public',
    current_version_id uuid,
    -- Discovery axis (orthogonal to access_mode): does this site show up in the
    -- org feed. Defaults to true (auto-shared); the owner flips it false to keep
    -- the site private (off the feed). See migration 0005.
    feed_visible       boolean NOT NULL DEFAULT true,
    -- Optional human-facing feed metadata the owner sets (null → fall back to slug).
    title              text,
    description        text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    -- Collaboration toggle (migration 0014): true (default) lets any org member
    -- modify content; false restricts content edits to creator-or-admin.
    allow_member_edits boolean NOT NULL DEFAULT true,
    CONSTRAINT sites_org_slug_key UNIQUE (org_id, slug)
);

-- site_versions: immutable, content-addressed deploys.
CREATE TABLE app.site_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id      uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    version_no   int NOT NULL,
    status       text NOT NULL DEFAULT 'pending',
    r2_prefix    text NOT NULL,
    content_hash text NOT NULL,
    size_bytes   bigint NOT NULL DEFAULT 0,
    created_by   uuid NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- created_via: 'ai' marks drafts produced by the AI builder (migration 0010);
    -- draft GC pins them for a retention window. preview_expires_at is the active
    -- preview-host deadline (NULL = no active preview).
    created_via  text NOT NULL DEFAULT 'deploy'
                     CHECK (created_via IN ('deploy', 'ai')),
    preview_expires_at timestamptz,
    CONSTRAINT site_versions_site_version_no_key UNIQUE (site_id, version_no),
    CONSTRAINT site_versions_site_content_hash_key UNIQUE (site_id, content_hash)
);

-- Deferrable FK closing the sites <-> site_versions cycle.
ALTER TABLE app.sites
    ADD CONSTRAINT sites_current_version_id_fkey
        FOREIGN KEY (current_version_id)
        REFERENCES app.site_versions (id)
        DEFERRABLE INITIALLY DEFERRED;

-- skills: a shareable Claude skill (SKILL.md + supporting files) owned by a
-- user inside an org (migration 0008). owner_user_id
-- 00000000-0000-0000-0000-000000000000 = seeded by Dropway.
CREATE TABLE app.skills (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    slug               text NOT NULL,
    owner_user_id      uuid NOT NULL,
    title              text,
    description        text,
    current_version_id uuid,
    -- feed_visible (migration 0009): a skill auto-joins the org feed on publish
    -- unless the owner/admin makes it private (mirrors sites.feed_visible).
    feed_visible       boolean NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now(),
    -- Collaboration toggle (migration 0014, mirrors sites.allow_member_edits).
    allow_member_edits boolean NOT NULL DEFAULT true,
    CONSTRAINT skills_org_slug_key UNIQUE (org_id, slug)
);

-- skill_versions: immutable, content-addressed skill uploads (shape of
-- site_versions; v1 exposes only the current one).
CREATE TABLE app.skill_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    skill_id     uuid NOT NULL REFERENCES app.skills (id) ON DELETE CASCADE,
    version_no   int NOT NULL,
    status       text NOT NULL DEFAULT 'pending',
    content_hash text NOT NULL,
    size_bytes   bigint NOT NULL DEFAULT 0,
    created_by   uuid NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT skill_versions_skill_version_no_key UNIQUE (skill_id, version_no),
    CONSTRAINT skill_versions_skill_content_hash_key UNIQUE (skill_id, content_hash)
);

-- Deferrable FK closing the skills <-> skill_versions cycle.
ALTER TABLE app.skills
    ADD CONSTRAINT skills_current_version_id_fkey
        FOREIGN KEY (current_version_id)
        REFERENCES app.skill_versions (id)
        DEFERRABLE INITIALLY DEFERRED;

-- skill_folders: admin-curated org taxonomy for skills (defaults: engineering,
-- product, marketing — seeded lazily per org).
CREATE TABLE app.skill_folders (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    slug       text NOT NULL,
    title      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT skill_folders_org_slug_key UNIQUE (org_id, slug)
);

-- skill_folder_items: folder membership; is_preset marks the admin-curated
-- starter set that bulk "download the presets" surfaces.
CREATE TABLE app.skill_folder_items (
    org_id    uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    folder_id uuid NOT NULL REFERENCES app.skill_folders (id) ON DELETE CASCADE,
    skill_id  uuid NOT NULL REFERENCES app.skills (id) ON DELETE CASCADE,
    is_preset boolean NOT NULL DEFAULT false,
    added_by  uuid NOT NULL,
    added_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (folder_id, skill_id)
);
CREATE INDEX skill_folder_items_skill_idx ON app.skill_folder_items (skill_id);
CREATE INDEX skills_current_version_idx ON app.skills (current_version_id);

-- domains: custom hostnames mapped to a site. hostname is GLOBALLY unique.
-- cf_hostname_id / dcv_record track the Cloudflare-for-SaaS custom hostname and
-- the DNS DCV record the user must create (migration 0006). verify_status also
-- carries the intermediate 'verifying' state.
CREATE TABLE app.domains (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id        uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    hostname       text NOT NULL UNIQUE,
    verify_status  text NOT NULL DEFAULT 'pending',
    tls_status     text NOT NULL DEFAULT 'pending',
    cf_hostname_id text,
    dcv_record     text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- site_access_policy: per-site gating config (Phase 2 for non-public modes).
CREATE TABLE app.site_access_policy (
    site_id       uuid PRIMARY KEY REFERENCES app.sites (id) ON DELETE CASCADE,
    org_id        uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    mode          text NOT NULL DEFAULT 'public',
    password_hash text,
    expires_at    timestamptz,
    unlisted      boolean NOT NULL DEFAULT false,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- post_comments: polymorphic org-internal discussion on a feed post (a site or a
-- skill), with @mentions (migration 0009). subject_type is 'site' | 'skill'.
CREATE TABLE app.post_comments (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    subject_type       text NOT NULL,
    subject_id         uuid NOT NULL,
    author_user_id     uuid NOT NULL,
    body               text NOT NULL,
    mentioned_user_ids uuid[] NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT post_comments_subject_type_check CHECK (subject_type IN ('site', 'skill'))
);

-- post_votes: polymorphic up/down votes on a feed post (one per subject per user).
CREATE TABLE app.post_votes (
    subject_type text NOT NULL,
    subject_id   uuid NOT NULL,
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL,
    value        smallint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subject_type, subject_id, user_id),
    CONSTRAINT post_votes_subject_type_check CHECK (subject_type IN ('site', 'skill')),
    CONSTRAINT post_votes_value_check CHECK (value IN (-1, 1))
);

-- allowlist_entries: pre-registration email grants for allowlist sites.
CREATE TABLE app.allowlist_entries (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id            uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    email              text NOT NULL,
    is_external        boolean NOT NULL DEFAULT false,
    claimed_at         timestamptz,
    claimed_by_user_id uuid,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT allowlist_entries_site_email_key UNIQUE (site_id, email)
);

-- audit_log: append-only record of sensitive actions. actor_token / request_id /
-- trace_id added in migration 0007 (Phase 4 audit + tracing provenance).
CREATE TABLE app.audit_log (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    actor_user  uuid,
    actor_token uuid,
    action      text NOT NULL,
    target      text,
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip          inet,
    request_id  text,
    trace_id    text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- host_routes: GLOBAL host -> owning (org, site) registry. host is the PRIMARY
-- KEY so a conflicting insert from any org raises 23505 (surfaced as
-- ErrHostTaken), enforcing global host uniqueness above the per-(org,slug) site
-- constraint. (Mirror of migration 0005; RLS/GRANT plumbing omitted per this
-- file's convention.)
CREATE TABLE app.host_routes (
    host       text PRIMARY KEY,
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id    uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Preview routes (migration 0010): kind='preview' rows pin the draft
    -- version they serve and expire at expires_at (edge 410s past it).
    kind       text NOT NULL DEFAULT 'canonical'
                   CHECK (kind IN ('canonical', 'custom', 'preview')),
    version_id uuid REFERENCES app.site_versions (id) ON DELETE CASCADE,
    expires_at timestamptz
);

-- ai_sessions: one AI-builder chat per site edit stream (migration 0010). The
-- sandbox id/expiry are cached so a dead machine is lazily recreated on the
-- next message; base/latest version ids tie the chat to the drafts it produced.
CREATE TABLE app.ai_sessions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id            uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    created_by         uuid NOT NULL,
    status             text NOT NULL DEFAULT 'active'
                           CHECK (status IN ('active', 'running', 'idle', 'archived', 'failed')),
    model              text NOT NULL,
    sandbox_id         text,
    sandbox_expires_at timestamptz,
    base_version_id    uuid REFERENCES app.site_versions (id) ON DELETE SET NULL,
    latest_version_id  uuid REFERENCES app.site_versions (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    last_activity_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_sessions_org_site_idx ON app.ai_sessions (org_id, site_id, created_at DESC);

-- ai_messages: the conversation transcript. content is the OpenAI message
-- shape (incl. tool calls / truncated tool results); seq is per-session
-- monotonic and doubles as the SSE Last-Event-ID for resume.
CREATE TABLE app.ai_messages (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES app.ai_sessions (id) ON DELETE CASCADE,
    seq        integer NOT NULL,
    role       text NOT NULL CHECK (role IN ('system', 'user', 'assistant', 'tool')),
    content    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ai_messages_session_seq_key UNIQUE (session_id, seq)
);

-- ai_usage: append-only AI cost ledger, one row per OpenRouter generation.
-- session_id is SET NULL on session deletion (billing rows outlive chats);
-- reported_to_billing_at is NULL until the cloud Stripe meter event is acked.
CREATE TABLE app.ai_usage (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                   uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    session_id               uuid REFERENCES app.ai_sessions (id) ON DELETE SET NULL,
    model                    text NOT NULL,
    openrouter_generation_id text NOT NULL UNIQUE,
    prompt_tokens            bigint NOT NULL DEFAULT 0,
    completion_tokens        bigint NOT NULL DEFAULT 0,
    cost_usd                 numeric(12,6) NOT NULL CHECK (cost_usd >= 0),
    reported_to_billing_at   timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_usage_org_created_idx ON app.ai_usage (org_id, created_at);

-- resolve_host: RLS-bypassing host → site resolver for the /authz exchange
-- (migration 0006). SECURITY DEFINER so a content host shared cross-org still
-- resolves; returns only routing fields (no secrets). Since migration 0010 a
-- preview route's pinned version_id wins over the live pointer. Mirror for
-- sqlc typing.
CREATE FUNCTION app.resolve_host(p_host text)
    RETURNS TABLE (
        host               text,
        site_id            uuid,
        org_id             uuid,
        slug               text,
        access_mode        text,
        version_id         uuid,
        preview_expires_at timestamptz
    )
    LANGUAGE sql
    STABLE
AS $$
    SELECT hr.host, s.id, s.org_id, s.slug, s.access_mode,
           COALESCE(hr.version_id, s.current_version_id),
           CASE WHEN hr.kind = 'preview' THEN hr.expires_at ELSE NULL END
    FROM app.host_routes hr
    JOIN app.sites s ON s.id = hr.site_id
    WHERE hr.host = p_host;
$$;

-- all_org_ids: RLS-bypassing system enumeration of org ids for cross-org jobs (DR
-- rebuild + R2 GC). SECURITY DEFINER, returns only ids (no secrets). OPS-ONLY: the
-- body RAISES unless app.ops_mode='1' (migration 0009), so a normal request can't
-- enumerate all org ids; the DR/GC path sets the GUC. Mirror of migrations 0008 +
-- 0009 for sqlc typing (the function is called via raw pgx in the store).
CREATE FUNCTION app.all_org_ids()
    RETURNS TABLE (id uuid)
    LANGUAGE plpgsql
    STABLE
    SECURITY DEFINER
    SET search_path = app, pg_temp
AS $$
BEGIN
    IF current_setting('app.ops_mode', true) IS DISTINCT FROM '1' THEN
        RAISE EXCEPTION 'app.all_org_ids() is ops-only; set app.ops_mode=1 (DR rebuild / GC path)'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN QUERY SELECT om.id FROM app.org_meta om ORDER BY om.created_at;
END;
$$;

-- chat_logs: a shared chat log (Share This Session, migration 0013) — an
-- append-only conversation history with OPTIONAL, re-pointable site
-- attachment (one attached log per site via the partial unique index).
-- next_seq keeps message seq monotonic across window pruning.
CREATE TABLE app.chat_logs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id       uuid REFERENCES app.sites (id) ON DELETE SET NULL,
    title         text NOT NULL DEFAULT '',
    source_tool   text NOT NULL DEFAULT 'other',
    panel_enabled boolean NOT NULL DEFAULT true,
    next_seq      integer NOT NULL DEFAULT 1,
    created_by    uuid NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- Collaboration toggle (migration 0014, mirrors sites.allow_member_edits).
    allow_member_edits boolean NOT NULL DEFAULT true
);
CREATE UNIQUE INDEX chat_logs_site_key ON app.chat_logs (site_id) WHERE site_id IS NOT NULL;

-- chat_messages: one message per row. kind 'chat' = conversation turn;
-- 'action' = LLM-authored annotation about work performed (meta jsonb
-- {action, tool, paths}). version_id stamps the site's current deploy at
-- append time (NULL when unattached).
CREATE TABLE app.chat_messages (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    chat_log_id uuid NOT NULL REFERENCES app.chat_logs (id) ON DELETE CASCADE,
    seq         integer NOT NULL,
    version_id  uuid REFERENCES app.site_versions (id) ON DELETE SET NULL,
    created_by  uuid NOT NULL,
    role        text NOT NULL CHECK (role IN ('user', 'assistant')),
    kind        text NOT NULL DEFAULT 'chat' CHECK (kind IN ('chat', 'action')),
    content     text NOT NULL,
    meta        jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chat_messages_log_seq_key UNIQUE (chat_log_id, seq)
);
