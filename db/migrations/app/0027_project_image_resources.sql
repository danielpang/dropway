-- +goose Up
-- Image resources share the site's immutable version, quota, RLS and comment
-- machinery, but are explicitly typed instead of inferred from a generated slug.
ALTER TABLE app.sites ADD COLUMN resource_kind text NOT NULL DEFAULT 'site'
  CHECK (resource_kind IN ('site', 'image'));

-- +goose Down
ALTER TABLE app.sites DROP COLUMN resource_kind;
