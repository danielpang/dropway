-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0015_drop_deploy_tokens.sql
--
-- Drop app.deploy_tokens. The table was scoped as the "CLI / CI deploy path"
-- credential, but only the client half of that feature was ever built (the
-- CLI's DROPWAY_TOKEN env var and the audit_log.actor_token provenance
-- column); no server code has ever minted, verified, read, or written a row,
-- so this drop is a no-op on every real database. Org-scoped API keys
-- (docs/typescript-sdk-api-keys.md) supersede the design with their own
-- table; audit_log.actor_token stays, and will record api_keys.id.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS app.deploy_tokens;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE app.deploy_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    scopes     text[] NOT NULL DEFAULT ARRAY['deploy']::text[],
    site_id    uuid REFERENCES app.sites (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX deploy_tokens_org_id_idx ON app.deploy_tokens USING btree (org_id);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.deploy_tokens ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ONLY app.deploy_tokens FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY deploy_tokens_tenant_isolation ON app.deploy_tokens
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.deploy_tokens TO dropway_app;
-- +goose StatementEnd
