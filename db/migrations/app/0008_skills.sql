-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0008_skills.sql
--
-- Org-wide skill sharing. A skill is a directory with a SKILL.md at its root
-- (plus supporting files) that a member uploads to share with the org. Content
-- is content-addressed into the same per-org blob store deploys use; versions
-- are immutable like site_versions (v1 exposes only the current one — finalize
-- flips the pointer, there is no separate publish).
--
-- Skills are organized into admin-curated FOLDERS (defaults: engineering,
-- product, marketing — seeded lazily per org, see org_meta.skills_seeded).
-- Folder membership carries an is_preset flag: the admin-curated starter set a
-- member can bulk-download. Uploaders pick folders at upload; only org
-- admins/owners manage folders and preset flags (enforced in Go).
--
-- RLS scopes all four tables to their org like every other tenant table.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE app.skills (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    slug               text NOT NULL,
    -- 00000000-0000-0000-0000-000000000000 sentinel = seeded by Dropway.
    owner_user_id      uuid NOT NULL,
    title              text,
    description        text,
    current_version_id uuid,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT skills_org_slug_key UNIQUE (org_id, slug)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.skill_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    skill_id     uuid NOT NULL REFERENCES app.skills (id) ON DELETE CASCADE,
    version_no   int NOT NULL,
    status       text NOT NULL DEFAULT 'pending',
    content_hash text NOT NULL,
    size_bytes   bigint NOT NULL DEFAULT 0,
    created_by   uuid NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT skill_versions_skill_version_no_key UNIQUE (skill_id, version_no),
    CONSTRAINT skill_versions_skill_content_hash_key UNIQUE (skill_id, content_hash)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Deferrable FK closing the skills <-> skill_versions cycle (mirrors sites).
ALTER TABLE app.skills
    ADD CONSTRAINT skills_current_version_id_fkey
        FOREIGN KEY (current_version_id)
        REFERENCES app.skill_versions (id)
        DEFERRABLE INITIALLY DEFERRED;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.skill_folders (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    slug       text NOT NULL,
    title      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT skill_folders_org_slug_key UNIQUE (org_id, slug)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app.skill_folder_items (
    org_id    uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    folder_id uuid NOT NULL REFERENCES app.skill_folders (id) ON DELETE CASCADE,
    skill_id  uuid NOT NULL REFERENCES app.skills (id) ON DELETE CASCADE,
    is_preset boolean NOT NULL DEFAULT false,
    added_by  uuid NOT NULL,
    added_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (folder_id, skill_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Membership lookups by skill (folder chips on a skill row).
CREATE INDEX skill_folder_items_skill_idx ON app.skill_folder_items USING btree (skill_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Guards the lazy per-org seeding of default folders + preset skills: set true
-- in the same tx that seeds, so Dropway never re-seeds over an admin's curation.
ALTER TABLE app.org_meta ADD COLUMN skills_seeded boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.skills ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.skills FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY skills_tenant_isolation ON app.skills
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.skills TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.skill_versions ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.skill_versions FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY skill_versions_tenant_isolation ON app.skill_versions
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.skill_versions TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.skill_folders ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.skill_folders FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY skill_folders_tenant_isolation ON app.skill_folders
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.skill_folders TO dropway_app;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app.skill_folder_items ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.skill_folder_items FORCE ROW LEVEL SECURITY;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE POLICY skill_folder_items_tenant_isolation ON app.skill_folder_items
    USING ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid))
    WITH CHECK ((org_id = (NULLIF(current_setting('app.current_org_id'::text, true), ''::text))::uuid));
-- +goose StatementEnd
-- +goose StatementBegin
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE app.skill_folder_items TO dropway_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS skills_seeded;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.skill_folder_items;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.skill_folders;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app.skills DROP CONSTRAINT IF EXISTS skills_current_version_id_fkey;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.skill_versions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS app.skills;
-- +goose StatementEnd
