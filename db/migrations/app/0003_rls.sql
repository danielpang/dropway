-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0003_rls.sql
--
-- Row-Level Security: the tenant-isolation BACKSTOP underneath the Go API's
-- primary authz layer (ARCHITECTURE.md §5 / §8 / §10).
--
-- Operating model:
--   * These migrations run as a privileged OWNER/admin role (the table owner).
--   * The Go API connects at RUNTIME as `dropway_app` -- a NON-superuser,
--     NON-BYPASSRLS login role (created in 0001).
--   * Every app.* tenant table here is `ENABLE` *and* `FORCE ROW LEVEL SECURITY`.
--     FORCE makes the policies apply even to the TABLE OWNER, so isolation cannot
--     be silently bypassed by whoever happens to own the table. (Only a true
--     BYPASSRLS / superuser role escapes RLS -- reserved for cross-tenant system
--     jobs on a separate pool, never request-scoped handlers; §8.)
--
-- Tenant context:
--   On every request the Go API opens a tx and runs, from the VERIFIED JWT:
--       SET LOCAL app.current_org_id  = '<org uuid>';
--       SET LOCAL app.current_user_id = '<user uuid>';
--   SET LOCAL is transaction-scoped -> safe under Supavisor transaction-mode
--   pooling (GUCs do not leak across pooled txns).
--
-- Policies are deliberately SUBQUERY-FREE -- a plain equality on the denormalized
-- org_id column against the GUC -- so there are no joins / helper-function lookups
-- on the hot path:
--       USING       (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
--       WITH CHECK   (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
--   `current_setting(..., true)` returns NULL when the GUC was NEVER set in the
--   session, but returns an EMPTY STRING '' after a SET-then-RESET in the same
--   session. '' ::uuid would RAISE (invalid uuid syntax). The NULLIF(..., '')
--   coerces both the unset and the reset cases to NULL, and `org_id = NULL` is
--   NULL -> the row is filtered out -> DEFAULT DENY whenever no tenant context is
--   established, never an error. (The Go API uses SET LOCAL per tx and never
--   RESETs, so it always supplies a valid uuid or nothing; this just makes the
--   backstop robust under any session.)
--
-- org_meta is keyed BY the org id (its PK *is* org_id), so its policy compares
-- `id` to the GUC; org_usage / all other tenant tables compare `org_id`.

-- +goose Up

-- ===========================================================================
-- app.org_meta
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.org_meta ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_meta FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.org_meta TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY org_meta_tenant_isolation ON app.org_meta
    USING (id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- ===========================================================================
-- app.org_usage
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.org_usage ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_usage FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.org_usage TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY org_usage_tenant_isolation ON app.org_usage
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- ===========================================================================
-- app.sites
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.sites ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.sites FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.sites TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY sites_tenant_isolation ON app.sites
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- ===========================================================================
-- app.site_versions
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.site_versions ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_versions FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.site_versions TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY site_versions_tenant_isolation ON app.site_versions
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- ===========================================================================
-- app.domains
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.domains ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.domains FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.domains TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY domains_tenant_isolation ON app.domains
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- ===========================================================================
-- app.site_access_policy
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.site_access_policy ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_access_policy FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.site_access_policy TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY site_access_policy_tenant_isolation ON app.site_access_policy
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- ===========================================================================
-- app.allowlist_entries
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.allowlist_entries ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.allowlist_entries FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.allowlist_entries TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY allowlist_entries_tenant_isolation ON app.allowlist_entries
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- ===========================================================================
-- app.deploy_tokens
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.deploy_tokens ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.deploy_tokens FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.deploy_tokens TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY deploy_tokens_tenant_isolation ON app.deploy_tokens
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- ===========================================================================
-- app.audit_log
-- ===========================================================================
-- +goose StatementBegin
ALTER TABLE app.audit_log ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.audit_log FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.audit_log TO dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY audit_log_tenant_isolation ON app.audit_log
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP POLICY IF EXISTS audit_log_tenant_isolation ON app.audit_log;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.audit_log FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.audit_log NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.audit_log DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
DROP POLICY IF EXISTS deploy_tokens_tenant_isolation ON app.deploy_tokens;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.deploy_tokens FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.deploy_tokens NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.deploy_tokens DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
DROP POLICY IF EXISTS allowlist_entries_tenant_isolation ON app.allowlist_entries;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.allowlist_entries FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.allowlist_entries NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.allowlist_entries DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
DROP POLICY IF EXISTS site_access_policy_tenant_isolation ON app.site_access_policy;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.site_access_policy FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_access_policy NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_access_policy DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
DROP POLICY IF EXISTS domains_tenant_isolation ON app.domains;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.domains FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.domains NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.domains DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
DROP POLICY IF EXISTS site_versions_tenant_isolation ON app.site_versions;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.site_versions FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_versions NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_versions DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
DROP POLICY IF EXISTS sites_tenant_isolation ON app.sites;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.sites FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.sites NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.sites DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
DROP POLICY IF EXISTS org_usage_tenant_isolation ON app.org_usage;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.org_usage FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_usage NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_usage DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
DROP POLICY IF EXISTS org_meta_tenant_isolation ON app.org_meta;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.org_meta FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_meta NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_meta DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
