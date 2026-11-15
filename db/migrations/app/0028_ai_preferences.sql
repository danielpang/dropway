-- +goose Up
CREATE TABLE app.personal_ai_preferences (
 org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
 user_id uuid NOT NULL,
 mode text NOT NULL DEFAULT 'local' CHECK (mode IN ('local','api')),
 credential_ref uuid REFERENCES app.provider_credentials(id) ON DELETE SET NULL,
 provider text NOT NULL DEFAULT '',
 model text NOT NULL DEFAULT '',
 PRIMARY KEY(org_id,user_id)
);
CREATE TABLE app.project_ai_preferences (
 org_id uuid NOT NULL,
 room_id uuid NOT NULL,
 provider text NOT NULL DEFAULT '',
 model text NOT NULL DEFAULT '',
 PRIMARY KEY(org_id,room_id),
 FOREIGN KEY(org_id,room_id) REFERENCES app.project_rooms(org_id,id) ON DELETE CASCADE
);
ALTER TABLE app.personal_ai_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.personal_ai_preferences FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant ON app.personal_ai_preferences USING (org_id = NULLIF(current_setting('app.current_org_id',true),'')::uuid);
ALTER TABLE app.project_ai_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.project_ai_preferences FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant ON app.project_ai_preferences USING (org_id = NULLIF(current_setting('app.current_org_id',true),'')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON app.personal_ai_preferences, app.project_ai_preferences TO dropway_app;
-- +goose Down
DROP TABLE app.project_ai_preferences;
DROP TABLE app.personal_ai_preferences;
