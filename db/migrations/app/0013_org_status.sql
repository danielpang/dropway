-- 0013_org_status.sql — per-org content status (self-host abuse/takedown lever).
--
-- app.org_meta gains an `org_status` the serving plane reads BEFORE streaming any
-- tenant content (public OR gated): 'suspended'/'over_limit' ⇒ the edge returns a
-- 503 platform page instead of the site, matching the Cloudflare Worker's
-- org_status:<org_id> KV gate. Self-host (OSS) is unlimited and has no billing, so
-- this is primarily an ABUSE / TAKEDOWN suspension lever (ARCHITECTURE.md §10) an
-- operator sets by hand:  UPDATE app.org_meta SET org_status='suspended' WHERE id=…
-- The hosted-cloud build mirrors billing.subscriptions.org_status onto this column.
-- Default 'active' ⇒ existing orgs and every new org are unaffected.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.org_meta
    ADD COLUMN org_status text NOT NULL DEFAULT 'active'
        CHECK (org_status IN ('active', 'suspended', 'over_limit'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN org_status;
-- +goose StatementEnd
