-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0005_host_routes.sql
--
-- GLOBAL host registry closing the cross-tenant public-host hijack
-- (ARCHITECTURE.md §6 edge projection; the projection.HostForSlug scheme).
--
-- The problem: the edge projection key `route:<host>` is a GLOBAL KV namespace,
-- but site slugs are UNIQUE only per (org_id, slug). Without a global guard, org
-- B can create + publish slug 'acme' and overwrite org A's
-- `route:acme.shippedusercontent.com`, serving org B's content at org A's URL.
--
-- The fix: a GLOBAL registry keyed by `host`. The PRIMARY KEY on `host` enforces
-- global uniqueness REGARDLESS of RLS visibility — a conflicting insert from
-- another org raises unique_violation (SQLSTATE 23505), which the Go API surfaces
-- as store.ErrHostTaken (HTTP 409). Host availability is not secret, so the cross-
-- tenant constraint error leaking "this host exists" is acceptable and intended.
--
-- RLS still applies for SELECT/UPDATE/DELETE so an org can only see / mutate the
-- host rows it OWNS (the publish-time ownership assertion reads its own row). The
-- policy is the same subquery-free org_id equality used by every other app table
-- (0003): plain equality of the denormalized org_id against the per-tx GUC, so it
-- stays index-free-of-joins on the hot path and DEFAULT-DENYs without tenant
-- context. Host reservation happens INSIDE the CreateSite tx, so a conflict rolls
-- the whole tx back and the site is never created.

-- +goose Up

-- +goose StatementBegin
-- host_routes: the global host -> owning (org, site) registry. `host` is the
-- canonical content host (projection.HostForSlug(slug)). PRIMARY KEY (host) is
-- the GLOBAL uniqueness guard; org_id is denormalized for the subquery-free RLS
-- policy and indexed leading on org_id for the per-tenant ownership lookup.
CREATE TABLE app.host_routes (
    host       text PRIMARY KEY,
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    site_id    uuid NOT NULL REFERENCES app.sites (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX host_routes_org_id_site_id_idx ON app.host_routes (org_id, site_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.host_routes ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.host_routes FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON app.host_routes TO shipped_app;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY host_routes_tenant_isolation ON app.host_routes
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP POLICY IF EXISTS host_routes_tenant_isolation ON app.host_routes;
-- +goose StatementEnd
-- +goose StatementBegin
REVOKE SELECT, INSERT, UPDATE, DELETE ON app.host_routes FROM shipped_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.host_routes NO FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.host_routes DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.host_routes;
-- +goose StatementEnd
