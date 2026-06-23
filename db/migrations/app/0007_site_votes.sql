-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0007_site_votes.sql
--
-- Up/down votes on feed posts. Each org member casts at most one vote per site
-- (value +1 or -1); the feed shows the net score and the viewer's own vote so it
-- can render the up/down controls in their live state. One row per (site, user);
-- "un-voting" deletes the row. RLS scopes votes to their org like every other
-- per-site table.

-- +goose Up
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
-- Sum/aggregate a site's score cheaply (the feed reads SUM(value) per site).
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

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.site_votes;
-- +goose StatementEnd
