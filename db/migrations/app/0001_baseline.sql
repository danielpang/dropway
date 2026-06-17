-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0001_baseline.sql
--
-- SQUASHED BASELINE. This single migration reproduces the EXACT schema that
-- migrations 0001..0013 produced (verified by applying both to fresh databases
-- and diffing pg_dump --schema-only output; they are identical). The original
-- 13 files were collapsed pre-launch for legibility -- see README.md in this dir.
--
-- Scope: the `app` schema (owned by the Go API), the empty `identity` namespace
-- (Better Auth owns + migrates its own tables into it), and the non-superuser,
-- non-BYPASSRLS `dropway_app` runtime role. The role password is set out-of-band
-- (DROPWAY_APP_DB_PASSWORD), never here.

-- +goose Up
-- pg_dump emits functions before the tables they reference; this defers function
-- body validation so creation order doesn't matter (scoped to this migration tx).
SET check_function_bodies = false;

-- +goose StatementBegin
CREATE SCHEMA IF NOT EXISTS app;
-- +goose StatementEnd

-- +goose StatementBegin
-- identity is Better-Auth-owned (separate from any reserved `auth` schema, e.g. Supabase); we only guarantee the schema exists as an FK target.
CREATE SCHEMA IF NOT EXISTS identity;
-- +goose StatementEnd

-- +goose StatementBegin
-- Non-superuser, non-BYPASSRLS runtime login role. Guarded so re-running (or a
-- managed PG where it is pre-provisioned, e.g. Supabase) stays idempotent.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dropway_app') THEN
        CREATE ROLE dropway_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
    END IF;
END
$$;
-- +goose StatementEnd

--
-- PostgreSQL database dump
--


-- Dumped from database version 16.14 (Debian 16.14-1.pgdg13+1)
-- Dumped by pg_dump version 16.14 (Debian 16.14-1.pgdg13+1)


--
-- Name: app; Type: SCHEMA; Schema: -; Owner: -
--



--
-- Name: identity; Type: SCHEMA; Schema: -; Owner: -
--



--
-- Name: all_org_ids(); Type: FUNCTION; Schema: app; Owner: -
--

-- +goose StatementBegin
CREATE FUNCTION app.all_org_ids() RETURNS TABLE(id uuid)
    LANGUAGE plpgsql STABLE SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
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


--
-- Name: enforce_allowlist_external_sharing(); Type: FUNCTION; Schema: app; Owner: -
--

-- +goose StatementBegin
CREATE FUNCTION app.enforce_allowlist_external_sharing() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
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


--
-- Name: enforce_policy_external_sharing(); Type: FUNCTION; Schema: app; Owner: -
--

-- +goose StatementBegin
CREATE FUNCTION app.enforce_policy_external_sharing() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
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


--
-- Name: enforce_site_external_sharing(); Type: FUNCTION; Schema: app; Owner: -
--

-- +goose StatementBegin
CREATE FUNCTION app.enforce_site_external_sharing() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
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


--
-- Name: resolve_host(text); Type: FUNCTION; Schema: app; Owner: -
--

-- +goose StatementBegin
CREATE FUNCTION app.resolve_host(p_host text) RETURNS TABLE(host text, site_id uuid, org_id uuid, slug text, access_mode text, version_id uuid)
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
    AS $$
    SELECT hr.host, s.id, s.org_id, s.slug, s.access_mode, s.current_version_id
    FROM app.host_routes hr
    JOIN app.sites s ON s.id = hr.site_id
    WHERE hr.host = p_host;
$$;
-- +goose StatementEnd




--
-- Name: allowlist_entries; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.allowlist_entries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    org_id uuid NOT NULL,
    site_id uuid NOT NULL,
    email text NOT NULL,
    is_external boolean DEFAULT false NOT NULL,
    claimed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    claimed_by_user_id uuid
);

ALTER TABLE ONLY app.allowlist_entries FORCE ROW LEVEL SECURITY;


--
-- Name: audit_log; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.audit_log (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    org_id uuid NOT NULL,
    actor_user uuid,
    action text NOT NULL,
    target text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip inet,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    actor_token uuid,
    request_id text,
    trace_id text
);

ALTER TABLE ONLY app.audit_log FORCE ROW LEVEL SECURITY;


