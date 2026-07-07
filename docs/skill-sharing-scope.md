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

Skills can be authored/uploaded and pulled from three surfaces, appear in the
org **feed** as first-class posts alongside sites, and carry a monotonic
**version** clients use to detect when their downloaded copy is out of date.

## Data model

### Skills (migration `db/migrations/app/0008_skills.sql`)

Four tenant tables, each with the standard RLS boilerplate (org-scoped
`_tenant_isolation` policy, FORCE RLS, grants to `dropway_app`):

- **`app.skills`** — one shareable skill per `(org, slug)`: owner, title,
  description (both fall back to `SKILL.md` frontmatter), the live
  `current_version_id`, and (migration 0009) a `feed_visible` flag.
- **`app.skill_versions`** — immutable, content-addressed uploads mirroring
  `site_versions`, each with a monotonic `version_no`. v1 is **latest-only**:
  finalizing an upload flips the live pointer in the same transaction, so
  finalize *is* publish and there is no separate rollback surface. The current
  version's `version_no` is surfaced as the skill's **`version`** (see
  *Versioning & updates*).
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

### Feed posts (migration `db/migrations/app/0009_feed_skill_posts.sql`)

Skills join the org feed, so votes and comments — previously per-site — become
**polymorphic** over a `(subject_type, subject_id)` pair where `subject_type`
is `'site'` or `'skill'`:

- **`app.post_votes`** / **`app.post_comments`** replace the old
  `app.site_votes` / `app.site_comments` (existing rows migrate in as
  `subject_type = 'site'`). Same org-scoped RLS as every tenant table.
- Because a polymorphic row can't foreign-key to two parents, a subject's
  delete path cleans its votes + comments explicitly (only `DeleteSkill` does
  today — sites are never deleted).
- `app.skills.feed_visible` (default `true`, partial index `skills_feed_idx`)
  is the discovery flag, mirroring `sites.feed_visible`: a skill auto-joins the
  feed on publish and the owner/admin can make it private to pull it off.

## Presets & lazy seeding

Three starter skills are embedded in the API binary from
`internal/skillseeds/seeds/<slug>/` (`preset.json` + the skill files):
`pr-review-checklist` → engineering, `design-doc-template` → product,
`launch-announcement` → marketing. On an org's **first skills touch** (any
authenticated `/v1/skills*`, `/v1/skill-folders*`, or `/v1/feed` request), the
API materializes the default folders and seeds each starter as an ordinary org
skill — sentinel owner `00000000-…-0000`, rendered "Dropway" — flagged
`is_preset` in its folder. `org_meta.skills_seeded` (set in the same tx under
an advisory lock) guarantees this happens exactly once, so admins can rename
or delete the folders, remove or replace the defaults, and never have Dropway
re-seed over their curation. Seed-content updates reach new orgs only.

## Versioning & updates

Each finalized upload is an immutable `skill_version` with a monotonic
`version_no`; the skill's **`version`** is its current version's number
(0 before the first upload). It bumps on every genuine content change and
stays put on an idempotent re-upload of identical content (finalize is
content-hash idempotent), so it is a reliable "has this moved?" signal. The
`version` rides on the skill list/get responses and on the download payload, so
a client can **record what it pulled** and later compare against the org's
current version:

- **CLI** — `dropway skills pull` writes a `.dropway.json` sidecar
  (`{slug, skill_id, version}`) into each pulled skill folder; `dropway skills
  check [--update]` reads those, compares against one `list` call, reports the
  outdated skills, and (with `--update`) re-pulls them.
- **MCP** — `download_skill` / `list_skills` return `version`, and
  `check_skill_updates` takes the agent's held `{name, version}` set and
  reports, per skill, `installed_version` / `latest_version` / `outdated`; the
  agent updates an outdated one by calling `download_skill`.

Versions remain **latest-only** — there is no per-version restore surface; the
version number exists for update detection, not rollback.

## Surfaces

- **API** — `/v1/skills` (create, list/search via `q`/`folder`/`presets`,
  get, delete, per-skill download) and `/v1/skill-folders` (folder CRUD,
  membership + preset flags, bulk download). Uploads reuse the deploy
  contract byte-for-byte: prepare validates the manifest and returns presigned
  PUT URLs for missing blobs; clients PUT bytes directly to storage; finalize
  server-verifies every blob, recomputes the digest, parses frontmatter, and
  publishes. Bulk folder download inlines every finalized skill under a
  50 MiB budget, returning truncated stubs (fetched per-skill) past it. The
  **feed** endpoints for skills mirror the site ones: `GET /v1/feed` returns a
  unified `{posts:[…]}` list tagged by `kind`, and `PUT /v1/skills/{id}/feed`
  (visibility), `/feed-meta`, `/vote`, plus `GET`/`POST /v1/skills/{id}/comments`
  drive sharing, votes, and the comment thread. All mutations are audit-logged
  (`skill.*`, `skill_folder.*` actions).
- **MCP** (`services/mcp/internal/tools`) — read tools `list_skills`,
  `download_skill`, `download_skill_folder`, `check_skill_updates` serve
  directly from Postgres + blobs under RLS (10 MiB inline cap); the write tool
  `upload_skill` forwards the caller's OAuth token to the API like
  `deploy_site`. Agents install downloads into `.claude/skills/<name>/` and can
  record each one's `version` (e.g. in `.dropway.json`) to feed
  `check_skill_updates` later.
- **CLI** (`cli/internal/cmd/skills.go`) — `dropway skills push <dir>
  [--name] [--folder a,b]`, `skills list [--search|--folder|--presets]`,
  `skills pull <name>|--folder <slug>` (default dest `.claude/skills/`,
  path-traversal-safe writes, bulk pull auto-refetches truncated entries, and a
  `.dropway.json` version record per skill), and `skills check [--update]`.
- **Dashboard** — a Skills page with search/folder/preset filters, plus two
  ways to add a skill: drag-and-drop **upload** reusing the browser deploy
  machinery (`lib/skill-upload.ts` over `lib/deploy-manifest.ts`), and a
  **write-a-skill** editor (`/skills/new`) with a Markdown editor + live
  preview (a dependency-free, escape-first XSS-safe renderer in
  `lib/markdown.ts`, since the strict CSP forbids external hosts). A skill's
  owner or an org admin can **edit** it (`/skills/[id]/edit`): the current
  `SKILL.md` + text files load into the same editor, binary files are carried
  through unchanged, and Save uploads a new version. The detail page previews
  every file, offers zip download (dependency-free STORE zip writer in
  `lib/zip.ts`), and — below the content — a per-skill feed-visibility toggle.
  The feed page renders skills and sites distinctly (kind badge, "Open skill"
  link) with kind-aware vote/comment actions. An admin dialog handles folder +
  preset curation.

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

Per-version restore / rollback UI (versions are stored immutably and the
current number is surfaced for update detection, so restore is a pure surface
addition later), **access-controlled** private skills (`feed_visible` is a feed
*discovery* flag, not an access boundary — a skill off the feed is still
listable and installable by the org), an org-level skills kill switch, and CLI
folder administration (dashboard-only for now).
