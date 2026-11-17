-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- A bearer invitation can disclose its own recipient and sign-in path, without
-- granting project access or allowing arbitrary email/account enumeration.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION app.inspect_project_invitation(p_token_hash text)
RETURNS TABLE(org_id uuid, owner_user_id uuid, email text, project_name text, has_account boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path TO app,pg_temp AS $$
BEGIN
 RETURN QUERY
 SELECT g.org_id,r.owner_user_id,g.email,r.name,
   EXISTS(SELECT 1 FROM identity."user" u WHERE lower(u.email)=g.email)
 FROM app.project_guest_shares g
 JOIN app.project_rooms r ON r.org_id=g.org_id AND r.id=g.room_id
 JOIN app.org_meta o ON o.id=g.org_id
 WHERE g.token_hash=p_token_hash AND g.revoked_at IS NULL AND g.expires_at>now()
   AND o.project_rooms_enabled AND o.allow_external_sharing
 LIMIT 1;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.inspect_project_invitation(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.inspect_project_invitation(text) TO dropway_app;

-- +goose Down
DROP FUNCTION app.inspect_project_invitation(text);
