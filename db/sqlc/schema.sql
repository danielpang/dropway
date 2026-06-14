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
-- (ARCHITECTURE.md §8: sqlc → Go types from the Go-owned app schema.)

CREATE SCHEMA IF NOT EXISTS app;

-- org_meta: the app-side anchor for an org. PK == Better Auth organization.id.
CREATE TABLE app.org_meta (
    id                     uuid PRIMARY KEY,
    plan_tier              text NOT NULL DEFAULT 'free',
    allow_external_sharing boolean NOT NULL DEFAULT false,
    default_visibility     text NOT NULL DEFAULT 'org_only',
    created_at             timestamptz NOT NULL DEFAULT now()
);

-- org_usage: per-org counter rows backing the hard-cap quota gate.
CREATE TABLE app.org_usage (
    org_id        uuid PRIMARY KEY REFERENCES app.org_meta (id) ON DELETE CASCADE,
    members_count int NOT NULL DEFAULT 0,
    sites_count   int NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- sites: a shareable static site owned by a user inside an org.
CREATE TABLE app.sites (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    slug               text NOT NULL,
    owner_user_id      uuid NOT NULL,
    access_mode        text NOT NULL DEFAULT 'public',
    current_version_id uuid,
    created_at         timestamptz NOT NULL DEFAULT now(),
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
    CONSTRAINT site_versions_site_version_no_key UNIQUE (site_id, version_no),
    CONSTRAINT site_versions_site_content_hash_key UNIQUE (site_id, content_hash)
);

-- Deferrable FK closing the sites <-> site_versions cycle.
ALTER TABLE app.sites
    ADD CONSTRAINT sites_current_version_id_fkey
        FOREIGN KEY (current_version_id)
        REFERENCES app.site_versions (id)
        DEFERRABLE INITIALLY DEFERRED;

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

-- deploy_tokens: hashed bearer tokens for the CLI / CI deploy path.
CREATE TABLE app.deploy_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    scopes     text[] NOT NULL DEFAULT ARRAY['deploy']::text[],
    site_id    uuid REFERENCES app.sites (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
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
    created_at timestamptz NOT NULL DEFAULT now()
);

-- resolve_host: RLS-bypassing host → site resolver for the /authz exchange
-- (migration 0006). SECURITY DEFINER so a content host shared cross-org still
-- resolves; returns only routing fields (no secrets). Mirror for sqlc typing.
CREATE FUNCTION app.resolve_host(p_host text)
    RETURNS TABLE (
        host        text,
        site_id     uuid,
        org_id      uuid,
        slug        text,
        access_mode text,
        version_id  uuid
    )
    LANGUAGE sql
    STABLE
AS $$
    SELECT hr.host, s.id, s.org_id, s.slug, s.access_mode, s.current_version_id
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