--
-- Name: deploy_tokens; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.deploy_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    org_id uuid NOT NULL,
    token_hash text NOT NULL,
    scopes text[] DEFAULT ARRAY['deploy'::text] NOT NULL,
    site_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone
);

ALTER TABLE ONLY app.deploy_tokens FORCE ROW LEVEL SECURITY;


--
-- Name: domains; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.domains (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    org_id uuid NOT NULL,
    site_id uuid NOT NULL,
    hostname text NOT NULL,
    verify_status text DEFAULT 'pending'::text NOT NULL,
    tls_status text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    cf_hostname_id text,
    dcv_record text,
    CONSTRAINT domains_tls_status_check CHECK ((tls_status = ANY (ARRAY['pending'::text, 'issued'::text, 'failed'::text]))),
    CONSTRAINT domains_verify_status_check CHECK ((verify_status = ANY (ARRAY['pending'::text, 'verifying'::text, 'verified'::text, 'failed'::text])))
);

ALTER TABLE ONLY app.domains FORCE ROW LEVEL SECURITY;


--
-- Name: host_routes; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.host_routes (
    host text NOT NULL,
    org_id uuid NOT NULL,
    site_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY app.host_routes FORCE ROW LEVEL SECURITY;


--
-- Name: org_blobs; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.org_blobs (
    org_id uuid NOT NULL,
    content_hash text NOT NULL,
    size_bytes bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT org_blobs_size_bytes_check CHECK ((size_bytes >= 0))
);

ALTER TABLE ONLY app.org_blobs FORCE ROW LEVEL SECURITY;


--
-- Name: org_meta; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.org_meta (
    id uuid NOT NULL,
    plan_tier text DEFAULT 'free'::text NOT NULL,
    allow_external_sharing boolean DEFAULT false NOT NULL,
    default_visibility text DEFAULT 'org_only'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    org_status text DEFAULT 'active'::text NOT NULL,
    CONSTRAINT org_meta_org_status_check CHECK ((org_status = ANY (ARRAY['active'::text, 'suspended'::text, 'over_limit'::text])))
);

ALTER TABLE ONLY app.org_meta FORCE ROW LEVEL SECURITY;


--
-- Name: org_usage; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.org_usage (
    org_id uuid NOT NULL,
    members_count integer DEFAULT 0 NOT NULL,
    sites_count integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    storage_bytes bigint DEFAULT 0 NOT NULL,
    CONSTRAINT org_usage_storage_bytes_check CHECK ((storage_bytes >= 0))
);

ALTER TABLE ONLY app.org_usage FORCE ROW LEVEL SECURITY;


--
-- Name: site_access_policy; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.site_access_policy (
    site_id uuid NOT NULL,
    org_id uuid NOT NULL,
    mode text DEFAULT 'public'::text NOT NULL,
    password_hash text,
    expires_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    unlisted boolean DEFAULT false NOT NULL,
    CONSTRAINT site_access_policy_mode_check CHECK ((mode = ANY (ARRAY['public'::text, 'password'::text, 'allowlist'::text, 'org_only'::text])))
);

ALTER TABLE ONLY app.site_access_policy FORCE ROW LEVEL SECURITY;


--
-- Name: site_versions; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.site_versions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    org_id uuid NOT NULL,
    site_id uuid NOT NULL,
    version_no integer NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    r2_prefix text NOT NULL,
    content_hash text NOT NULL,
    size_bytes bigint DEFAULT 0 NOT NULL,
    created_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT site_versions_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'uploading'::text, 'ready'::text, 'failed'::text])))
);

ALTER TABLE ONLY app.site_versions FORCE ROW LEVEL SECURITY;


--
-- Name: sites; Type: TABLE; Schema: app; Owner: -
--

CREATE TABLE app.sites (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    org_id uuid NOT NULL,
    slug text NOT NULL,
    owner_user_id uuid NOT NULL,
    access_mode text DEFAULT 'public'::text NOT NULL,
    current_version_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sites_access_mode_check CHECK ((access_mode = ANY (ARRAY['public'::text, 'password'::text, 'allowlist'::text, 'org_only'::text])))
);

ALTER TABLE ONLY app.sites FORCE ROW LEVEL SECURITY;


