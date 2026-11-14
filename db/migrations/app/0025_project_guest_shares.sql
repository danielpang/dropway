-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Project-scoped email invitations do not enroll recipients in organizations.

-- +goose Up
ALTER TABLE app.project_rooms ADD CONSTRAINT project_rooms_org_id_id_key UNIQUE(org_id,id);
CREATE TABLE app.project_guest_shares (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
 room_id uuid NOT NULL,
 email text NOT NULL CHECK(email=lower(email)),
 role text NOT NULL CHECK(role IN ('viewer','reviewer')),
 token_hash text NOT NULL UNIQUE,
 invited_by uuid NOT NULL,
 accepted_user_id uuid,accepted_at timestamptz,
 expires_at timestamptz NOT NULL,revoked_at timestamptz,
 last_invited_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(org_id,room_id) REFERENCES app.project_rooms(org_id,id) ON DELETE CASCADE,
 UNIQUE(org_id,room_id,email)
);
CREATE INDEX project_guest_shares_org_idx ON app.project_guest_shares(org_id,room_id,created_at,id);
CREATE INDEX project_guest_shares_user_idx ON app.project_guest_shares(accepted_user_id,room_id);
ALTER TABLE app.project_guest_shares ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.project_guest_shares FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app.project_guest_shares USING(org_id=NULLIF(current_setting('app.current_org_id',true),'')::uuid) WITH CHECK(org_id=NULLIF(current_setting('app.current_org_id',true),'')::uuid);
GRANT SELECT,INSERT,UPDATE,DELETE ON app.project_guest_shares TO dropway_app;
-- Discovery reveals only the host tenant/project IDs accessible to this verified
-- identity, before a tenant context exists. It never grants organization membership.
-- +goose StatementBegin
CREATE FUNCTION app.discover_guest_projects(p_user uuid,p_token text,p_room uuid)
RETURNS TABLE(org_id uuid,room_id uuid) LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path TO app,pg_temp AS $$
BEGIN
 RETURN QUERY SELECT g.org_id,g.room_id FROM app.project_guest_shares g
 JOIN app.org_meta o ON o.id=g.org_id
 JOIN identity."user" u ON u.id=p_user::text
 WHERE lower(u.email)=g.email AND u."emailVerified"=true
 AND COALESCE((to_jsonb(u)->>'banned')::boolean,false)=false
 AND o.project_rooms_enabled AND o.allow_external_sharing
 AND g.revoked_at IS NULL
 AND (p_room IS NULL OR g.room_id=p_room)
 AND ((p_token IS NULL AND g.accepted_user_id=p_user AND g.accepted_at IS NOT NULL)
      OR (p_token IS NOT NULL AND g.expires_at>now() AND g.token_hash=p_token AND (g.accepted_user_id IS NULL OR g.accepted_user_id=p_user)))
 ORDER BY g.created_at DESC,g.id LIMIT 200;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.discover_guest_projects(uuid,text,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.discover_guest_projects(uuid,text,uuid) TO dropway_app;
-- +goose Down
DROP FUNCTION app.discover_guest_projects(uuid,text,uuid);
DROP TABLE app.project_guest_shares;
ALTER TABLE app.project_rooms DROP CONSTRAINT project_rooms_org_id_id_key;
