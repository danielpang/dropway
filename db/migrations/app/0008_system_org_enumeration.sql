-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0008_system_org_enumeration.sql
--
-- Phase 4 (ops): a narrow, read-only system enumeration of org ids for the
-- cross-org system JOBS — the DR projection rebuild (wipe KV/D1, replay from
-- Postgres) and the R2 version GC. Both must iterate EVERY org, but the runtime
-- `shipped_app` role is non-BYPASSRLS, so a plain `SELECT id FROM app.org_meta`
-- returns only the rows of whatever tenant context is set (or nothing with none).
--
-- Rather than require a separate BYPASSRLS/superuser pool on these operator tools
-- (ARCHITECTURE.md §8 reserves BYPASSRLS for system jobs but prefers we avoid it on
-- the app role), we expose a single SECURITY DEFINER function that returns ONLY the
-- org ids — no secrets, no tenant data. This mirrors the 0006 app.resolve_host()
-- pattern exactly: definer rights provide the scoped, audited escalation, and a
-- fixed search_path closes the SECURITY DEFINER injection footgun.
--
-- The functions are then driven per-org under each org's OWN RLS tenant context by
-- the store (store.CollectRoutesForOrg / the GC), so the actual route/blob reads
-- stay tenant-scoped — only the id ENUMERATION is elevated.

-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION app.all_org_ids()
    RETURNS TABLE (id uuid)
    LANGUAGE sql
    STABLE
    SECURITY DEFINER
    SET search_path = app, pg_temp
AS $$
    SELECT om.id FROM app.org_meta om ORDER BY om.created_at;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
GRANT EXECUTE ON FUNCTION app.all_org_ids() TO shipped_app;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION app.all_org_ids() FROM shipped_app;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.all_org_ids();
-- +goose StatementEnd