--
-- Name: allowlist_entries allowlist_entries_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.allowlist_entries
    ADD CONSTRAINT allowlist_entries_pkey PRIMARY KEY (id);


--
-- Name: allowlist_entries allowlist_entries_site_email_key; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.allowlist_entries
    ADD CONSTRAINT allowlist_entries_site_email_key UNIQUE (site_id, email);


--
-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);


--
-- Name: deploy_tokens deploy_tokens_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.deploy_tokens
    ADD CONSTRAINT deploy_tokens_pkey PRIMARY KEY (id);


--
-- Name: deploy_tokens deploy_tokens_token_hash_key; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.deploy_tokens
    ADD CONSTRAINT deploy_tokens_token_hash_key UNIQUE (token_hash);


--
-- Name: domains domains_hostname_key; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.domains
    ADD CONSTRAINT domains_hostname_key UNIQUE (hostname);


--
-- Name: domains domains_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.domains
    ADD CONSTRAINT domains_pkey PRIMARY KEY (id);


--
-- Name: host_routes host_routes_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.host_routes
    ADD CONSTRAINT host_routes_pkey PRIMARY KEY (host);


--
-- Name: org_blobs org_blobs_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.org_blobs
    ADD CONSTRAINT org_blobs_pkey PRIMARY KEY (org_id, content_hash);


--
-- Name: org_meta org_meta_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.org_meta
    ADD CONSTRAINT org_meta_pkey PRIMARY KEY (id);


--
-- Name: org_usage org_usage_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.org_usage
    ADD CONSTRAINT org_usage_pkey PRIMARY KEY (org_id);


--
-- Name: site_access_policy site_access_policy_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.site_access_policy
    ADD CONSTRAINT site_access_policy_pkey PRIMARY KEY (site_id);


--
-- Name: site_versions site_versions_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.site_versions
    ADD CONSTRAINT site_versions_pkey PRIMARY KEY (id);


--
-- Name: site_versions site_versions_site_content_hash_key; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.site_versions
    ADD CONSTRAINT site_versions_site_content_hash_key UNIQUE (site_id, content_hash);


--
-- Name: site_versions site_versions_site_version_no_key; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.site_versions
    ADD CONSTRAINT site_versions_site_version_no_key UNIQUE (site_id, version_no);


--
-- Name: sites sites_org_slug_key; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.sites
    ADD CONSTRAINT sites_org_slug_key UNIQUE (org_id, slug);


--
-- Name: sites sites_pkey; Type: CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.sites
    ADD CONSTRAINT sites_pkey PRIMARY KEY (id);


--
-- Name: allowlist_entries_org_id_email_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX allowlist_entries_org_id_email_idx ON app.allowlist_entries USING btree (org_id, email);


--
-- Name: allowlist_entries_org_id_site_id_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX allowlist_entries_org_id_site_id_idx ON app.allowlist_entries USING btree (org_id, site_id);


--
-- Name: audit_log_org_id_created_at_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX audit_log_org_id_created_at_idx ON app.audit_log USING btree (org_id, created_at DESC);


--
-- Name: deploy_tokens_org_id_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX deploy_tokens_org_id_idx ON app.deploy_tokens USING btree (org_id);


--
-- Name: domains_org_id_site_id_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX domains_org_id_site_id_idx ON app.domains USING btree (org_id, site_id);


--
-- Name: host_routes_org_id_site_id_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX host_routes_org_id_site_id_idx ON app.host_routes USING btree (org_id, site_id);


--
-- Name: site_access_policy_org_id_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX site_access_policy_org_id_idx ON app.site_access_policy USING btree (org_id);


--
-- Name: site_versions_org_id_site_id_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX site_versions_org_id_site_id_idx ON app.site_versions USING btree (org_id, site_id, version_no DESC);


--
-- Name: sites_org_id_created_at_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX sites_org_id_created_at_idx ON app.sites USING btree (org_id, created_at DESC);


--
-- Name: sites_org_id_owner_idx; Type: INDEX; Schema: app; Owner: -
--

CREATE INDEX sites_org_id_owner_idx ON app.sites USING btree (org_id, owner_user_id);


--
-- Name: allowlist_entries allowlist_entries_external_sharing_guard; Type: TRIGGER; Schema: app; Owner: -
--

