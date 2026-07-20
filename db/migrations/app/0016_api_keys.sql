-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0016_api_keys.sql
--
-- Org-scoped API keys: the credential a CI job / server script / the TypeScript
-- SDK / the headless CLI holds to create and deploy sites over /v1 without an
-- interactive OAuth flow. See docs/typescript-sdk-api-keys.md.
--
-- The key belongs to the ORG (any admin can list/revoke it) but is ATTRIBUTED to
-- the member who minted it (created_by): keyed requests act AS that user for the
-- NOT NULL ownership columns (sites.owner_user_id, site_versions.created_by) and
-- stamp the key id into audit_log.actor_token. The full secret is returned ONCE
-- at creation; only the sha256 hash + a non-secret display prefix are stored.
--
-- Resolution at the auth boundary runs BEFORE any tenant context exists (the whole
-- point is to DISCOVER the org from the key), so it cannot run under RLS. A
-- SECURITY DEFINER function app.resolve_api_key(hash) does the cross-tenant lookup
-- — the same pattern as app.resolve_host() for the password/mint path — while every
-- MANAGEMENT query (create/list/revoke) runs normally under the tenant's RLS.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE app.api_keys (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    -- The identity user id of the member who minted the key. Attribution, not
    -- ownership: the key belongs to the org, but acts as this user.
    created_by   uuid NOT NULL,
    name         text NOT NULL,
    -- sha256 hex of the full secret (a 256-bit random value → a fast indexed
    -- equality lookup is the right trade, not bcrypt; see the ERD).
    key_hash     text NOT NULL UNIQUE,
    -- Non-secret display handle, e.g. "dw_live_3fk9" — enough to match a leaked
    -- key to a row, useless for recovery.
    key_prefix   text NOT NULL,
    scopes       text[] NOT NULL DEFAULT ARRAY['sites:*']::text[],
    -- Optional single-site restriction (column reserved; unwired in v1).
    site_id      uuid REFERENCES app.sites (id) ON DELETE CASCADE,
    last_used_at timestamptz,
    -- NULL = non-expiring (the v1 default). Column + check are in place so hosted
    -- expiry policy is a later data change, not a schema change.
    expires_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    revoked_by   uuid
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX api_keys_org_idx ON app.api_keys (org_id, created_at DESC);
-- +goose StatementEnd

-- RLS: identical tenant-isolation posture to every other app table. Management
-- queries run under the tenant GUC; the auth-path lookup uses the definer function
-- below (which bypasses RLS to discover the org).
-- +goose StatementBegin
ALTER TABLE app.api_keys ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.api_keys FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY api_keys_tenant_isolation ON app.api_keys
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.api_keys TO dropway_app;
-- +goose StatementEnd

-- The org kill switch (mirrors mcp_enabled / ai_enabled / chat_logs_enabled):
-- off → every key in the org 401s at the auth boundary; management still works so
-- admins can see and revoke.
-- +goose StatementBegin
ALTER TABLE app.org_meta ADD COLUMN api_keys_enabled boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- app.resolve_api_key(hash): the auth-boundary lookup. SECURITY DEFINER so it can
-- read the key row before any tenant context is set (RLS would otherwise hide every
-- row under the empty GUC). It returns the raw liveness fields + the joined org
-- governance fields; the Go auth path applies the fail-closed policy (revoked /
-- expired / suspended org / kill switch / creator-membership) and maps every failure
-- to a uniform 401. search_path is pinned (0011/0012 hardening) so the definer body
-- resolves only trusted schemas.
-- +goose StatementBegin
CREATE FUNCTION app.resolve_api_key(p_hash text)
    RETURNS TABLE(
        id uuid,
        org_id uuid,
        created_by uuid,
        expires_at timestamptz,
        revoked_at timestamptz,
        org_status text,
        api_keys_enabled boolean
    )
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
    AS $$
    SELECT k.id, k.org_id, k.created_by, k.expires_at, k.revoked_at,
           m.org_status, m.api_keys_enabled
    FROM app.api_keys k
    JOIN app.org_meta m ON m.id = k.org_id
    WHERE k.key_hash = p_hash;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE ALL ON FUNCTION app.resolve_api_key(text) FROM PUBLIC;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT EXECUTE ON FUNCTION app.resolve_api_key(text) TO dropway_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.resolve_api_key(text);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS api_keys_enabled;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.api_keys;
-- +goose StatementEnd
