-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0018_drop_ai_builder.sql
--
-- Removes the AI website builder: chat sessions, the transcript, the cost
-- ledger, and the org kill switch / monthly spend cap. Version previews and
-- site_versions.created_via stay — previews serve ordinary deploys, and
-- historical rows may still carry created_via='ai'. Org memory's source_kind
-- check still allows 'ai_session' so existing memory rows remain valid.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS app.ai_usage;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS app.ai_messages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS app.ai_sessions;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS ai_monthly_cap_usd;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS ai_enabled;
-- +goose StatementEnd

-- +goose Down
-- The builder is removed. Re-creating its tables would reintroduce a feature
-- this migration exists to delete; down is intentionally a no-op.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