CREATE TRIGGER allowlist_entries_external_sharing_guard BEFORE INSERT OR UPDATE OF is_external, org_id ON app.allowlist_entries FOR EACH ROW EXECUTE FUNCTION app.enforce_allowlist_external_sharing();


--
-- Name: site_access_policy site_access_policy_external_sharing_guard; Type: TRIGGER; Schema: app; Owner: -
--

CREATE TRIGGER site_access_policy_external_sharing_guard BEFORE INSERT OR UPDATE OF mode, org_id ON app.site_access_policy FOR EACH ROW EXECUTE FUNCTION app.enforce_policy_external_sharing();


--
-- Name: sites sites_external_sharing_guard; Type: TRIGGER; Schema: app; Owner: -
--

CREATE TRIGGER sites_external_sharing_guard BEFORE INSERT OR UPDATE OF access_mode, org_id ON app.sites FOR EACH ROW EXECUTE FUNCTION app.enforce_site_external_sharing();


--
-- Name: allowlist_entries allowlist_entries_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.allowlist_entries
    ADD CONSTRAINT allowlist_entries_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: allowlist_entries allowlist_entries_site_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.allowlist_entries
    ADD CONSTRAINT allowlist_entries_site_id_fkey FOREIGN KEY (site_id) REFERENCES app.sites(id) ON DELETE CASCADE;


--
-- Name: audit_log audit_log_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.audit_log
    ADD CONSTRAINT audit_log_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: deploy_tokens deploy_tokens_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.deploy_tokens
    ADD CONSTRAINT deploy_tokens_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: deploy_tokens deploy_tokens_site_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.deploy_tokens
    ADD CONSTRAINT deploy_tokens_site_id_fkey FOREIGN KEY (site_id) REFERENCES app.sites(id) ON DELETE CASCADE;


--
-- Name: domains domains_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.domains
    ADD CONSTRAINT domains_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: domains domains_site_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.domains
    ADD CONSTRAINT domains_site_id_fkey FOREIGN KEY (site_id) REFERENCES app.sites(id) ON DELETE CASCADE;


--
-- Name: host_routes host_routes_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.host_routes
    ADD CONSTRAINT host_routes_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: host_routes host_routes_site_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.host_routes
    ADD CONSTRAINT host_routes_site_id_fkey FOREIGN KEY (site_id) REFERENCES app.sites(id) ON DELETE CASCADE;


--
-- Name: org_blobs org_blobs_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.org_blobs
    ADD CONSTRAINT org_blobs_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: org_usage org_usage_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.org_usage
    ADD CONSTRAINT org_usage_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: site_access_policy site_access_policy_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.site_access_policy
    ADD CONSTRAINT site_access_policy_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: site_access_policy site_access_policy_site_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.site_access_policy
    ADD CONSTRAINT site_access_policy_site_id_fkey FOREIGN KEY (site_id) REFERENCES app.sites(id) ON DELETE CASCADE;


--
-- Name: site_versions site_versions_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.site_versions
    ADD CONSTRAINT site_versions_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: site_versions site_versions_site_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.site_versions
    ADD CONSTRAINT site_versions_site_id_fkey FOREIGN KEY (site_id) REFERENCES app.sites(id) ON DELETE CASCADE;


