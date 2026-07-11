-- SPDX-License-Identifier: LicenseRef-Dropway-Proprietary
--
-- =============================================================================
-- CLOUD-ONLY / PROPRIETARY MIGRATION -- NOT part of the FSL open-core build.
-- =============================================================================
--
-- 0003_ai_metered_period.sql
--
-- Records the subscription's CURRENT billing-period START so the AI builder's
-- spend window (cap + "usage this month") lines up with the exact day the org's
-- Stripe subscription renews. Without it we defaulted the window to the calendar
-- month, so the number a user saw could drift a few days from the invoice period.
--
-- The webhook already stores current_period_end; this adds the matching start.
-- Nullable (a free org with no subscription has neither); the resolver falls
-- back to the calendar month when it is null.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE billing.subscriptions ADD COLUMN current_period_start timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE billing.subscriptions DROP COLUMN IF EXISTS current_period_start;
-- +goose StatementEnd
