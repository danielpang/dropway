-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0006_access_control_phase2.sql
--
-- Phase 2 schema additions (access control & domains, ARCHITECTURE.md §5/§6/§9):
--
--   1. app.site_access_policy.unlisted        — the public-tier "unlisted" flag
--      (an unguessable host; not world-listed). Carried into the edge RouteValue
--      so the Worker can treat unlisted public sites the same as public for
--      serving but the dashboard can hide them from any listing.
--
--   2. app.allowlist_entries.claimed_by_user_id — records WHICH verified Dropway
--      account claimed a pending allowlist grant on first match (the claim_at
--      timestamp already exists). Surfaced as "claimed by" in the audit log
--      (ARCHITECTURE.md §10 [HIGH] allowlist grants).
--
--   3. app.domains.cf_hostname_id              — the Cloudflare-for-SaaS custom
--      hostname id returned by CreateCustomHostname, used to poll Status. Plus
--      dcv_record / a 'verifying' verify_status so the pending→verifying→active
--      state machine has somewhere to persist the DCV TXT record to surface to the
--      user. The verify_status CHECK is widened to include 'verifying'.
--
-- These are purely additive (new nullable columns / a widened CHECK), so the
-- migration is backward compatible: existing rows keep their values and the new
-- columns default to their "off"/null state.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. site_access_policy.unlisted
-- ---------------------------------------------------------------------------
-- +goose StatementBegin
ALTER TABLE app.site_access_policy
    ADD COLUMN unlisted boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- 2. allowlist_entries.claimed_by_user_id
-- ---------------------------------------------------------------------------
-- +goose StatementBegin
-- auth.user.id (Better Auth) of the verified account that claimed this grant.
-- Not FK'd (cross-schema, read-only target), mirroring sites.owner_user_id.
ALTER TABLE app.allowlist_entries
    ADD COLUMN claimed_by_user_id uuid;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- 3. domains: Cloudflare-for-SaaS custom hostname tracking + 'verifying' state
-- ---------------------------------------------------------------------------
-- +goose StatementBegin
-- The Cloudflare custom-hostname id (opaque). Lets Status(id) poll CF.
ALTER TABLE app.domains
    ADD COLUMN cf_hostname_id text;
-- +goose StatementEnd

-- +goose StatementBegin
-- The DNS DCV (Domain Control Validation) record the user must create to prove
-- ownership: "<name> <type> <value>" surfaced by the GET status endpoint.
ALTER TABLE app.domains
    ADD COLUMN dcv_record text;
-- +goose StatementEnd

-- +goose StatementBegin
-- Widen verify_status to include the intermediate 'verifying' state of the
-- pending→verifying→active(verified) machine. We drop and re-add the CHECK so the
-- new value is permitted; existing 'pending'/'verified'/'failed' rows are valid.
ALTER TABLE app.domains
    DROP CONSTRAINT IF EXISTS domains_verify_status_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.domains
    ADD CONSTRAINT domains_verify_status_check
        CHECK (verify_status IN ('pending', 'verifying', 'verified', 'failed'));
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- 4. app.resolve_host(text) — RLS-bypassing host → site resolver for /authz
-- ---------------------------------------------------------------------------
-- The cross-domain /authz viewer exchange (ARCHITECTURE.md §6) must resolve a
-- CONTENT HOST to its owning site REGARDLESS of the viewer's org: an allowlist or
-- public site can be shared with a viewer in a DIFFERENT org, so the viewer's RLS
-- tenant context would hide the site's host_routes/sites rows. This is a narrow,
-- read-only system lookup — exactly the case §8 reserves for a privileged path.
--
-- Rather than open a BYPASSRLS connection on the request path, we expose a single
-- SECURITY DEFINER function that returns ONLY the routing fields needed to drive
-- the authz decision (no secrets — password_hash is NOT returned here; the policy
-- is then read under the SITE's tenant context). A fixed search_path closes the
-- SECURITY DEFINER injection footgun, mirroring the 0004 trigger functions. The
-- runtime role is granted EXECUTE so the non-BYPASSRLS request connection can call
-- it; the function's own definer rights provide the scoped, audited escalation.
-- +goose StatementBegin
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
    SECURITY DEFINER
    SET search_path = app, pg_temp
AS $$
    SELECT hr.host, s.id, s.org_id, s.slug, s.access_mode, s.current_version_id
    FROM app.host_routes hr
    JOIN app.sites s ON s.id = hr.site_id
    WHERE hr.host = p_host;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
GRANT EXECUTE ON FUNCTION app.resolve_host(text) TO dropway_app;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION app.resolve_host(text) FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.resolve_host(text);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.domains
    DROP CONSTRAINT IF EXISTS domains_verify_status_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.domains
    ADD CONSTRAINT domains_verify_status_check
        CHECK (verify_status IN ('pending', 'verified', 'failed'));
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.domains DROP COLUMN IF EXISTS dcv_record;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.domains DROP COLUMN IF EXISTS cf_hostname_id;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.allowlist_entries DROP COLUMN IF EXISTS claimed_by_user_id;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_access_policy DROP COLUMN IF EXISTS unlisted;
-- +goose StatementEnd
