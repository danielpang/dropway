-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- db/sqlc/query.sql
--
-- Typed queries for the Go API's `app` schema. sqlc generates a method per
-- annotated query into services/api/internal/store/db. Every query here runs
-- inside a per-request transaction that has already executed the SET LOCAL
-- tenant GUCs (see internal/store + internal/middleware/rlstx), so RLS scopes
-- each statement to the active org. These queries therefore carry NO explicit
-- org filter beyond what RLS enforces, except where we deliberately re-derive a
-- resource's org for the confused-deputy guard (ARCHITECTURE.md §5).

-- ===========================================================================
-- org provisioning
-- ===========================================================================

-- name: EnsureOrgMeta :exec
-- Idempotent upsert of the app-side org anchor (PK = Better Auth organization.id).
-- ON CONFLICT DO NOTHING keeps a re-provision a no-op (the dashboard may call
-- ensure-org on every request).
INSERT INTO app.org_meta (id)
VALUES ($1)
ON CONFLICT (id) DO NOTHING;

-- name: EnsureOrgUsage :exec
-- Idempotent upsert of the per-org counter row backing the quota gate.
INSERT INTO app.org_usage (org_id)
VALUES ($1)
ON CONFLICT (org_id) DO NOTHING;

-- name: GetOrgMeta :one
SELECT id, plan_tier, allow_external_sharing, default_visibility, created_at
FROM app.org_meta
WHERE id = $1;

-- name: GetOrgUsage :one
SELECT org_id, members_count, sites_count, updated_at
FROM app.org_usage
WHERE org_id = $1;

-- name: IncSiteCount :one
-- Bump the org's sites_count counter, returning the new value. Run inside the
-- create-site tx after the row is inserted.
UPDATE app.org_usage
SET sites_count = sites_count + 1,
    updated_at  = now()
WHERE org_id = $1
RETURNING sites_count;

-- ===========================================================================
-- sites
-- ===========================================================================

-- name: CreateSite :one
INSERT INTO app.sites (org_id, slug, owner_user_id, access_mode)
VALUES ($1, $2, $3, $4)
RETURNING id, org_id, slug, owner_user_id, access_mode, current_version_id, created_at;

-- name: GetSite :one
SELECT id, org_id, slug, owner_user_id, access_mode, current_version_id, created_at
FROM app.sites
WHERE id = $1;

-- name: ListSites :many
SELECT id, org_id, slug, owner_user_id, access_mode, current_version_id, created_at
FROM app.sites
ORDER BY created_at DESC;

-- name: SetCurrentVersion :exec
-- Flip the live-version pointer (publish / rollback). RLS guarantees we can only
-- touch our own org's site; we also re-check the version belongs to the site.
UPDATE app.sites
SET current_version_id = $2
WHERE id = $1;

-- ===========================================================================
-- site_versions
-- ===========================================================================

-- name: NextVersionNo :one
-- The next monotonic version number for a site (1 on the first deploy).
SELECT COALESCE(MAX(version_no), 0) + 1 AS next_version_no
FROM app.site_versions
WHERE site_id = $1;

-- name: CreateSiteVersion :one
INSERT INTO app.site_versions (
    org_id, site_id, version_no, status, r2_prefix, content_hash, size_bytes, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, org_id, site_id, version_no, status, r2_prefix, content_hash, size_bytes, created_by, created_at;

-- name: GetSiteVersion :one
SELECT id, org_id, site_id, version_no, status, r2_prefix, content_hash, size_bytes, created_by, created_at
FROM app.site_versions
WHERE id = $1;

-- name: GetSiteVersionByContentHash :one
-- Used to make a re-deploy of identical content idempotent (the per-site
-- content_hash unique constraint backs this).
SELECT id, org_id, site_id, version_no, status, r2_prefix, content_hash, size_bytes, created_by, created_at
FROM app.site_versions
WHERE site_id = $1 AND content_hash = $2;

-- name: ListSiteVersions :many
SELECT id, org_id, site_id, version_no, status, r2_prefix, content_hash, size_bytes, created_by, created_at
FROM app.site_versions
WHERE site_id = $1
ORDER BY version_no DESC;

-- ===========================================================================
-- host_routes (global host registry — the cross-tenant hijack guard)
-- ===========================================================================

-- name: InsertHostRoute :exec
-- Reserve a GLOBAL host for (org, site) inside the CreateSite tx. The PRIMARY KEY
-- on host means a host already owned by ANY org raises 23505 (unique_violation),
-- which the store surfaces as ErrHostTaken so the whole tx rolls back. RLS scopes
-- the row to the active org for later SELECT/UPDATE/DELETE, but the PK guard is
-- global regardless of RLS visibility.
INSERT INTO app.host_routes (host, org_id, site_id)
VALUES ($1, $2, $3);

-- name: GetHostRoute :one
-- Read the host's owning (org, site). Under the per-tx RLS tenant context this
-- returns a row ONLY if the active org owns the host; another org's row (or an
-- absent host) is a no-rows miss. Used by Publish / the projection writers to
-- assert the publishing site OWNS the host before writing route:<host>.
SELECT host, org_id, site_id, created_at
FROM app.host_routes
WHERE host = $1;

-- ===========================================================================
-- projection rebuild (the "KV is rebuildable from Postgres" invariant)
-- ===========================================================================

-- name: ListPublishedSitesForRebuild :many
-- Every site that currently has a live version, joined to that version's
-- access_mode source (the site row). Drives projection.RebuildFromDB: Postgres
-- is authoritative, the KV/D1 projection is a rebuildable cache.
SELECT
    s.id            AS site_id,
    s.org_id        AS org_id,
    s.slug          AS slug,
    s.access_mode   AS access_mode,
    s.current_version_id AS version_id
FROM app.sites s
WHERE s.current_version_id IS NOT NULL
ORDER BY s.created_at;
