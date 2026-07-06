<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Org-wide skill sharing — design overview

How Dropway lets org members share Claude skills with their team and install
teammates' skills from the dashboard, the MCP server, or the CLI. This is the
"why + shape" reference for the shipped implementation; the earlier scoping
draft it replaces proposed uploader-set categories and a global preset table,
both superseded by the admin-curated folder model described here.

A **skill** is a directory with a `SKILL.md` at its root (YAML frontmatter
`name`/`description` plus instructions) and optional supporting files. Dropway
stores and distributes skill files; it never executes them. The shared rules
live in `internal/skillspec`: a root `SKILL.md` is required, ≤ 200 files,
≤ 5 MiB total, clean relative paths only — enforced at upload prepare (before
any bytes move) and re-asserted at finalize against server-verified sizes.

## Data model (migration `db/migrations/app/0008_skills.sql`)

Four tenant tables, each with the standard RLS boilerplate (org-scoped
`_tenant_isolation` policy, FORCE RLS, grants to `dropway_app`):

- **`app.skills`** — one shareable skill per `(org, slug)`: owner, title,
  description (both fall back to `SKILL.md` frontmatter), and the live
  `current_version_id`.
- **`app.skill_versions`** — immutable, content-addressed uploads mirroring
  `site_versions`. v1 is **latest-only**: finalizing an upload flips the live
  pointer in the same transaction, so finalize *is* publish and there is no
  separate rollback surface.
- **`app.skill_folders`** — the org's admin-curated taxonomy (defaults:
  engineering, product, marketing). Slugs are stable filter keys; titles are
  renameable.
- **`app.skill_folder_items`** — folder membership with an **`is_preset`**
  flag marking the org-endorsed starter set. Uploaders pick folders for their
  own skills; only admins/owners curate folders, other people's skills, and
  preset flags.

Skill content reuses the per-org content-addressed blob store deploys use
(`blobs/<org>/<sha256>` — dedup and the storage meter apply unchanged);
manifests live at `skill-manifests/<org>/<skill>/<version>.json`
(`storage.SkillManifestKey`). The R2 GC unions every skill's *current*
manifest into its referenced-blob set, so live skill content is never
collected while superseded skill versions' blobs age out as orphans.

## Presets & lazy seeding

Three starter skills are embedded in the API binary from
`internal/skillseeds/seeds/<slug>/` (`preset.json` + the skill files):
`pr-review-checklist` → engineering, `design-doc-template` → product,
`launch-announcement` → marketing. On an org's **first skills touch** (any
authenticated `/v1/skills*` or `/v1/skill-folders*` request), the API
materializes the default folders and seeds each starter as an ordinary org
skill — sentinel owner `00000000-…-0000`, rendered "Dropway" — flagged
`is_preset` in its folder. `org_meta.skills_seeded` (set in the same tx under
an advisory lock) guarantees this happens exactly once, so admins can rename
or delete the folders, remove or replace the defaults, and never have Dropway
re-seed over their curation. Seed-content updates reach new orgs only.

## Surfaces

- **API** — `/v1/skills` (create, list/search via `q`/`folder`/`presets`,
  get, delete, per-skill download) and `/v1/skill-folders` (folder CRUD,
  membership + preset flags, bulk download). Uploads reuse the deploy
  contract byte-for-byte: prepare validates the manifest and returns presigned
  PUT URLs for missing blobs; clients PUT bytes directly to storage; finalize
  server-verifies every blob, recomputes the digest, parses frontmatter, and
  publishes. Bulk folder download inlines every finalized skill under a
  50 MiB budget, returning truncated stubs (fetched per-skill) past it. All
  mutations are audit-logged (`skill.*`, `skill_folder.*` actions).
- **MCP** (`services/mcp/internal/tools`) — read tools `list_skills`,
  `download_skill`, `download_skill_folder` serve directly from Postgres +
  blobs under RLS (10 MiB inline cap); the write tool `upload_skill` forwards
  the caller's OAuth token to the API like `deploy_site`. Agents install
  downloads into `.claude/skills/<name>/`.
- **CLI** (`cli/internal/cmd/skills.go`) — `dropway skills push <dir>
  [--name] [--folder a,b]`, `skills list [--search|--folder|--presets]`, and
  `skills pull <name>|--folder <slug>` (default dest `.claude/skills/`,
  path-traversal-safe writes, bulk pull auto-refetches truncated entries).
- **Dashboard** — a Skills page with search/folder/preset filters,
  drag-and-drop upload reusing the browser deploy machinery
  (`lib/skill-upload.ts` over `lib/deploy-manifest.ts`), zip downloads
  (dependency-free STORE zip writer in `lib/zip.ts`), and an admin dialog for
  folder + preset curation.

## Quota & plans

Folder size is the plan lever: `quota.ResourceSkillPerFolder` caps the free
tier at **10 skills per folder** in the cloud provider (`cloud/quota`), with
pro/business/enterprise uncapped and OSS self-host always unlimited. The check
runs inside the store's advisory-lock COUNT → policy → INSERT critical section
(the same race-safe pattern as the site cap) and surfaces as the standard 402
quota body the dashboard's upgrade modal understands. Skills per org are
unlimited on every tier (`ResourceSkillPerOrg` is a dormant seam), and skill
bytes flow through the existing dedup-aware org storage meter.

## Deliberate v1 non-goals

Version history / rollback UI (versions are stored immutably, so this is a
pure surface addition later), private or draft skills (uploading = sharing),
an org-level skills kill switch, and CLI folder administration (dashboard-only
for now).
