-- SPDX-License-Identifier: LicenseRef-Dropway-Proprietary
--
-- =============================================================================
-- CLOUD-ONLY / PROPRIETARY MIGRATION -- NOT part of the FSL open-core build.
-- =============================================================================
--
-- 0002_business_tier.sql
--
-- Adds the new Business ($150, unlimited sites) plan tier and completes the tier
-- rename started in db/migrations/app/0004:
--   - Widen billing.subscriptions.plan_tier CHECK to allow the renamed 'pro' tier
--     ($25) alongside the new 'business' tier ($150). Final key space:
--     free / pro / business / enterprise.
--   - Migrate existing subscribers off the old 'business' key onto 'pro' so their
--     $25 entitlement is preserved. The new 'business' tier ($150) has no existing
--     rows, so there is no collision.
--
-- Run ordering: the `app` migrations run first (documented in
-- db/migrations/app/README.md), so app.org_meta.plan_tier is already on 'pro' by
-- the time this runs; the two updates are independent.

-- +goose Up
-- +goose StatementBegin
-- Drop + re-add (rather than a bare ADD) so the auto-named constraint stays stable.
ALTER TABLE billing.subscriptions DROP CONSTRAINT subscriptions_plan_tier_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE billing.subscriptions
    ADD CONSTRAINT subscriptions_plan_tier_check
    CHECK (plan_tier IN ('free', 'pro', 'business', 'enterprise'));
-- +goose StatementEnd
-- +goose StatementBegin
-- Preserve existing $25 subscribers: old 'business' key -> 'pro'.
UPDATE billing.subscriptions SET plan_tier = 'pro' WHERE plan_tier = 'business';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE billing.subscriptions SET plan_tier = 'business' WHERE plan_tier = 'pro';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE billing.subscriptions DROP CONSTRAINT subscriptions_plan_tier_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE billing.subscriptions
    ADD CONSTRAINT subscriptions_plan_tier_check
    CHECK (plan_tier IN ('free', 'business', 'enterprise'));
-- +goose StatementEnd
