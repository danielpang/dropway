-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0004_rename_business_to_pro.sql
--
-- Tier rename for the new Business ($150, unlimited sites) plan. The internal key
-- 'business' historically meant the $25 entry paid tier (labeled "Pro" in the UI).
-- We rename it to 'pro' so the internal keys finally match the public labels
-- (free / pro / business / enterprise), and free up 'business' for the new $150
-- unlimited-sites tier between Pro and Enterprise.
--
-- This migrates the AUTHORITATIVE entitlement (app.org_meta.plan_tier). Any org on
-- the old 'business' key keeps its existing $25 entitlement, now under 'pro'. The
-- billing.subscriptions mirror is migrated by the cloud-only billing migration
-- (db/migrations/billing/0002), which also widens its CHECK constraint. On the
-- OSS/self-host build no org is ever non-free, so this is a no-op there.
--
-- org_meta.plan_tier intentionally carries no CHECK constraint (it mirrors the
-- billing-table key space, which is the constrained source), so no constraint
-- change is needed here.

-- +goose Up
-- +goose StatementBegin
UPDATE app.org_meta SET plan_tier = 'pro' WHERE plan_tier = 'business';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE app.org_meta SET plan_tier = 'business' WHERE plan_tier = 'pro';
-- +goose StatementEnd
