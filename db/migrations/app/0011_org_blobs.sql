-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0011_org_blobs.sql
--
-- Per-org storage metering (docs/pricing.md §5). Cloudflare R2 reports storage per
-- BUCKET, not per prefix/tenant, and we use one bucket with per-org prefixes
-- (blobs/<org_id>/<sha256>), so per-org storage is computed by us.
--
-- app.org_blobs is the per-org SET of stored, content-addressed blobs — one row per
-- distinct (org_id, content_hash). Because blob dedup is scoped per org (the org_id
-- is in the R2 key), this table mirrors exactly what lives under blobs/<org_id>/ and
-- its SUM(size_bytes) is the org's true (dedup-aware) storage. The deploy path
-- INSERTs ON CONFLICT DO NOTHING (counts a blob once); GC DELETEs the row when it
-- removes the orphaned R2 object. app.org_usage.storage_bytes is the denormalized
-- running total the quota cap reads (avoids a SUM on the deploy path); it is
-- reconcilable from org_blobs (RecomputeOrgStorage).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE app.org_blobs (
    org_id       uuid    NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    content_hash text    NOT NULL,                       -- sha256, the blobs/<org>/<sha> suffix
    size_bytes   bigint  NOT NULL CHECK (size_bytes >= 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, content_hash)
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.org_usage
    ADD COLUMN storage_bytes bigint NOT NULL DEFAULT 0 CHECK (storage_bytes >= 0);
-- +goose StatementEnd

-- RLS: org_blobs is a tenant table — same FORCE-RLS + GRANT + org-scoped policy as
-- the other app.* tables (0003), so even the table owner is constrained and a row is
-- only visible/writable under its own org's SET LOCAL app.current_org_id.
-- +goose StatementBegin
ALTER TABLE app.org_blobs ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_blobs FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.org_blobs TO shipped_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY org_blobs_tenant_isolation ON app.org_blobs
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.org_blobs;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.org_usage DROP COLUMN IF EXISTS storage_bytes;
-- +goose StatementEnd
