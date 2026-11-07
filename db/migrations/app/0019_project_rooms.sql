-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0019_project_rooms.sql
--
-- Same-org project rooms for private review. A room REFERENCES existing
-- sites; it does not copy them and does not change sites.owner_user_id.
-- Feed comments (app.post_comments) stay a separate table.
--
-- Room rows are org-isolated by RLS. Same-org membership is NOT an RLS
-- predicate (policies stay subquery-free); every API query also filters on
-- the caller's membership. Private reviews are not written into the public
-- serving projection.
--
-- project_rooms_enabled defaults FALSE.
--
-- app.resolve_api_key also returns scopes and site_id so the auth boundary
-- can enforce them. Default keys stay scopes '{sites:*}' and site_id NULL.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.org_meta
    ADD COLUMN project_rooms_enabled boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.project_rooms (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    name          text NOT NULL,
    owner_user_id uuid NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_rooms_name_check CHECK (char_length(name) BETWEEN 1 AND 80)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX project_rooms_org_idx ON app.project_rooms (org_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.project_room_members (
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    room_id    uuid NOT NULL REFERENCES app.project_rooms (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL,
    role       text NOT NULL CHECK (role IN ('owner', 'editor', 'reviewer', 'viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX project_room_members_org_idx ON app.project_room_members (org_id, user_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX project_room_members_one_owner
    ON app.project_room_members (room_id)
    WHERE role = 'owner';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.project_room_sites (
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    room_id    uuid NOT NULL REFERENCES app.project_rooms (id) ON DELETE CASCADE,
    site_id    uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, site_id)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX project_room_sites_org_idx ON app.project_room_sites (org_id, room_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.review_threads (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    room_id      uuid NOT NULL REFERENCES app.project_rooms (id) ON DELETE CASCADE,
    site_id      uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    version_id   uuid NOT NULL REFERENCES app.site_versions (id) ON DELETE CASCADE,
    content_hash text NOT NULL,
    page         text NOT NULL,
    anchor_kind  text NOT NULL CHECK (anchor_kind IN ('point', 'text', 'component', 'page')),
    anchor       jsonb NOT NULL,
    anchor_state text NOT NULL DEFAULT 'anchored' CHECK (anchor_state IN ('anchored', 'orphaned')),
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    created_by   uuid NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    resolved_by  uuid,
    resolved_at  timestamptz,
    CONSTRAINT review_threads_page_check CHECK (char_length(page) BETWEEN 1 AND 512)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX review_threads_org_idx ON app.review_threads (org_id, room_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.review_anchor_revisions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    thread_id     uuid NOT NULL REFERENCES app.review_threads (id) ON DELETE CASCADE,
    anchor_kind   text NOT NULL CHECK (anchor_kind IN ('point', 'text', 'component', 'page')),
    anchor        jsonb NOT NULL,
    page          text NOT NULL,
    anchor_state  text NOT NULL CHECK (anchor_state IN ('anchored', 'orphaned')),
    reason        text NOT NULL CHECK (reason IN ('created', 'reanchored', 'orphaned')),
    actor_user_id uuid NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX review_anchor_revisions_org_idx
    ON app.review_anchor_revisions (org_id, thread_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.review_replies (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    thread_id      uuid NOT NULL REFERENCES app.review_threads (id) ON DELETE CASCADE,
    author_user_id uuid NOT NULL,
    body           text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT review_replies_body_check CHECK (char_length(body) BETWEEN 1 AND 4000)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX review_replies_org_idx ON app.review_replies (org_id, thread_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.review_version_approvals (
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    room_id      uuid NOT NULL REFERENCES app.project_rooms (id) ON DELETE CASCADE,
    site_id      uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    version_id   uuid NOT NULL REFERENCES app.site_versions (id) ON DELETE CASCADE,
    content_hash text NOT NULL,
    user_id      uuid NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, version_id, user_id)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX review_version_approvals_org_idx
    ON app.review_version_approvals (org_id, room_id, version_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.review_read_state (
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    room_id      uuid NOT NULL REFERENCES app.project_rooms (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL,
    last_read_at timestamptz NOT NULL,
    PRIMARY KEY (room_id, user_id)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX review_read_state_org_idx ON app.review_read_state (org_id, room_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.review_activity (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    room_id       uuid NOT NULL REFERENCES app.project_rooms (id) ON DELETE CASCADE,
    actor_user_id uuid NOT NULL,
    kind          text NOT NULL CHECK (kind IN (
        'room_created', 'member_added', 'member_removed', 'member_role_changed',
        'ownership_transferred', 'site_added', 'site_removed',
        'thread_created', 'reply_created', 'thread_resolved', 'thread_reopened',
        'anchor_reanchored', 'anchor_orphaned', 'version_signoff', 'version_signoff_withdrawn',
        'candidate_submitted', 'candidate_approved', 'candidate_rejected', 'candidate_conflict'
    )),
    thread_id     uuid,
    site_id       uuid,
    version_id    uuid,
    created_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX review_activity_org_idx
    ON app.review_activity (org_id, room_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION app.project_room_site_same_org() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path TO app, pg_temp
AS $$
DECLARE
    site_org uuid;
    room_org uuid;
BEGIN
    SELECT org_id INTO site_org FROM app.sites WHERE id = NEW.site_id;
    SELECT org_id INTO room_org FROM app.project_rooms WHERE id = NEW.room_id;
    IF site_org IS NULL OR room_org IS NULL
       OR site_org IS DISTINCT FROM NEW.org_id
       OR room_org IS DISTINCT FROM NEW.org_id THEN
        RAISE EXCEPTION 'project room site reference must stay in the same org'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER project_room_sites_same_org
    BEFORE INSERT OR UPDATE ON app.project_room_sites
    FOR EACH ROW EXECUTE FUNCTION app.project_room_site_same_org();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION app.project_room_thread_pin_check() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path TO app, pg_temp
AS $$
DECLARE
    v_site uuid;
    v_org uuid;
    v_hash text;
BEGIN
    SELECT site_id, org_id, content_hash INTO v_site, v_org, v_hash
    FROM app.site_versions WHERE id = NEW.version_id;
    IF v_site IS NULL OR v_site IS DISTINCT FROM NEW.site_id
       OR v_org IS DISTINCT FROM NEW.org_id
       OR v_hash IS DISTINCT FROM NEW.content_hash THEN
        RAISE EXCEPTION 'project room thread pin does not match the site version'
            USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM app.project_room_sites s
        WHERE s.room_id = NEW.room_id AND s.site_id = NEW.site_id AND s.org_id = NEW.org_id
    ) THEN
        RAISE EXCEPTION 'project room thread site is not in the room'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER review_threads_pin_check
    BEFORE INSERT OR UPDATE OF version_id, content_hash, site_id, org_id, room_id
    ON app.review_threads
    FOR EACH ROW EXECUTE FUNCTION app.project_room_thread_pin_check();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION app.project_room_thread_pin_immutable() RETURNS trigger
LANGUAGE plpgsql
SET search_path TO app, pg_temp
AS $$
BEGIN
    IF NEW.org_id IS DISTINCT FROM OLD.org_id
       OR NEW.room_id IS DISTINCT FROM OLD.room_id
       OR NEW.site_id IS DISTINCT FROM OLD.site_id
       OR NEW.version_id IS DISTINCT FROM OLD.version_id
       OR NEW.content_hash IS DISTINCT FROM OLD.content_hash
       OR NEW.page IS DISTINCT FROM OLD.page
       OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
        RAISE EXCEPTION 'project room thread pin is immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER review_threads_pin_immutable
    BEFORE UPDATE ON app.review_threads
    FOR EACH ROW EXECUTE FUNCTION app.project_room_thread_pin_immutable();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION app.project_room_approval_pin_check() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path TO app, pg_temp
AS $$
DECLARE
    v_site uuid;
    v_org uuid;
    v_hash text;
BEGIN
    SELECT site_id, org_id, content_hash INTO v_site, v_org, v_hash
    FROM app.site_versions WHERE id = NEW.version_id;
    IF v_site IS NULL OR v_site IS DISTINCT FROM NEW.site_id
       OR v_org IS DISTINCT FROM NEW.org_id
       OR v_hash IS DISTINCT FROM NEW.content_hash THEN
        RAISE EXCEPTION 'project room approval does not match the site version'
            USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM app.project_room_sites s
        WHERE s.room_id = NEW.room_id AND s.site_id = NEW.site_id AND s.org_id = NEW.org_id
    ) THEN
        RAISE EXCEPTION 'project room approval site is not in the room'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER review_version_approvals_pin_check
    BEFORE INSERT OR UPDATE ON app.review_version_approvals
    FOR EACH ROW EXECUTE FUNCTION app.project_room_approval_pin_check();
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE ALL ON FUNCTION app.project_room_site_same_org() FROM PUBLIC;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE ALL ON FUNCTION app.project_room_thread_pin_check() FROM PUBLIC;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE ALL ON FUNCTION app.project_room_thread_pin_immutable() FROM PUBLIC;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE ALL ON FUNCTION app.project_room_approval_pin_check() FROM PUBLIC;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.project_rooms ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.project_rooms FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY project_rooms_tenant_isolation ON app.project_rooms
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.project_rooms TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.project_room_members ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.project_room_members FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY project_room_members_tenant_isolation ON app.project_room_members
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.project_room_members TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.project_room_sites ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.project_room_sites FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY project_room_sites_tenant_isolation ON app.project_room_sites
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.project_room_sites TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.review_threads ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.review_threads FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY review_threads_tenant_isolation ON app.review_threads
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.review_threads TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.review_anchor_revisions ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.review_anchor_revisions FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY review_anchor_revisions_tenant_isolation ON app.review_anchor_revisions
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.review_anchor_revisions TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.review_replies ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.review_replies FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY review_replies_tenant_isolation ON app.review_replies
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.review_replies TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.review_version_approvals ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.review_version_approvals FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY review_version_approvals_tenant_isolation ON app.review_version_approvals
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.review_version_approvals TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.review_read_state ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.review_read_state FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY review_read_state_tenant_isolation ON app.review_read_state
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.review_read_state TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.review_activity ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.review_activity FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY review_activity_tenant_isolation ON app.review_activity
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.review_activity TO dropway_app;
-- +goose StatementEnd

-- review_candidates: immutable proposed artifacts. Approval is a human
-- decision to APPLY a change. It is not reviewer version sign-off.
-- state defaults to pending. There is no timeout and no default approval.
-- content identity columns are immutable; a changed proposal is a new row.
-- +goose StatementBegin
CREATE TABLE app.review_candidates (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    room_id            uuid NOT NULL REFERENCES app.project_rooms (id) ON DELETE CASCADE,
    site_id            uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    base_version_id    uuid NOT NULL REFERENCES app.site_versions (id) ON DELETE CASCADE,
    base_content_hash  text NOT NULL,
    content_hash       text NOT NULL,
    diff_hash          text NOT NULL,
    summary            text NOT NULL,
    state              text NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'approved', 'rejected', 'conflict')),
    created_by         uuid NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    approved_by        uuid,
    approved_at        timestamptz,
    rejected_by        uuid,
    rejected_at        timestamptz,
    CONSTRAINT review_candidates_summary_check CHECK (char_length(summary) BETWEEN 1 AND 2000)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX review_candidates_org_idx ON app.review_candidates (org_id, room_id, created_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION app.review_candidate_immutable() RETURNS trigger
LANGUAGE plpgsql
SET search_path TO app, pg_temp
AS $$
BEGIN
    IF NEW.org_id IS DISTINCT FROM OLD.org_id
       OR NEW.room_id IS DISTINCT FROM OLD.room_id
       OR NEW.site_id IS DISTINCT FROM OLD.site_id
       OR NEW.base_version_id IS DISTINCT FROM OLD.base_version_id
       OR NEW.base_content_hash IS DISTINCT FROM OLD.base_content_hash
       OR NEW.content_hash IS DISTINCT FROM OLD.content_hash
       OR NEW.diff_hash IS DISTINCT FROM OLD.diff_hash
       OR NEW.summary IS DISTINCT FROM OLD.summary
       OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
        RAISE EXCEPTION 'project room candidate artifact is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.state <> 'pending' AND NEW.state IS DISTINCT FROM OLD.state THEN
        RAISE EXCEPTION 'project room candidate decision is terminal'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER review_candidates_immutable
    BEFORE UPDATE ON app.review_candidates
    FOR EACH ROW EXECUTE FUNCTION app.review_candidate_immutable();
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE ALL ON FUNCTION app.review_candidate_immutable() FROM PUBLIC;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.review_candidates ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.review_candidates FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY review_candidates_tenant_isolation ON app.review_candidates
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE app.review_candidates TO dropway_app;
-- +goose StatementEnd

-- Return scopes and site_id. Changing the OUT row type requires DROP first.
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.resolve_api_key(text);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION app.resolve_api_key(p_hash text)
    RETURNS TABLE(
        id uuid,
        org_id uuid,
        created_by uuid,
        expires_at timestamptz,
        revoked_at timestamptz,
        org_status text,
        api_keys_enabled boolean,
        scopes text[],
        site_id uuid
    )
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'app', 'pg_temp'
    AS $$
    SELECT k.id, k.org_id, k.created_by, k.expires_at, k.revoked_at,
           m.org_status, m.api_keys_enabled, k.scopes, k.site_id
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

-- +goose StatementBegin
DROP TABLE IF EXISTS app.review_candidates;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.review_candidate_immutable();
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.review_activity;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.review_read_state;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.review_version_approvals;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.review_replies;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.review_anchor_revisions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.review_threads;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.project_room_sites;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.project_room_members;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.project_rooms;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.project_room_approval_pin_check();
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.project_room_thread_pin_immutable();
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.project_room_thread_pin_check();
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS app.project_room_site_same_org();
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS project_rooms_enabled;
-- +goose StatementEnd
