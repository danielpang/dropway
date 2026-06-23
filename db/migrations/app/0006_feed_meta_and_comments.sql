-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0006_feed_meta_and_comments.sql
--
-- Two feed enhancements:
--
--   1. Per-site feed metadata: an optional human Title and Description the owner
--      sets so a site reads as more than its slug in the org feed. Both nullable
--      (the feed falls back to the slug when no title is set).
--
--   2. Site comments: org members can discuss a shared site and tag specific
--      teammates. mentioned_user_ids is the set of tagged org users (identity ids,
--      so no app-schema FK — same as sites.owner_user_id). RLS scopes every
--      comment to its org, exactly like the other per-site tables.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.sites
    ADD COLUMN title       text,
    ADD COLUMN description text;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.site_comments (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id            uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    author_user_id     uuid NOT NULL,
    body               text NOT NULL,
    -- Tagged org users (identity ids). Empty when the comment mentions no one.
    mentioned_user_ids uuid[] NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Lists a site's comments oldest-first (a conversation thread) — RLS scopes to org.
CREATE INDEX site_comments_site_idx ON app.site_comments USING btree (site_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.site_comments ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_comments FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE POLICY site_comments_tenant_isolation ON app.site_comments
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.site_comments TO dropway_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.site_comments;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.sites
    DROP COLUMN IF EXISTS title,
    DROP COLUMN IF EXISTS description;
-- +goose StatementEnd
