-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0005_site_feed_visible.sql
--
-- The org feed: a per-org discovery surface where members see each other's
-- sites, newest first. A site joins the feed automatically when it is created
-- or published, UNLESS its owner marks it "private" (kept off the feed).
--
-- We model that opt-out as a per-site boolean rather than a fifth access_mode:
-- the four access modes (public/password/allowlist/org_only) are the EDGE
-- access-control axis (who can load the served bytes), while feed_visible is a
-- separate, dashboard-only DISCOVERY axis (does this site show up in teammates'
-- feed). Keeping them orthogonal means the feed toggle never has to touch the
-- edge projection / authz / external-sharing trigger — a private site keeps
-- whatever access mode it had, it's just hidden from the feed listing.
--
-- Defaults to true so every existing and future site is auto-shared to the
-- feed; the owner flips it false to make a site private.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.sites
    ADD COLUMN feed_visible boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose StatementBegin
-- Backs the feed listing (active org's non-private sites, newest first). Partial
-- on feed_visible so private sites stay out of the index the feed query scans.
CREATE INDEX sites_feed_idx
    ON app.sites USING btree (org_id, created_at DESC)
    WHERE feed_visible;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS app.sites_feed_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.sites
    DROP COLUMN IF EXISTS feed_visible;
-- +goose StatementEnd
