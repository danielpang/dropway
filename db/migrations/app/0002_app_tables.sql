-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0002_app_tables.sql
--
-- The `app` schema tables (ARCHITECTURE.md §5 data model). Every TENANT-scoped
-- table carries a DENORMALIZED `org_id uuid NOT NULL` and composite indexes that
-- LEAD ON org_id, so the RLS predicate `org_id = current_org` is index-backed and
-- subquery-free on the hot path.
--
-- FK note: app tables conceptually attach to a tenant via `org_meta.id`, which is
-- itself `= auth.organization.id` (Better-Auth-owned). We FK app tables to
-- app.org_meta(id) -- NOT directly to auth.organization -- so the Go-owned schema
-- has a self-contained referential graph it fully controls, while org_meta.id is
-- populated to mirror organization.id (see ARCHITECTURE.md §5).
--
-- We do NOT hard-depend on any extension for UUID generation: callers
-- (Better Auth for org ids, the Go API for everything else) supply ids, and we
-- default to gen_random_uuid() (pgcrypto, present by default on PG13+ via the
-- built-in `pg_catalog` function on PG16) only as a convenience.

-- +goose Up

-- +goose StatementBegin
-- org_meta: the app-side anchor for an org. PK == Better Auth organization.id.
-- Holds the cached plan tier plus the org-level sharing policy (§5.4). This row
-- is NOT itself FORCE-RLS-filtered by org_id in the usual tenant sense because
-- its PK *is* the org id; the policy in 0003 still scopes it to current_org.
CREATE TABLE app.org_meta (
    id                     uuid PRIMARY KEY, -- = auth.organization.id (Better Auth)
    plan_tier              text NOT NULL DEFAULT 'free',
    allow_external_sharing boolean NOT NULL DEFAULT false,
    default_visibility     text NOT NULL DEFAULT 'public',
    created_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- org_usage: per-org counter rows backing the hard-cap quota gate (§5 quota race
-- safety). The Go/cloud quota provider does SELECT ... FOR UPDATE on this row,
-- checks the cap, increments, then inserts -- serializing per-org creates.
CREATE TABLE app.org_usage (
    org_id        uuid PRIMARY KEY REFERENCES app.org_meta (id) ON DELETE CASCADE,
    members_count int NOT NULL DEFAULT 0,
    sites_count   int NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- sites: a shareable static site owned by a user inside an org.
-- current_version_id points at the live version; the FK is added separately
-- below as DEFERRABLE INITIALLY DEFERRED to break the sites<->site_versions cycle.
CREATE TABLE app.sites (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    slug               text NOT NULL,
    owner_user_id      uuid NOT NULL, -- auth.user.id (Better Auth); not FK'd (cross-schema, read-only target)
    access_mode        text NOT NULL DEFAULT 'public'
                           CHECK (access_mode IN ('public', 'password', 'allowlist', 'org_only')),
    current_version_id uuid, -- nullable until first publish; deferrable FK added below
    created_at         timestamptz NOT NULL DEFAULT now(),
    -- slug is unique within an org (the public host is org/site scoped).
    CONSTRAINT sites_org_slug_key UNIQUE (org_id, slug)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- site_versions: immutable, content-addressed deploys. version_no is monotonic
-- per site; content_hash dedups identical content per site.
CREATE TABLE app.site_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id      uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    version_no   int NOT NULL,
    status       text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'uploading', 'ready', 'failed')),
    r2_prefix    text NOT NULL, -- manifests/<org>/<site>/<version> in R2
    content_hash text NOT NULL, -- sha256 of the deploy manifest (content digest)
    size_bytes   bigint NOT NULL DEFAULT 0,
    created_by   uuid NOT NULL, -- auth.user.id or a deploy-token service principal
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT site_versions_site_version_no_key UNIQUE (site_id, version_no),
    CONSTRAINT site_versions_site_content_hash_key UNIQUE (site_id, content_hash)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Deferrable FK closing the sites <-> site_versions cycle. INITIALLY DEFERRED so a
-- create-site + first-version + set-pointer can happen in a single transaction
-- without ordering pain (ARCHITECTURE.md §5).
ALTER TABLE app.sites
    ADD CONSTRAINT sites_current_version_id_fkey
        FOREIGN KEY (current_version_id)
        REFERENCES app.site_versions (id)
        DEFERRABLE INITIALLY DEFERRED;
-- +goose StatementEnd

-- +goose StatementBegin
-- domains: custom hostnames mapped to a site. hostname is GLOBALLY unique
-- (a host resolves to exactly one site across all tenants).
CREATE TABLE app.domains (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id       uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    hostname      text NOT NULL UNIQUE,
    verify_status text NOT NULL DEFAULT 'pending'
                      CHECK (verify_status IN ('pending', 'verified', 'failed')),
    tls_status    text NOT NULL DEFAULT 'pending'
                      CHECK (tls_status IN ('pending', 'issued', 'failed')),
    created_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- site_access_policy: per-site gating config (Phase 2 for non-public modes).
-- site_id is the PK (one policy per site). password_hash only for mode='password'.
CREATE TABLE app.site_access_policy (
    site_id       uuid PRIMARY KEY REFERENCES app.sites (id) ON DELETE CASCADE,
    org_id        uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    mode          text NOT NULL DEFAULT 'public'
                      CHECK (mode IN ('public', 'password', 'allowlist', 'org_only')),
    password_hash text, -- non-null only for mode='password'
    expires_at    timestamptz,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- allowlist_entries: pre-registration email grants for allowlist sites. A grant
-- is CLAIMED (claimed_at set) only when a verified Dropway account first matches
-- it (ARCHITECTURE.md §10). is_external marks grants outside the org's domain.
CREATE TABLE app.allowlist_entries (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id     uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    email       text NOT NULL,
    is_external boolean NOT NULL DEFAULT false,
    claimed_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT allowlist_entries_site_email_key UNIQUE (site_id, email)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- deploy_tokens: hashed bearer tokens for the CLI / CI deploy path. Optionally
-- scoped to a single site (least-privilege default, §10). Only the hash is stored.
CREATE TABLE app.deploy_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    scopes     text[] NOT NULL DEFAULT ARRAY['deploy']::text[],
    site_id    uuid REFERENCES app.sites (id) ON DELETE CASCADE, -- null = org-wide
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
-- +goose StatementEnd

-- +goose StatementBegin
-- audit_log: append-only record of sensitive actions. metadata is freeform jsonb.
CREATE TABLE app.audit_log (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    actor_user uuid, -- auth.user.id; null for system/service actors
    action     text NOT NULL,
    target     text, -- e.g. "site:<id>", "member:<id>"
    metadata   jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip         inet,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Composite indexes: every tenant table gets an index LEADING ON org_id so the
-- RLS predicate is index-backed, plus the natural access paths.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE INDEX sites_org_id_created_at_idx ON app.sites (org_id, created_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX sites_org_id_owner_idx ON app.sites (org_id, owner_user_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX site_versions_org_id_site_id_idx ON app.site_versions (org_id, site_id, version_no DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX domains_org_id_site_id_idx ON app.domains (org_id, site_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX site_access_policy_org_id_idx ON app.site_access_policy (org_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX allowlist_entries_org_id_site_id_idx ON app.allowlist_entries (org_id, site_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Lookup path for claiming a grant by (site, verified email).
CREATE INDEX allowlist_entries_org_id_email_idx ON app.allowlist_entries (org_id, email);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX deploy_tokens_org_id_idx ON app.deploy_tokens (org_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX audit_log_org_id_created_at_idx ON app.audit_log (org_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down

-- Drop in reverse dependency order. The deferrable self-referential FK on sites
-- is dropped first so the two tables can be dropped independently.
-- +goose StatementBegin
ALTER TABLE app.sites DROP CONSTRAINT IF EXISTS sites_current_version_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS app.audit_log;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.deploy_tokens;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.allowlist_entries;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.site_access_policy;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.domains;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.site_versions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.sites;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.org_usage;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.org_meta;
-- +goose StatementEnd
