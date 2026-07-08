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
-- resource's org for the confused-deputy guard.

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
SELECT id, plan_tier, allow_external_sharing, default_visibility, created_at, mcp_enabled
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

-- name: GetPlanTier :one
-- The org's live entitlement tier (the authoritative cap-check input). In the
-- hosted build org_meta.plan_tier is synced from billing.subscriptions by the
-- Stripe webhook; in self-host it stays 'free' but the Unlimited provider ignores
-- it. Returns 'free' when the org row is somehow absent (fail-soft default).
SELECT COALESCE(
    (SELECT plan_tier FROM app.org_meta WHERE id = $1),
    'free'
)::text AS plan_tier;

-- name: LockOrgSiteQuota :exec
-- Serialize concurrent site creates for the SAME org: take a transaction-scoped
-- advisory lock keyed by hashtext(org||':sites'). Held until the create-site tx
-- commits/rolls back, it makes the COUNT → policy check → INSERT a critical
-- section, so two racing creates anywhere in the org can't both read current=N and
-- both insert (the TOCTOU the per-ORG cap must not allow). Advisory locks are
-- independent of RLS and of row locks, so this needs no rows to exist yet.
SELECT pg_advisory_xact_lock(hashtext($1::text || ':sites'));

-- name: CountSitesForOrg :one
-- The number of sites in the active org (the per-ORG cap input, POOLED across all
-- members — seats are free, so the lever is org site count). Read under the
-- advisory lock above; RLS already scopes it to the active org and we filter by
-- org_id to be explicit.
SELECT count(*)::bigint AS n
FROM app.sites
WHERE org_id = $1;

-- name: LockOrgMemberQuota :exec
-- Serialize the members-cap preflight for an org: a transaction-scoped advisory
-- lock keyed by hashtext(org||':members'). Best-effort server-side enforcement on
-- OUR code path (Better Auth actually inserts the member row), so the lock just
-- makes our COUNT → policy check coherent under concurrent preflights.
SELECT pg_advisory_xact_lock(hashtext($1::text || ':members'));

-- ===========================================================================
-- storage metering: org_blobs ledger + org_usage counter
-- ===========================================================================

-- name: LockOrgStorageQuota :exec
-- Serialize concurrent deploys' storage accounting for an org: a transaction-scoped
-- advisory lock keyed by hashtext(org||':storage'), so the GetOrgStorage → cap check
-- → ledger INSERT → counter UPDATE is a critical section (the same TOCTOU guard the
-- site cap uses). Held until the deploy tx commits/rolls back.
SELECT pg_advisory_xact_lock(hashtext($1::text || ':storage'));

-- name: GetOrgStorage :one
-- The org's current stored bytes (the storage cap-check input). 0 when the
-- org_usage row is somehow absent (fail-soft, like GetPlanTier).
SELECT COALESCE(
    (SELECT storage_bytes FROM app.org_usage WHERE org_id = $1),
    0
)::bigint AS storage_bytes;

-- name: InsertOrgBlob :one
-- Record a content-addressed blob as stored for the org. Dedup-aware: ON CONFLICT
-- DO NOTHING means a blob already present for the org is NOT re-counted — RETURNING
-- yields a row (the size) ONLY when the blob is genuinely new, so the caller sums
-- the returned sizes as the storage delta. No row (pgx.ErrNoRows) = already stored.
INSERT INTO app.org_blobs (org_id, content_hash, size_bytes)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, content_hash) DO NOTHING
RETURNING size_bytes;

-- name: AddOrgStorage :exec
-- Increment the org's running storage total by the new-blob delta (called once per
-- deploy with the sum of genuinely-new blob sizes).
UPDATE app.org_usage
SET storage_bytes = storage_bytes + sqlc.arg(delta)::bigint,
    updated_at = now()
WHERE org_id = sqlc.arg(org_id);

-- name: DeleteOrgBlob :one
-- Drop a blob's ledger row when GC removes the orphaned R2 object; RETURNING the
-- size lets the caller decrement the running total. No row = it wasn't in the
-- ledger (e.g. uploaded before metering existed) → nothing to decrement.
DELETE FROM app.org_blobs
WHERE org_id = $1 AND content_hash = $2
RETURNING size_bytes;

-- name: SubOrgStorage :exec
-- Decrement the org's running storage total by the freed bytes (GC). GREATEST(0,…)
-- floors at zero so a reconciliation skew can never make the counter negative.
UPDATE app.org_usage
SET storage_bytes = GREATEST(0, storage_bytes - sqlc.arg(delta)::bigint),
    updated_at = now()
WHERE org_id = sqlc.arg(org_id);

