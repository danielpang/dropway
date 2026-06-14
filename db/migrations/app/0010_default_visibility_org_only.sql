-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0010_default_visibility_org_only.sql
--
-- A brand-new org should be INTERNAL by default: a fresh org has
-- allow_external_sharing=false, so its sites must default to org-visible
-- (org_only), NOT public (§2.2 "sites default to Tier (b) org-visible"; §5.4).
-- The previous default of 'public' meant a fresh org couldn't create ANY site —
-- the external-sharing trigger (0004) 403s a public site under a false policy.
-- New sites now inherit app.org_meta.default_visibility (org.CreateSite), so this
-- flips the column default to org_only.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.org_meta ALTER COLUMN default_visibility SET DEFAULT 'org_only';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.org_meta ALTER COLUMN default_visibility SET DEFAULT 'public';
-- +goose StatementEnd
