-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0004_external_sharing_invariant.sql
--
-- Defense-in-depth for the org sharing policy (ARCHITECTURE.md §5.4 / §10
-- "[CRITICAL] External-sharing policy is enforced in depth").
--
-- Invariant: when an org has `org_meta.allow_external_sharing = false`, NO site
-- in that org may be shared outside the org. Concretely we reject, at the DB:
--   * sites.access_mode = 'public'                (the site is world-readable)
--   * site_access_policy.mode = 'public'          (policy mirror of the above)
--   * allowlist_entries.is_external = true        (an external-email grant)
-- whenever the owning org's policy is false.
--
-- This cannot be a plain CHECK constraint because the predicate spans tables
-- (the row being written vs. app.org_meta). We therefore use BEFORE
-- INSERT/UPDATE triggers. The trigger functions are SECURITY DEFINER so they can
-- read app.org_meta even though the runtime `dropway_app` role's RLS would
-- otherwise scope the lookup -- the check must be authoritative regardless of the
-- caller's tenant context. A fixed search_path closes the SECURITY DEFINER
-- injection footgun.
--
-- NOTE: flipping allow_external_sharing back to false does NOT retroactively
-- reject already-shared rows via this trigger (triggers fire on the written row,
-- not on org_meta updates). The Go API runs a reconcile job on that flip
-- (§5.4 / §6) to revoke existing external grants + public visibility and rewrite
-- the edge deny-list. This trigger guarantees no NEW external grant can be
-- created while the policy is false.

-- +goose Up

-- +goose StatementBegin
-- Guard for app.sites: reject access_mode='public' under a false org policy.
CREATE FUNCTION app.enforce_site_external_sharing()
    RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = app, pg_temp
AS $$
DECLARE
    v_allow boolean;
BEGIN
    IF NEW.access_mode = 'public' THEN
        SELECT allow_external_sharing INTO v_allow
        FROM app.org_meta
        WHERE id = NEW.org_id;

        IF v_allow IS DISTINCT FROM true THEN
            RAISE EXCEPTION
                'external sharing disabled for org %: site access_mode=''public'' is not permitted',
                NEW.org_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sites_external_sharing_guard
    BEFORE INSERT OR UPDATE OF access_mode, org_id ON app.sites
    FOR EACH ROW
    EXECUTE FUNCTION app.enforce_site_external_sharing();
-- +goose StatementEnd

-- +goose StatementBegin
-- Guard for app.site_access_policy: reject mode='public' under a false org policy.
CREATE FUNCTION app.enforce_policy_external_sharing()
    RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = app, pg_temp
AS $$
DECLARE
    v_allow boolean;
BEGIN
    IF NEW.mode = 'public' THEN
        SELECT allow_external_sharing INTO v_allow
        FROM app.org_meta
        WHERE id = NEW.org_id;

        IF v_allow IS DISTINCT FROM true THEN
            RAISE EXCEPTION
                'external sharing disabled for org %: access policy mode=''public'' is not permitted',
                NEW.org_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER site_access_policy_external_sharing_guard
    BEFORE INSERT OR UPDATE OF mode, org_id ON app.site_access_policy
    FOR EACH ROW
    EXECUTE FUNCTION app.enforce_policy_external_sharing();
-- +goose StatementEnd

-- +goose StatementBegin
-- Guard for app.allowlist_entries: reject is_external=true under a false policy.
CREATE FUNCTION app.enforce_allowlist_external_sharing()
    RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = app, pg_temp
AS $$
DECLARE
    v_allow boolean;
BEGIN
    IF NEW.is_external = true THEN
        SELECT allow_external_sharing INTO v_allow
        FROM app.org_meta
        WHERE id = NEW.org_id;

        IF v_allow IS DISTINCT FROM true THEN
            RAISE EXCEPTION
                'external sharing disabled for org %: external allowlist grant is not permitted',
                NEW.org_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER allowlist_entries_external_sharing_guard
    BEFORE INSERT OR UPDATE OF is_external, org_id ON app.allowlist_entries
    FOR EACH ROW
    EXECUTE FUNCTION app.enforce_allowlist_external_sharing();
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TRIGGER IF EXISTS allowlist_entries_external_sharing_guard ON app.allowlist_entries;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.enforce_allowlist_external_sharing();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS site_access_policy_external_sharing_guard ON app.site_access_policy;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.enforce_policy_external_sharing();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS sites_external_sharing_guard ON app.sites;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.enforce_site_external_sharing();
-- +goose StatementEnd