-- name: RecomputeOrgStorage :exec
-- Reconcile the running total to the authoritative ledger sum (the cheap drift
-- fix; a deeper audit lists R2 to prune ledger rows orphaned by a crashed GC).
UPDATE app.org_usage
SET storage_bytes = COALESCE(
        (SELECT SUM(b.size_bytes) FROM app.org_blobs b WHERE b.org_id = $1), 0),
    updated_at = now()
WHERE app.org_usage.org_id = $1;

-- ===========================================================================
-- sites
-- ===========================================================================

-- name: CreateSite :one
INSERT INTO app.sites (org_id, slug, owner_user_id, access_mode)
VALUES ($1, $2, $3, $4)
RETURNING id, org_id, slug, owner_user_id, access_mode, current_version_id, feed_visible, title, description, created_at;

-- name: GetSite :one
SELECT id, org_id, slug, owner_user_id, access_mode, current_version_id, feed_visible, title, description, created_at
FROM app.sites
WHERE id = $1;

-- name: GetSiteStorageBytes :one
-- LOGICAL storage of a single site = the byte size of its CURRENT (live) version
-- (site_versions.size_bytes, the sum of that version's file sizes). A site with no
-- live version is 0. "Logical" means NOT deduplicated across sites/versions: a file
-- shipped by two sites counts in both, the same per-folder model Dropbox/Drive use.
-- RLS scopes the read to the active org.
SELECT COALESCE(v.size_bytes, 0)::bigint AS bytes
FROM app.sites s
LEFT JOIN app.site_versions v ON v.id = s.current_version_id
WHERE s.id = $1;

-- name: ListSiteStorageForOrg :many
-- LOGICAL storage per site for the active org (the current-version size of each
-- site, 0 when it has no live version) paired with the owning user, so the caller
-- can show per-site usage AND aggregate it per user. Same non-deduplicated model as
-- GetSiteStorageBytes. RLS scopes the read to the active org.
SELECT
    s.id            AS site_id,
    s.owner_user_id AS owner_user_id,
    COALESCE(v.size_bytes, 0)::bigint AS bytes
FROM app.sites s
LEFT JOIN app.site_versions v ON v.id = s.current_version_id
ORDER BY s.created_at DESC;

-- name: ListSites :many
SELECT id, org_id, slug, owner_user_id, access_mode, current_version_id, feed_visible, title, description, created_at
FROM app.sites
ORDER BY created_at DESC;

-- name: ListFeedSites :many
-- The org feed's SITE posts: every site in the active org that is feed-visible
-- (not private), newest first (older sites sink to the bottom). Each row carries
-- its net vote score, the CALLER's own vote ($1 = caller user id; 0 when they
-- haven't voted), and its comment count, so the feed renders the up/down controls
-- + counts in one query (no N+1). Votes/comments are polymorphic (subject_type =
-- 'site'). RLS scopes every read to the active org.
SELECT
    sqlc.embed(s),
    COALESCE((SELECT SUM(v.value) FROM app.post_votes v WHERE v.subject_type = 'site' AND v.subject_id = s.id), 0)::bigint AS score,
    COALESCE((SELECT mv.value FROM app.post_votes mv WHERE mv.subject_type = 'site' AND mv.subject_id = s.id AND mv.user_id = $1), 0)::int AS my_vote,
    COALESCE((SELECT COUNT(*) FROM app.post_comments c WHERE c.subject_type = 'site' AND c.subject_id = s.id), 0)::bigint AS comment_count
FROM app.sites s
WHERE s.feed_visible
ORDER BY s.created_at DESC;

-- name: ListFeedSkills :many
-- The org feed's SKILL posts: every skill in the active org that is feed-visible,
-- newest first, each carrying its current-version size, net vote score, the
-- caller's own vote ($1), and its comment count (subject_type = 'skill'). Skills
-- that never finalized an upload (no current version) are shown only to their
-- owner, so half-finished uploads don't clutter the feed. RLS scopes every read.
SELECT
    sqlc.embed(sk),
    COALESCE(ver.size_bytes, 0)::bigint AS size_bytes,
    COALESCE(ver.version_no, 0)::int AS version,
    COALESCE((SELECT SUM(v.value) FROM app.post_votes v WHERE v.subject_type = 'skill' AND v.subject_id = sk.id), 0)::bigint AS score,
    COALESCE((SELECT mv.value FROM app.post_votes mv WHERE mv.subject_type = 'skill' AND mv.subject_id = sk.id AND mv.user_id = $1), 0)::int AS my_vote,
    COALESCE((SELECT COUNT(*) FROM app.post_comments c WHERE c.subject_type = 'skill' AND c.subject_id = sk.id), 0)::bigint AS comment_count
FROM app.skills sk
LEFT JOIN app.skill_versions ver ON ver.id = sk.current_version_id
WHERE sk.feed_visible
  AND (sk.current_version_id IS NOT NULL OR sk.owner_user_id = $1::uuid)
ORDER BY sk.created_at DESC;

-- name: UpsertPostVote :exec
-- Cast (or change) the caller's vote on a feed post (site or skill). One row per
-- (subject_type, subject_id, user); a flip from up to down overwrites value. RLS
-- scopes the write to the active org.
INSERT INTO app.post_votes (subject_type, subject_id, org_id, user_id, value)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (subject_type, subject_id, user_id) DO UPDATE
SET value = EXCLUDED.value, updated_at = now();

-- name: DeletePostVote :exec
-- Remove the caller's vote on a feed post (un-vote). RLS scopes the delete to the org.
DELETE FROM app.post_votes
WHERE subject_type = $1 AND subject_id = $2 AND user_id = $3;

-- name: GetPostVoteScore :one
-- A feed post's net vote score (sum of +1/-1). RLS scopes the read to the org.
SELECT COALESCE(SUM(value), 0)::bigint AS score
FROM app.post_votes
WHERE subject_type = $1 AND subject_id = $2;

-- name: DeletePostVotesForSubject :exec
-- Drop every vote on a subject (called when the site/skill itself is deleted,
-- since the polymorphic table can't FK-cascade to two parents).
DELETE FROM app.post_votes
WHERE subject_type = $1 AND subject_id = $2;

-- name: DeletePostCommentsForSubject :exec
-- Drop every comment on a subject (called on the subject's delete; see above).
DELETE FROM app.post_comments
WHERE subject_type = $1 AND subject_id = $2;

-- name: SetSiteFeedVisible :one
-- Mark a site shared-to-feed (true) or private/off-feed (false). RLS scopes the
-- UPDATE to the active org; the handler additionally restricts it to the site's
-- owner or an org admin/owner. Does NOT touch access_mode, so the edge projection
-- is unaffected (feed visibility is the discovery axis, not the access axis).
UPDATE app.sites
SET feed_visible = $2
WHERE id = $1
RETURNING id, org_id, slug, owner_user_id, access_mode, current_version_id, feed_visible, title, description, created_at;

-- name: SetSiteFeedMeta :one
-- Set a site's human feed metadata (title + description). Empty strings are passed
-- as NULL by the caller so "clear it" round-trips to a null column. RLS scopes the
-- UPDATE to the active org; the handler restricts it to the owner or an org admin.
UPDATE app.sites
SET title       = $2,
    description  = $3
WHERE id = $1
RETURNING id, org_id, slug, owner_user_id, access_mode, current_version_id, feed_visible, title, description, created_at;

-- name: SetCurrentVersion :exec
-- Flip the live-version pointer (publish / rollback). RLS guarantees we can only
-- touch our own org's site; we also re-check the version belongs to the site.
UPDATE app.sites
SET current_version_id = $2
WHERE id = $1;

-- ===========================================================================
-- site_comments — org-internal discussion on a shared site, with @mentions
-- ===========================================================================

-- name: CreatePostComment :one
-- Add a comment to a feed post (site or skill). mentioned_user_ids is the set of
-- tagged org users (identity ids). RLS scopes the INSERT to the active org (the
-- WITH CHECK clause on the tenant policy rejects a row whose org_id isn't the
-- active tenant).
INSERT INTO app.post_comments (org_id, subject_type, subject_id, author_user_id, body, mentioned_user_ids)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, org_id, subject_type, subject_id, author_user_id, body, mentioned_user_ids, created_at;

-- name: ListPostComments :many
-- A feed post's comment thread, displayed oldest-first (top-to-bottom like a
-- conversation) but BOUNDED to the most recent $3 comments so a long thread can't
-- load an unbounded result. RLS scopes the read to the active org; the
-- (subject_type, subject_id, created_at) index backs both orderings.
SELECT id, org_id, subject_type, subject_id, author_user_id, body, mentioned_user_ids, created_at
FROM (
    SELECT id, org_id, subject_type, subject_id, author_user_id, body, mentioned_user_ids, created_at
    FROM app.post_comments
    WHERE subject_type = $1 AND subject_id = $2
    ORDER BY created_at DESC, id DESC
    LIMIT $3
) recent
ORDER BY created_at ASC, id ASC;

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

-- name: ListHostRoutesForSite :many
-- Every host registered for a site in the GLOBAL registry — the canonical
-- <slug>.dropwaycontent.com host AND every verified custom-domain host. RLS
-- scopes the rows to the active org, so a caller only ever sees its own site's
-- hosts. An access-mode / policy change must rewrite EVERY one of these routes
-- (not just the canonical one), or a verified custom host keeps serving at the
-- OLD access_mode after the policy tightened (revocation).
SELECT host, org_id, site_id, created_at
FROM app.host_routes
WHERE site_id = $1
ORDER BY host;

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

-- ===========================================================================
-- access policy (Phase 2) — per-site gating config
-- ===========================================================================

-- name: SetSiteAccessMode :exec
-- Flip a site's access_mode (the source for the edge RouteValue). RLS scopes the
-- UPDATE to the active org; the external-sharing trigger (0004) rejects 'public'
-- under a false org policy.
UPDATE app.sites
SET access_mode = $2
WHERE id = $1;

-- name: UpsertSiteAccessPolicy :one
-- Insert or replace the per-site access policy (one row per site, PK = site_id).
-- password_hash is non-null only for mode='password'; expires_at / unlisted are
-- optional. The policy-mirror external-sharing trigger (0004) rejects mode='public'
-- under a false org policy.
INSERT INTO app.site_access_policy (site_id, org_id, mode, password_hash, expires_at, unlisted, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (site_id) DO UPDATE
SET mode          = EXCLUDED.mode,
    password_hash = EXCLUDED.password_hash,
    expires_at    = EXCLUDED.expires_at,
    unlisted      = EXCLUDED.unlisted,
    updated_at    = now()
RETURNING site_id, org_id, mode, password_hash, expires_at, unlisted, updated_at;

-- name: GetSiteAccessPolicy :one
SELECT site_id, org_id, mode, password_hash, expires_at, unlisted, updated_at
FROM app.site_access_policy
WHERE site_id = $1;

-- ===========================================================================
-- allowlist (Phase 2)
-- ===========================================================================

-- name: UpsertAllowlistEntry :one
-- Add (or re-add) an email grant to a site's allowlist. is_external marks an
-- email whose domain is not an org verified domain; the external-sharing trigger
-- (0004) rejects is_external=true under a false org policy. Re-adding an email
-- resets it to a fresh pending (unclaimed) grant.
INSERT INTO app.allowlist_entries (org_id, site_id, email, is_external)
VALUES ($1, $2, $3, $4)
ON CONFLICT (site_id, email) DO UPDATE
SET is_external        = EXCLUDED.is_external,
    claimed_at         = NULL,
    claimed_by_user_id = NULL
RETURNING id, org_id, site_id, email, is_external, claimed_at, claimed_by_user_id, created_at;

-- name: DeleteAllowlistEntry :exec
DELETE FROM app.allowlist_entries
WHERE site_id = $1 AND email = $2;

-- name: ListAllowlistEntries :many
SELECT id, org_id, site_id, email, is_external, claimed_at, claimed_by_user_id, created_at
FROM app.allowlist_entries
WHERE site_id = $1
ORDER BY created_at;

-- name: GetAllowlistEntryByEmail :one
-- Look up a grant by (site, email) for the authz claim path.
SELECT id, org_id, site_id, email, is_external, claimed_at, claimed_by_user_id, created_at
FROM app.allowlist_entries
WHERE site_id = $1 AND email = $2;

-- name: ClaimAllowlistEntry :exec
-- Claim a pending grant for the first verified account that matches it: set
-- claimed_at + claimed_by_user_id. Idempotent — re-claiming by the same user is a
-- no-op; we only set claim fields when still unclaimed so the original claimant
-- and timestamp are preserved.
UPDATE app.allowlist_entries
SET claimed_at         = COALESCE(claimed_at, now()),
    claimed_by_user_id = COALESCE(claimed_by_user_id, $2)
WHERE id = $1;

-- ===========================================================================
-- domains (Phase 2) — Cloudflare-for-SaaS custom hostnames
-- ===========================================================================

-- name: InsertDomain :one
-- Reserve a custom hostname for a site. hostname is GLOBALLY UNIQUE, so a
-- conflicting insert from any org raises 23505 (surfaced as ErrHostTaken). Stores
-- the Cloudflare custom-hostname id + the DCV record to surface to the user.
INSERT INTO app.domains (org_id, site_id, hostname, verify_status, tls_status, cf_hostname_id, dcv_record)
VALUES ($1, $2, $3, 'pending', 'pending', $4, $5)
RETURNING id, org_id, site_id, hostname, verify_status, tls_status, cf_hostname_id, dcv_record, created_at;

-- name: GetDomain :one
SELECT id, org_id, site_id, hostname, verify_status, tls_status, cf_hostname_id, dcv_record, created_at
FROM app.domains
WHERE id = $1;

-- name: ListDomainsForSite :many
SELECT id, org_id, site_id, hostname, verify_status, tls_status, cf_hostname_id, dcv_record, created_at
FROM app.domains
WHERE site_id = $1
ORDER BY created_at;

-- name: UpdateDomainStatus :one
-- Advance the custom-domain state machine (pending → verifying → verified/failed)
-- and the TLS status from a Cloudflare Status() poll.
UPDATE app.domains
SET verify_status = $2,
    tls_status    = $3
WHERE id = $1
RETURNING id, org_id, site_id, hostname, verify_status, tls_status, cf_hostname_id, dcv_record, created_at;

-- name: DeleteDomain :one
-- Remove a custom domain, returning its hostname + cf_hostname_id so the caller can
-- also drop the global host route (so serve/edge stop resolving the host) and delete
-- the Cloudflare custom hostname. RLS scopes the delete to the active org.
DELETE FROM app.domains
WHERE id = $1
RETURNING id, org_id, site_id, hostname, cf_hostname_id;

-- ===========================================================================
-- host_routes (Phase 2) — register/unregister a custom host in the global registry
-- ===========================================================================

-- name: UpsertHostRoute :exec
-- Register a host → (org, site) in the GLOBAL registry. Used when a custom domain
-- verifies (the content host is the custom hostname). PK on host enforces global
-- uniqueness; a conflict with another org raises 23505 (ErrHostTaken). ON CONFLICT
-- updates only when the row is already owned by THIS (org, site) — a different
-- owner can never be overwritten because RLS makes its row invisible to UPDATE, so
-- the ON CONFLICT target row isn't visible and the upsert raises instead.
INSERT INTO app.host_routes (host, org_id, site_id)
VALUES ($1, $2, $3)
ON CONFLICT (host) DO UPDATE
SET site_id = EXCLUDED.site_id
WHERE app.host_routes.org_id = EXCLUDED.org_id;

-- name: DeleteHostRoute :exec
DELETE FROM app.host_routes WHERE host = $1;

-- ===========================================================================
-- host resolution (Phase 2) — resolve a content host → owning site (for /authz)
-- ===========================================================================

-- name: ResolveSiteByHostRoute :one
-- Resolve a content host (the *.dropwaycontent.com label OR a verified custom
-- host) to its owning site via the global host registry, returning the site's
-- access fields. Runs under RLS so only the active org's hosts resolve — the
-- /authz mint sets the tenant from the resolved org first (see store.AuthzContext).
SELECT
    hr.host       AS host,
    s.id          AS site_id,
    s.org_id      AS org_id,
    s.slug        AS slug,
    s.access_mode AS access_mode,
    s.current_version_id AS version_id
FROM app.host_routes hr
JOIN app.sites s ON s.id = hr.site_id
WHERE hr.host = $1;

-- ===========================================================================
-- org policy (Phase 2) — allow_external_sharing toggle + reconcile
-- ===========================================================================

-- name: SetAllowExternalSharing :exec
-- Toggle the org's external-sharing policy (admin/owner only, enforced in Go).
UPDATE app.org_meta
SET allow_external_sharing = $2
WHERE id = $1;

-- name: SetMcpEnabled :exec
-- Toggle whether the Dropway MCP server may serve this org (admin/owner only,
-- enforced in Go). The MCP resource server ALSO re-checks org_meta.mcp_enabled per
-- request, so disabling takes effect immediately even for already-issued tokens.
UPDATE app.org_meta
SET mcp_enabled = $2
WHERE id = $1;

-- name: ListPublicSitesForOrg :many
-- Every site in the active org whose access_mode = 'public' (used by the reconcile
-- on disabling external sharing: these are downgraded to org_only).
SELECT id, org_id, slug, owner_user_id, access_mode, current_version_id, feed_visible, title, description, created_at
FROM app.sites
WHERE access_mode = 'public'
ORDER BY created_at;

-- name: DeleteExternalAllowlistEntriesForOrg :exec
-- Remove every external-email allowlist grant in the active org (reconcile on
-- disabling external sharing — revoke external access).
DELETE FROM app.allowlist_entries
WHERE is_external = true;

-- NOTE: resolving a content host → owning site via the RLS-bypassing
-- app.resolve_host() SECURITY DEFINER function (migration 0006) is done with raw
-- pgx in the store (store.resolveHost), NOT sqlc: sqlc cannot infer column types
-- from a RETURNS TABLE function (it emits interface{}). The store scans the known
-- types directly. See services/api/internal/store/authz.go.

-- ===========================================================================
-- audit_log (Phase 4) — append-only record of sensitive actions
-- ===========================================================================

-- name: WriteAuditLog :one
-- Append an audit row for a sensitive mutation. Runs inside the SAME RLS tenant
-- tx as the action it records (org-scoped by the per-tx GUC + the explicit org_id),
-- so an audit write can never land under the wrong tenant. actor_user is the verified
-- user id (null for a deploy-token actor); actor_token is the deploy-token id when a
-- token drove the action; metadata is freeform jsonb; ip/request_id/trace_id carry
-- the request provenance.
INSERT INTO app.audit_log (
    org_id, actor_user, actor_token, action, target, metadata, ip, request_id, trace_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, org_id, actor_user, actor_token, action, target, metadata, ip, request_id, trace_id, created_at;

-- ===========================================================================
-- R2 version GC (Phase 4) — versions to retain per org
-- ===========================================================================

-- name: ListVersionsForGC :many
-- Every version of every site in the active org, newest first within each site,
-- flagged with whether it is the site's CURRENT (live) version. Drives the R2
-- version GC retention policy (keep current + last N): the GC groups by site, keeps
-- the current version + the top-N by version_no, reads those versions' manifests to
-- collect referenced blob shas, and deletes every org blob not in that set. RLS
-- scopes the rows to the active org. r2_prefix + id locate the manifest object.
SELECT
    v.id            AS version_id,
    v.site_id       AS site_id,
    v.version_no    AS version_no,
    v.r2_prefix     AS r2_prefix,
    (s.current_version_id IS NOT NULL AND s.current_version_id = v.id) AS is_current
FROM app.site_versions v
JOIN app.sites s ON s.id = v.site_id
ORDER BY v.site_id, v.version_no DESC;

-- name: ListAuditLog :many
-- Page the active org's audit log newest-first. RLS scopes the read to the org; the
-- (org_id, created_at DESC) index backs the order. Keyset-free LIMIT/OFFSET paging is
-- adequate for an admin audit viewer (small N per page).
SELECT id, org_id, actor_user, actor_token, action, target, metadata, ip, request_id, trace_id, created_at
FROM app.audit_log
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- ===========================================================================
-- skills (migration 0008) — org-wide skill sharing
-- ===========================================================================

-- name: LockOrgSkillQuota :exec
-- Serialize concurrent skill creates for the SAME org (the same COUNT → policy →
-- INSERT critical section the site cap uses).
SELECT pg_advisory_xact_lock(hashtext($1::text || ':skills'));

-- name: CountSkillsForOrg :one
SELECT count(*)::bigint AS n
FROM app.skills
WHERE org_id = $1;

-- name: CreateSkill :one
INSERT INTO app.skills (org_id, slug, owner_user_id, title, description)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, org_id, slug, owner_user_id, title, description, current_version_id, feed_visible, created_at;

-- name: CreateSeedSkill :one
-- Insert a preset seed skill, or DO NOTHING if the org already has that slug
-- (a real user's skill, or this seed from a prior attempt). ON CONFLICT means a
-- collision never raises 23505 and aborts the seeding transaction; a no-rows
-- result tells the caller to inspect the existing row and skip it unless it is
-- our own seed.
INSERT INTO app.skills (org_id, slug, owner_user_id, title, description)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (org_id, slug) DO NOTHING
RETURNING id, org_id, slug, owner_user_id, title, description, current_version_id, feed_visible, created_at;

-- name: GetSkill :one
-- Embeds the full skill row plus its current version's size (0 when unset), so
-- reads never N+1 a per-skill version lookup.
SELECT sqlc.embed(sk),
       COALESCE(v.size_bytes, 0)::bigint AS size_bytes,
       COALESCE(v.version_no, 0)::int AS version
FROM app.skills sk
LEFT JOIN app.skill_versions v ON v.id = sk.current_version_id
WHERE sk.id = $1;

-- name: GetSkillBySlug :one
SELECT sqlc.embed(sk),
       COALESCE(v.size_bytes, 0)::bigint AS size_bytes,
       COALESCE(v.version_no, 0)::int AS version
FROM app.skills sk
LEFT JOIN app.skill_versions v ON v.id = sk.current_version_id
WHERE sk.slug = $1;

-- name: ListSkills :many
-- Search + filter the active org's skills. q matches slug/title/description
-- (ILIKE, '' = no text filter); folder_slug restricts to members of that folder
-- ('' = any); presets_only additionally requires the membership's is_preset flag.
-- Skills that have never finalized an upload (no current version) are visible
-- only to their owner (caller_id), so half-finished uploads don't clutter the
-- org listing. RLS scopes every read to the active org.
SELECT sqlc.embed(sk),
       COALESCE(v.size_bytes, 0)::bigint AS size_bytes,
       COALESCE(v.version_no, 0)::int AS version
FROM app.skills sk
LEFT JOIN app.skill_versions v ON v.id = sk.current_version_id
WHERE (sk.current_version_id IS NOT NULL OR sk.owner_user_id = sqlc.arg(caller_id)::uuid)
  AND (
        sqlc.arg(q)::text = ''
        OR sk.slug ILIKE '%' || sqlc.arg(q) || '%'
        OR COALESCE(sk.title, '') ILIKE '%' || sqlc.arg(q) || '%'
        OR COALESCE(sk.description, '') ILIKE '%' || sqlc.arg(q) || '%'
      )
  AND (
        (sqlc.arg(folder_slug)::text = '' AND NOT sqlc.arg(presets_only)::boolean)
        OR EXISTS (
            SELECT 1
            FROM app.skill_folder_items fi
            JOIN app.skill_folders f ON f.id = fi.folder_id
            WHERE fi.skill_id = sk.id
              AND (sqlc.arg(folder_slug)::text = '' OR f.slug = sqlc.arg(folder_slug))
              AND (NOT sqlc.arg(presets_only)::boolean OR fi.is_preset)
        )
      )
ORDER BY sk.created_at DESC;

-- name: DeleteSkill :one
-- Remove a skill (versions + folder memberships cascade). RETURNING detects an
-- RLS-invisible / absent row as a no-rows miss (→ ErrNotFound).
DELETE FROM app.skills
WHERE id = $1
RETURNING id;

-- name: SetSkillMeta :one
-- Fill a skill's human metadata (from SKILL.md frontmatter on finalize, or an
-- explicit edit). Empty strings are passed as NULL so "unset" round-trips.
UPDATE app.skills
SET title = $2,
    description = $3
WHERE id = $1
RETURNING id, org_id, slug, owner_user_id, title, description, current_version_id, feed_visible, created_at;

-- name: SetSkillFeedVisible :one
-- Share a skill to the org feed (true) or make it private/off-feed (false). RLS
-- scopes the UPDATE to the active org; the handler restricts it to the skill's
-- owner or an org admin/owner. A miss surfaces as a no-rows error (→ ErrNotFound).
UPDATE app.skills
SET feed_visible = $2
WHERE id = $1
RETURNING id, org_id, slug, owner_user_id, title, description, current_version_id, feed_visible, created_at;

-- name: SetSkillCurrentVersion :exec
-- Flip the live pointer (finalize = publish in the latest-only v1 model).
UPDATE app.skills
SET current_version_id = $2
WHERE id = $1;

-- name: NextSkillVersionNo :one
SELECT COALESCE(MAX(version_no), 0) + 1 AS next_version_no
FROM app.skill_versions
WHERE skill_id = $1;

-- name: CreateSkillVersion :one
INSERT INTO app.skill_versions (
    org_id, skill_id, version_no, status, content_hash, size_bytes, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, org_id, skill_id, version_no, status, content_hash, size_bytes, created_by, created_at;

-- name: UpsertSkillVersion :one
-- Get-or-create a version by its (skill_id, content_hash): a no-op DO UPDATE so
-- RETURNING always yields the row whether it was just inserted or already
-- existed. Used by idempotent seeding so a retried seed can't raise 23505 and
-- abort the transaction. The no-op update sets status to itself.
INSERT INTO app.skill_versions (
    org_id, skill_id, version_no, status, content_hash, size_bytes, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (skill_id, content_hash) DO UPDATE
SET status = app.skill_versions.status
RETURNING id, org_id, skill_id, version_no, status, content_hash, size_bytes, created_by, created_at;

-- name: GetSkillVersion :one
SELECT id, org_id, skill_id, version_no, status, content_hash, size_bytes, created_by, created_at
FROM app.skill_versions
WHERE id = $1;

-- name: GetSkillVersionByContentHash :one
-- Idempotent re-upload of identical content (the per-skill content_hash unique
-- constraint backs this).
SELECT id, org_id, skill_id, version_no, status, content_hash, size_bytes, created_by, created_at
FROM app.skill_versions
WHERE skill_id = $1 AND content_hash = $2;

-- ===========================================================================
-- skill folders — admin-curated taxonomy + preset flags
-- ===========================================================================

-- name: CreateSkillFolder :one
INSERT INTO app.skill_folders (org_id, slug, title)
VALUES ($1, $2, $3)
RETURNING id, org_id, slug, title, created_at;

-- name: GetOrCreateSkillFolder :one
-- Get-or-create a folder by (org_id, slug): a no-op DO UPDATE so RETURNING
-- always yields the row. Used by idempotent seeding so re-running against an
-- org that already has a default folder (e.g. an admin created it first) never
-- raises 23505 and aborts the seed transaction.
INSERT INTO app.skill_folders (org_id, slug, title)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, slug) DO UPDATE
SET title = app.skill_folders.title
RETURNING id, org_id, slug, title, created_at;

-- name: GetSkillFolder :one
SELECT id, org_id, slug, title, created_at
FROM app.skill_folders
WHERE id = $1;

-- name: GetSkillFolderBySlug :one
SELECT id, org_id, slug, title, created_at
FROM app.skill_folders
WHERE slug = $1;

-- name: ListSkillFolders :many
-- The org's folders with their member counts (the folder tabs + admin panel).
SELECT
    f.id, f.org_id, f.slug, f.title, f.created_at,
    COALESCE((SELECT COUNT(*) FROM app.skill_folder_items fi WHERE fi.folder_id = f.id), 0)::bigint AS item_count
FROM app.skill_folders f
ORDER BY f.slug;

-- name: RenameSkillFolder :one
UPDATE app.skill_folders
SET title = $2
WHERE id = $1
RETURNING id, org_id, slug, title, created_at;

-- name: DeleteSkillFolder :one
-- Memberships cascade; the skills themselves survive.
DELETE FROM app.skill_folders
WHERE id = $1
RETURNING id;

-- name: LockSkillFolderQuota :exec
-- Serialize concurrent membership inserts for the SAME folder, so the COUNT →
-- per-folder cap check → INSERT is a critical section (free tier caps skills per
-- folder; see quota.ResourceSkillPerFolder).
SELECT pg_advisory_xact_lock(hashtext($1::text || ':folder_items'));

-- name: CountFolderItems :one
SELECT count(*)::bigint AS n
FROM app.skill_folder_items
WHERE folder_id = $1;

-- name: UpsertSkillFolderItem :exec
-- Add a skill to a folder (or update its preset flag if already a member).
INSERT INTO app.skill_folder_items (org_id, folder_id, skill_id, is_preset, added_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (folder_id, skill_id) DO UPDATE
SET is_preset = EXCLUDED.is_preset;

-- name: RemoveSkillFolderItem :one
DELETE FROM app.skill_folder_items
WHERE folder_id = $1 AND skill_id = $2
RETURNING skill_id;

-- name: SetSkillFolderItemPreset :one
UPDATE app.skill_folder_items
SET is_preset = $3
WHERE folder_id = $1 AND skill_id = $2
RETURNING folder_id, skill_id, is_preset;

-- name: ListFoldersForSkills :many
-- Folder memberships for a set of skills in one round-trip (the folder chips on
-- each row of a skills listing — no N+1).
SELECT fi.skill_id, f.id AS folder_id, f.slug, f.title, fi.is_preset
FROM app.skill_folder_items fi
JOIN app.skill_folders f ON f.id = fi.folder_id
WHERE fi.skill_id = ANY(sqlc.arg(skill_ids)::uuid[])
ORDER BY f.slug;

-- name: ListFolderSkills :many
-- Every skill in a folder that has a live version (the bulk-download set).
SELECT sqlc.embed(sk),
       COALESCE(v.size_bytes, 0)::bigint AS size_bytes,
       COALESCE(v.version_no, 0)::int AS version
FROM app.skill_folder_items fi
JOIN app.skills sk ON sk.id = fi.skill_id
LEFT JOIN app.skill_versions v ON v.id = sk.current_version_id
WHERE fi.folder_id = $1
  AND sk.current_version_id IS NOT NULL
ORDER BY sk.slug;

-- name: DeleteSkillFolderItemsForSkill :exec
-- Replace-memberships helper: clear a skill's memberships before re-inserting
-- the new set (PUT /skills/{id}/folders semantics), preserving nothing.
DELETE FROM app.skill_folder_items
WHERE skill_id = $1;

-- ===========================================================================
-- skills seeding — lazy per-org default folders + preset skills
-- ===========================================================================

-- name: ListCurrentSkillVersionsForGC :many
-- Every skill's CURRENT version (latest-only model: that is the only version
-- whose blobs must survive GC — superseded skill versions' blobs become
-- orphans). The R2 GC unions these manifests' blob refs with the retained site
-- versions' refs before deleting unreferenced org blobs; without this, skill
-- content would look orphaned to a site-only GC and be deleted. RLS scopes the
-- rows to the active org.
SELECT sk.id AS skill_id, sk.current_version_id AS version_id
FROM app.skills sk
WHERE sk.current_version_id IS NOT NULL
ORDER BY sk.id;

-- name: LockOrgSkillsSeed :exec
-- Serialize concurrent first-touches of the skills feature for an org, so the
-- skills_seeded check → seed → set-flag sequence runs exactly once.
SELECT pg_advisory_xact_lock(hashtext($1::text || ':skills_seed'));

-- name: GetOrgSkillsSeeded :one
SELECT COALESCE(
    (SELECT skills_seeded FROM app.org_meta WHERE id = $1),
    false
)::boolean AS skills_seeded;

-- name: SetOrgSkillsSeeded :exec
UPDATE app.org_meta
SET skills_seeded = $2
WHERE id = $1;
