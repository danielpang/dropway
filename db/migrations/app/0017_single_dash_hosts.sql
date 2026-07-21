-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0017_single_dash_hosts.sql
--
-- Content-host separator change: `<org>--<app>.<domain>` → `<org>-<app>.<domain>`.
-- The serving path looks hosts up verbatim (app.resolve_host / the KV route
-- projection), so this migration rewrites every stored canonical + preview host;
-- the Go API's projection.HostForSite ships the same change in lockstep. Old
-- `--` hosts are NOT kept as routes — the serving path 301s any unresolved host
-- containing `--` to its single-dash rewrite, which resolves to the rows written
-- here. Custom-domain rows (kind='custom') are a tenant's OWN hostname and may
-- legitimately contain `--`; they are never touched.
--
-- Because slugs may contain single dashes, two (org, app) pairs can now render
-- the same host label ("dpang-studios"/"readme" vs "dpang"/"studios-readme").
-- The host_routes PRIMARY KEY already arbitrates new claims (ErrHostTaken →
-- 409); this migration pre-checks that the REWRITE itself creates no such
-- collision and aborts loudly if it would (resolve manually, then re-run).
--
-- Also introduces the 'vanity' host kind: an optional, org-claimed bare
-- `<slug>.<domain>` platform subdomain (single label — still covered by the
-- wildcard cert). One per site, globally first-come-first-served via the same
-- PRIMARY KEY.
--
-- app.host_routes_sep_backup exists so Down is honest: replace(host,'-','--')
-- is not a valid inverse, so Down restores the exact pre-migration hosts from
-- the mapping captured here. Runs as the migration role (owner of app.*), which
-- bypasses FORCE RLS the same way every prior host_routes migration has.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    collisions text;
BEGIN
    -- Abort if rewriting `--` → `-` would make any two rows (rewritten or not,
    -- any kind) claim the same host. GROUP over ALL rows post-rewrite: custom
    -- rows keep their host verbatim but still occupy the namespace.
    SELECT string_agg(new_host || ' (x' || cnt || ')', ', ')
    INTO collisions
    FROM (
        SELECT CASE
                   WHEN kind IN ('canonical', 'preview')
                       THEN replace(host, '--', '-')
                   ELSE host
               END AS new_host,
               count(*) AS cnt
        FROM app.host_routes
        GROUP BY 1
        HAVING count(*) > 1
    ) dupes;

    IF collisions IS NOT NULL THEN
        RAISE EXCEPTION 'single-dash host rewrite would collide; resolve these hosts first: %',
            collisions;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.host_routes_sep_backup (
    old_host text PRIMARY KEY,
    new_host text NOT NULL UNIQUE
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO app.host_routes_sep_backup (old_host, new_host)
SELECT host, replace(host, '--', '-')
FROM app.host_routes
WHERE kind IN ('canonical', 'preview') AND host LIKE '%--%';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE app.host_routes
SET host = replace(host, '--', '-')
WHERE kind IN ('canonical', 'preview') AND host LIKE '%--%';
-- +goose StatementEnd

-- The 'vanity' kind: a bare <slug>.<domain> claimed manually per site.
-- +goose StatementBegin
ALTER TABLE app.host_routes
    DROP CONSTRAINT host_routes_kind_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.host_routes
    ADD CONSTRAINT host_routes_kind_check
        CHECK (kind IN ('canonical', 'custom', 'preview', 'vanity'));
-- +goose StatementEnd

-- One vanity host per site (the global "is this label free?" race is already
-- arbitrated by the host_routes PRIMARY KEY).
-- +goose StatementBegin
CREATE UNIQUE INDEX host_routes_one_vanity_per_site
    ON app.host_routes (site_id)
    WHERE kind = 'vanity';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    -- The narrowed kind CHECK below (and the rewritten API this Down accompanies)
    -- predates vanity hosts; refuse to roll back past rows that depend on them.
    IF EXISTS (SELECT 1 FROM app.host_routes WHERE kind = 'vanity') THEN
        RAISE EXCEPTION 'vanity host_routes rows exist; release them before rolling back 0017';
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS app.host_routes_one_vanity_per_site;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.host_routes
    DROP CONSTRAINT host_routes_kind_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.host_routes
    ADD CONSTRAINT host_routes_kind_check
        CHECK (kind IN ('canonical', 'custom', 'preview'));
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE app.host_routes hr
SET host = b.old_host
FROM app.host_routes_sep_backup b
WHERE hr.host = b.new_host;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE app.host_routes_sep_backup;
-- +goose StatementEnd