--
-- Name: sites sites_current_version_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.sites
    ADD CONSTRAINT sites_current_version_id_fkey FOREIGN KEY (current_version_id) REFERENCES app.site_versions(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: sites sites_org_id_fkey; Type: FK CONSTRAINT; Schema: app; Owner: -
--

ALTER TABLE ONLY app.sites
    ADD CONSTRAINT sites_org_id_fkey FOREIGN KEY (org_id) REFERENCES app.org_meta(id) ON DELETE CASCADE;


--
-- Name: allowlist_entries; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.allowlist_entries ENABLE ROW LEVEL SECURITY;

--
-- Name: allowlist_entries allowlist_entries_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY allowlist_entries_tenant_isolation ON app.allowlist_entries USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: audit_log; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.audit_log ENABLE ROW LEVEL SECURITY;

--
-- Name: audit_log audit_log_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY audit_log_tenant_isolation ON app.audit_log USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: deploy_tokens; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.deploy_tokens ENABLE ROW LEVEL SECURITY;

--
-- Name: deploy_tokens deploy_tokens_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY deploy_tokens_tenant_isolation ON app.deploy_tokens USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: domains; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.domains ENABLE ROW LEVEL SECURITY;

--
-- Name: domains domains_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY domains_tenant_isolation ON app.domains USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: host_routes; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.host_routes ENABLE ROW LEVEL SECURITY;

--
-- Name: host_routes host_routes_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY host_routes_tenant_isolation ON app.host_routes USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: org_blobs; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.org_blobs ENABLE ROW LEVEL SECURITY;

--
-- Name: org_blobs org_blobs_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY org_blobs_tenant_isolation ON app.org_blobs USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: org_meta; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.org_meta ENABLE ROW LEVEL SECURITY;

--
-- Name: org_meta org_meta_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY org_meta_tenant_isolation ON app.org_meta USING ((id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: org_usage; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.org_usage ENABLE ROW LEVEL SECURITY;

--
-- Name: org_usage org_usage_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY org_usage_tenant_isolation ON app.org_usage USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: site_access_policy; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.site_access_policy ENABLE ROW LEVEL SECURITY;

--
-- Name: site_access_policy site_access_policy_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY site_access_policy_tenant_isolation ON app.site_access_policy USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: site_versions; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.site_versions ENABLE ROW LEVEL SECURITY;

--
-- Name: site_versions site_versions_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY site_versions_tenant_isolation ON app.site_versions USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: sites; Type: ROW SECURITY; Schema: app; Owner: -
--

ALTER TABLE app.sites ENABLE ROW LEVEL SECURITY;

--
-- Name: sites sites_tenant_isolation; Type: POLICY; Schema: app; Owner: -
--

CREATE POLICY sites_tenant_isolation ON app.sites USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid)) WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));


--
-- Name: SCHEMA app; Type: ACL; Schema: -; Owner: -
--

GRANT USAGE ON SCHEMA app TO dropway_app;


--
-- Name: SCHEMA identity; Type: ACL; Schema: -; Owner: -
--

GRANT USAGE ON SCHEMA identity TO dropway_app;


--
-- Name: FUNCTION all_org_ids(); Type: ACL; Schema: app; Owner: -
--

REVOKE ALL ON FUNCTION app.all_org_ids() FROM PUBLIC;
GRANT ALL ON FUNCTION app.all_org_ids() TO dropway_app;


--
-- Name: FUNCTION resolve_host(p_host text); Type: ACL; Schema: app; Owner: -
--

GRANT ALL ON FUNCTION app.resolve_host(p_host text) TO dropway_app;


--
-- Name: TABLE allowlist_entries; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.allowlist_entries TO dropway_app;


--
-- Name: TABLE audit_log; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.audit_log TO dropway_app;


--
-- Name: TABLE deploy_tokens; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.deploy_tokens TO dropway_app;


--
-- Name: TABLE domains; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.domains TO dropway_app;


--
-- Name: TABLE host_routes; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.host_routes TO dropway_app;


--
-- Name: TABLE org_blobs; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.org_blobs TO dropway_app;


--
-- Name: TABLE org_meta; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.org_meta TO dropway_app;


--
-- Name: TABLE org_usage; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.org_usage TO dropway_app;


--
-- Name: TABLE site_access_policy; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.site_access_policy TO dropway_app;


--
-- Name: TABLE site_versions; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.site_versions TO dropway_app;


--
-- Name: TABLE sites; Type: ACL; Schema: app; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.sites TO dropway_app;


--
-- Name: DEFAULT PRIVILEGES FOR TABLES; Type: DEFAULT ACL; Schema: identity; Owner: -
--

ALTER DEFAULT PRIVILEGES IN SCHEMA identity GRANT SELECT ON TABLES TO dropway_app;


--
-- PostgreSQL database dump complete
--

-- +goose Down
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA identity REVOKE SELECT ON TABLES FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT ON ALL TABLES IN SCHEMA identity FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE USAGE ON SCHEMA identity FROM dropway_app;
-- +goose StatementEnd
-- +goose StatementBegin
-- Drops every app.* object (tables, functions, policies, grants) with it. We
-- leave the identity schema (Better Auth owns it) and the cluster-global role.
DROP SCHEMA IF EXISTS app CASCADE;
-- +goose StatementEnd
