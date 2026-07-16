-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0014_collab_toggles.sql
--
-- Per-resource collaboration toggle: "allow non-creators to modify". Dropway
-- is collaborative BY DEFAULT — any org member may edit any site, skill, or
-- chat log (content edits: deploys/publish/previews, skill uploads/metadata/
-- folders, chat appends/message ops). The creator (or an org admin) can flip
-- a resource's toggle OFF to restrict content edits to creator-or-admin.
--
-- The toggle governs CONTENT modification only. Security-sensitive changes
-- (access mode, allowlist, revocation, custom domains) stay admin-gated
-- regardless, and destructive deletes stay creator-or-admin regardless.
-- Enforced in Go at the handler gates (like every role check); RLS remains
-- the org-isolation backstop.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.sites ADD COLUMN allow_member_edits boolean NOT NULL DEFAULT true;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.skills ADD COLUMN allow_member_edits boolean NOT NULL DEFAULT true;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.chat_logs ADD COLUMN allow_member_edits boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.chat_logs DROP COLUMN IF EXISTS allow_member_edits;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.skills DROP COLUMN IF EXISTS allow_member_edits;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.sites DROP COLUMN IF EXISTS allow_member_edits;
-- +goose StatementEnd
