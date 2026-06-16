-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0009_ops_mode_org_enumeration.sql
--
-- Tighten app.all_org_ids() (added in 0008). That function is SECURITY DEFINER and
-- enumerates EVERY org id across all tenants — exactly what the cross-org system
-- JOBS (the DR projection rebuild + the R2 version GC) need. But 0008 GRANTed
-- EXECUTE to the request-path `dropway_app` role outright, so ANY normal request,
-- running as the same role, could call it and enumerate all org ids (a tenant-
-- isolation leak: org ids are not tenant data a request should see across orgs).
--
-- FIX: keep EXECUTE on the role (the ops jobs run as the SAME non-BYPASSRLS
-- dropway_app role — introducing a separate ops DB role would be far more invasive),
-- but GATE THE FUNCTION BODY on an explicit ops-mode GUC. The function now RAISES
-- unless the caller has set `app.ops_mode = '1'` for the current transaction. The
-- ops/DR/GC path sets `SET LOCAL app.ops_mode = '1'` before calling (see
-- store.ListAllOrgIDs); the request path never sets it, so a normal request that
-- reaches this function is denied. EXECUTE is also REVOKEd from PUBLIC so only the
-- runtime role can reach it at all.
--
-- The escalation is therefore explicit, narrow, and audited in one place: a caller
-- must deliberately opt into ops mode, which only the operator maintenance jobs do.

-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.all_org_ids()
    RETURNS TABLE (id uuid)
    LANGUAGE plpgsql
    STABLE
    SECURITY DEFINER
    SET search_path = app, pg_temp
AS $$
BEGIN
    -- Ops-only: the DR rebuild / R2 GC set app.ops_mode='1' for their transaction.
    -- A normal request never sets it, so cross-org enumeration is denied here even
    -- though the request-path role nominally holds EXECUTE.
    IF current_setting('app.ops_mode', true) IS DISTINCT FROM '1' THEN
        RAISE EXCEPTION 'app.all_org_ids() is ops-only; set app.ops_mode=1 (DR rebuild / GC path)'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN QUERY SELECT om.id FROM app.org_meta om ORDER BY om.created_at;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- PUBLIC must never reach it; the runtime role keeps EXECUTE (the ops jobs run as it)
-- but is now gated by the ops-mode GUC inside the body.
REVOKE EXECUTE ON FUNCTION app.all_org_ids() FROM PUBLIC;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
-- Revert to the ungated 0008 definition (SQL body, no ops-mode check).
CREATE OR REPLACE FUNCTION app.all_org_ids()
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
GRANT EXECUTE ON FUNCTION app.all_org_ids() TO dropway_app;
-- +goose StatementEnd
