-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0009_feed_skill_posts.sql
--
-- Bring SKILLS into the org feed as first-class posts, alongside sites.
--
--   1. Skills gain feed_visible (default true), so a skill auto-joins the feed
--      when it is published (via UI, MCP, or CLI) — exactly like sites — and the
--      owner/admin can make it private to pull it off. Skills already carry
--      title/description, which serve as the feed post's title/description.
--
--   2. Votes and comments become POLYMORPHIC over a (subject_type, subject_id)
--      pair so a single up/down vote and a single comment thread work for both
--      sites and skills. The old per-site app.site_votes / app.site_comments
--      tables are replaced by app.post_votes / app.post_comments; existing rows
--      migrate over as subject_type = 'site'.
--
-- The polymorphic tables can't FK to two parents, so a deleted site/skill's
-- votes+comments are cleaned up in the store's delete path (DeleteSite /
-- DeleteSkill) rather than by ON DELETE CASCADE. RLS scopes both tables to their
-- org exactly like the tables they replace.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.skills
    ADD COLUMN feed_visible boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose StatementBegin
-- Backs the feed listing of the org's non-private skills, newest first. Partial
-- on feed_visible so private skills stay out of the scanned index (mirrors
-- sites_feed_idx).
CREATE INDEX skills_feed_idx
    ON app.skills USING btree (org_id, created_at DESC)
    WHERE feed_visible;
-- +goose StatementEnd

-- --- post_votes: polymorphic up/down votes ---------------------------------

-- +goose StatementBegin
CREATE TABLE app.post_votes (
    subject_type text NOT NULL,
    subject_id   uuid NOT NULL,
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL,
    value        smallint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subject_type, subject_id, user_id),
    CONSTRAINT post_votes_subject_type_check CHECK (subject_type IN ('site', 'skill')),
    CONSTRAINT post_votes_value_check CHECK (value IN (-1, 1))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Sum/aggregate one post's score cheaply (the feed reads SUM(value) per subject).
CREATE INDEX post_votes_subject_idx ON app.post_votes USING btree (subject_type, subject_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Carry the existing per-site votes over as 'site' subjects.
INSERT INTO app.post_votes (subject_type, subject_id, org_id, user_id, value, created_at, updated_at)
SELECT 'site', site_id, org_id, user_id, value, created_at, updated_at
FROM app.site_votes;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.post_votes ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.post_votes FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE POLICY post_votes_tenant_isolation ON app.post_votes
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.post_votes TO dropway_app;
-- +goose StatementEnd

-- --- post_comments: polymorphic comment threads ----------------------------

-- +goose StatementBegin
CREATE TABLE app.post_comments (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    subject_type       text NOT NULL,
    subject_id         uuid NOT NULL,
    author_user_id     uuid NOT NULL,
    body               text NOT NULL,
    mentioned_user_ids uuid[] NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT post_comments_subject_type_check CHECK (subject_type IN ('site', 'skill'))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Lists one post's comments oldest-first (a conversation thread) — RLS scopes to org.
CREATE INDEX post_comments_subject_idx ON app.post_comments USING btree (subject_type, subject_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Carry the existing per-site comments over as 'site' subjects.
INSERT INTO app.post_comments (id, org_id, subject_type, subject_id, author_user_id, body, mentioned_user_ids, created_at)
SELECT id, org_id, 'site', site_id, author_user_id, body, mentioned_user_ids, created_at
FROM app.site_comments;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.post_comments ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.post_comments FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE POLICY post_comments_tenant_isolation ON app.post_comments
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.post_comments TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS app.site_votes;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.site_comments;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Recreate the per-site tables (shape from migrations 0006/0007).
CREATE TABLE app.site_comments (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id            uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    author_user_id     uuid NOT NULL,
    body               text NOT NULL,
    mentioned_user_ids uuid[] NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
-- +goose StatementBegin
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

-- +goose StatementBegin
CREATE TABLE app.site_votes (
    site_id    uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL,
    value      smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, user_id),
    CONSTRAINT site_votes_value_check CHECK (value IN (-1, 1))
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX site_votes_site_idx ON app.site_votes USING btree (site_id);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_votes ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.site_votes FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY site_votes_tenant_isolation ON app.site_votes
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.site_votes TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
-- Copy the 'site' subjects back into the per-site tables.
INSERT INTO app.site_votes (site_id, org_id, user_id, value, created_at, updated_at)
SELECT subject_id, org_id, user_id, value, created_at, updated_at
FROM app.post_votes WHERE subject_type = 'site';
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO app.site_comments (id, org_id, site_id, author_user_id, body, mentioned_user_ids, created_at)
SELECT id, org_id, subject_id, author_user_id, body, mentioned_user_ids, created_at
FROM app.post_comments WHERE subject_type = 'site';
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS app.post_votes;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.post_comments;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS app.skills_feed_idx;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.skills DROP COLUMN IF EXISTS feed_visible;
-- +goose StatementEnd
