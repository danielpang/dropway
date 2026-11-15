-- +goose Up
-- PostHog remains the rollout gate. A separate organization opt-in is no longer
-- required before creating a first project. Existing explicit opt-outs, and
-- organizations that have already used rooms, keep their current setting.
ALTER TABLE app.org_meta ALTER COLUMN project_rooms_enabled SET DEFAULT true;

UPDATE app.org_meta AS org
SET project_rooms_enabled = true
WHERE NOT org.project_rooms_enabled
  AND NOT EXISTS (
    SELECT 1 FROM app.audit_log AS event
    WHERE event.org_id = org.id AND event.action = 'org.project_rooms_toggle'
  )
  AND NOT EXISTS (
    SELECT 1 FROM app.project_rooms AS room WHERE room.org_id = org.id
  );

-- +goose Down
-- Restore the default only. Do not revoke projects created since the upgrade,
-- or overwrite organization settings that an administrator has since changed.
ALTER TABLE app.org_meta ALTER COLUMN project_rooms_enabled SET DEFAULT false;
