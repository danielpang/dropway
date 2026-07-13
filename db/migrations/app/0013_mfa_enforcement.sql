-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0013_mfa_enforcement.sql
--
-- Org policy: require_mfa. When true, every member of the org must have
-- two-factor authentication enrolled before the dashboard serves them anything
-- but the mandatory setup flow (enforced on each authenticated dashboard
-- request, next-request semantics — no live session revocation on toggle).
--
-- Writable only by owners/admins on business/enterprise plans; both checks live
-- in the Go API (role re-checked against identity.member, tier via the quota
-- provider), not the schema — the same split as every other org policy toggle
-- (mcp_enabled, ai_enabled, allow_external_sharing).
--
-- The MFA credential storage itself (identity."twoFactor",
-- identity."user"."twoFactorEnabled") is owned and migrated by Better Auth, not
-- by these files (see README.md).
--
-- No RLS or GRANT changes: org_meta already carries the tenant policy and table
-- grants, and a new column inherits them.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.org_meta ADD COLUMN require_mfa boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS require_mfa;
-- +goose StatementEnd
