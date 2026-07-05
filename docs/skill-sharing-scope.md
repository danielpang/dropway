<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Scoping: org-wide skill sharing

Scope of work for letting org members share Claude skills with their team through
the Dropway MCP server and CLI. This is a planning document, not a spec — it maps
the requirements onto the existing architecture, calls out the decisions we need
to make, and breaks the work into phases with rough estimates.

## Requirements (as stated)

1. A user creates a skill in their local environment (a directory with a
   `SKILL.md` plus supporting files).
2. Using the Dropway MCP server or CLI, they upload the skill to be shared with
   the org, setting one or more **categories** (engineering, product, marketing).
3. Another org member can **list or search** skills via MCP or CLI, filtering by
   skill name or category.
4. A user can **download** a skill found via search, or pick from **presets** —
   a seeded set of starter skills.

## What we already have to build on

Almost every piece of infrastructure this feature needs already exists for sites;
skills are a second resource that follows the same rails:

| Need | Existing piece |
|---|---|
| Org/team scoping + isolation | Better Auth orgs, `app.org_meta`, RLS tenant context on every store call (`services/api/internal/store/store.go`) |
| Auth from CLI + MCP | OAuth 2.1 / EdDSA JWT verification (`internal/auth`, `internal/middleware`); MCP write tools forward the bearer token to the API (`services/mcp/internal/apiclient`) |
| Upload | Content-addressed manifest → presigned PUT → server-verified finalize (`services/api/internal/handlers/deployments.go`); MCP inline-bytes variant (`deploy_site`) |
| Download | Manifest resolve + blob streaming with a 10 MiB inline cap (`services/mcp/internal/tools/tools.go` — `read_file`/`download_site`) |
| Blob storage + dedup + metering | `internal/storage` (S3/R2 + memory fake), `app.org_blobs`, `org_usage.storage_bytes` |
| Audit + quota | `internal/audit`, `internal/quota` |

Two things do **not** exist yet and are net-new patterns:

- **Categories/tags and search.** No table has a taxonomy today, and listing is
  plain `ORDER BY` with no filtering. Skills introduce the first search surface.
- **Seeding/presets.** Provisioning creates empty org rows only; migrations are
  pure DDL. Preset skills need a seeding mechanism that doesn't exist.

## Proposed design

### Data model

New tables in `db/migrations/app/0008_skills.sql` (mirrored into
`db/sqlc/schema.sql`, queries in `db/sqlc/query.sql`, then `sqlc generate`),
modeled directly on `app.sites` / `app.site_versions`:

```sql
-- app.skills: a shareable skill owned by a user inside an org.
CREATE TABLE app.skills (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES app.org_meta (id) ON DELETE CASCADE,
    slug               text NOT NULL,              -- skill name, e.g. "pr-review-checklist"
    owner_user_id      uuid NOT NULL,
    title              text,
    description        text,                       -- extracted from SKILL.md frontmatter on upload
    categories         text[] NOT NULL DEFAULT '{}',
    current_version_id uuid,                       -- deferrable FK, same cycle-break as sites
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT skills_org_slug_key UNIQUE (org_id, slug)
);

-- app.skill_versions: immutable, content-addressed uploads (same shape as site_versions).
CREATE TABLE app.skill_versions ( ... version_no, status, content_hash, size_bytes, created_by ... );
```

Both tables get the standard RLS enable/force + `_tenant_isolation` policy +
grants to `dropway_app`.

**Categories** are a `text[]` validated server-side against a curated list
(`engineering`, `product`, `marketing` to start) kept in Go, not in a table.
That matches the stated requirement (a small fixed set), keeps filtering to a
GIN-indexable `categories @> ARRAY[$1]`, and lets us grow the list without a
migration. If we later want org-defined categories, promote to a table then.

**Skill content** reuses the per-org content-addressed blob layout
(`blobs/<org_id>/<sha256>`) and a manifest JSON at
`skill-manifests/<org_id>/<skill_id>/<version_id>.json`, so dedup and storage
metering (`app.org_blobs`, `org_usage`) work unchanged.

**Validation at upload:** require a `SKILL.md` at the root; parse its YAML
frontmatter for `name`/`description` (fall back to slug); cap total size
(proposed: 5 MiB per skill, well under the MCP 10 MiB inline cap) and file
count. Reject path traversal the same way deploy manifests do.

### API surface (`/v1/skills`)

New chi group in `services/api/internal/router/router.go` behind the existing
`middleware.Auth` + `EnsureOrgProvisioned`, with `handlers/skills.go` +
`store/skills.go` following the sites pattern:

| Route | Purpose |
|---|---|
| `POST /v1/skills` | Create skill (slug, title, categories) |
| `GET /v1/skills?q=&category=&preset=` | List/search — `q` matches slug/title/description (`ILIKE`), `category` filters the array |
| `GET /v1/skills/{id}` | Metadata + current version |
| `POST /v1/skills/{id}/uploads/prepare` | Manifest in → presigned PUTs for missing blobs (CLI path) |
| `POST /v1/skills/{id}/uploads` | Finalize: verify hashes, write manifest, insert version, flip current |
| `GET /v1/skills/{id}/files` / `GET /v1/skills/{id}/download` | Resolve manifest, stream content (download) |
| `PUT /v1/skills/{id}/categories` | Re-tag (owner or org admin) |
| `DELETE /v1/skills/{id}` | Remove (owner or org admin) |

Search is SQL-level: `WHERE org_id = current AND (slug ILIKE ... OR title ILIKE
... OR description ILIKE ...) AND categories @> ...`. No full-text engine —
org skill counts will be tens-to-hundreds, so `ILIKE` + GIN on `categories` is
plenty. `pg_trgm` is an easy later upgrade if needed.

Updates to `services/api/openapi/openapi.yaml` (contract-first) and the
generated dashboard client come with this phase even if no dashboard UI ships
initially.

### MCP tools (`services/mcp/internal/tools/tools.go`)

Following the existing read-direct / write-through split:

- `upload_skill` (write) — inline files (text/base64) forwarded to the API with
  the caller's token, like `deploy_site`; params: name, categories, files.
- `list_skills` (read) — direct RLS store read; params: `query`, `category`,
  `include_presets`.
- `download_skill` (read) — resolve manifest, return files inline (the agent
  writes them to `.claude/skills/<name>/` locally), same 10 MiB cap as
  `download_site`.

### CLI (`cli/internal/cmd/`, wired in `root.go`)

- `dropway skills push <dir> --category engineering[,product]` — walk dir,
  build manifest, prepare/upload/finalize (reuses the deploy upload core, which
  should be extracted into a shared helper rather than duplicated).
- `dropway skills list [--category X] [--search q] [--presets]`
- `dropway skills pull <name> [--dest .claude/skills/]`

### Presets (net-new pattern)

Recommended approach: a **global, RLS-exempt read-only table** rather than
copy-on-provision.

- `app.preset_skills` (+ blob content under a reserved `presets/` storage
  prefix): no `org_id`, `SELECT` granted to `dropway_app`, no RLS — every org
  reads the same rows. List endpoints union these in when `preset=true` /
  `--presets` is passed (or always, flagged `"preset": true`).
- Seed source lives in-repo under `examples/skills/<name>/` (SKILL.md +
  files), loaded by an **idempotent startup seeder** in the API (upsert by
  slug + content hash). This keeps presets versionable in git, updatable by
  redeploy, and avoids the alternative's downsides (copy-on-provision snapshots
  go stale; a magic "system org" fights RLS everywhere).
- Downloading a preset is read-only; "fork to org" = download + `skills push`.
- Proposed seed set (3–5): a PR-review skill, a design-doc template skill, a
  launch-comms/marketing skill — one per category so filtering demos well.

## Decisions to confirm

1. **Versioning surface:** keep immutable versions internally (cheap, matches
   site_versions) but expose only "latest" in v1 — rollback/history UI is out
   of scope. OK?
2. **Visibility:** v1 = every skill uploaded is org-visible (that's the point of
   the feature). No private/draft skills, no `feed_visible`-style toggle yet.
3. **Curated categories in Go vs. table:** proposal above says curated list; an
   org-defined taxonomy is a follow-up.
4. **Dashboard UI:** out of scope for v1 (MCP + CLI only), but the OpenAPI
   contract will support it. A read-only "Skills" tab is a natural fast-follow.
5. **Org policy toggle:** should `app.org_meta` grow an `skills_enabled` flag
   like `mcp_enabled`? Cheap to add now, suggest yes.

## Phased breakdown & estimate

Assumes one engineer, includes tests (store fakes, handler tests, an RLS
integration test for the new tables, MCP unit tests) since that's how the rest
of the codebase is built.

| Phase | Work | Est. |
|---|---|---|
| 1. Data model | Migration 0008, schema.sql mirror, sqlc queries + regen, store layer + fakestore, RLS integration test | 1.5–2 d |
| 2. API | Handlers (create/list/search/prepare/finalize/download/categories/delete), OpenAPI, quota + audit wiring, handler tests | 2.5–3 d |
| 3. MCP tools | `upload_skill`, `list_skills`, `download_skill` + registration + tests | 1–1.5 d |
| 4. CLI | `skills push/list/pull`, extract shared upload helper from deploy, docs page | 1.5–2 d |
| 5. Presets | `preset_skills` table + seeder, 3–5 seed skills in `examples/skills/`, union into list/download paths | 1.5–2 d |
| 6. Polish | Dashboard client regen, `docs/components.md` + CLI reference updates, e2e smoke in CI | 1 d |

**Total: ~9–11.5 engineer-days (~2–2.5 weeks)** with review cycles. Phases 3–5
are parallelizable once phase 2's contract lands, so two people could compress
this to ~1.5 weeks.

### Sequencing note

Phases 1+2 ship no user-visible surface and are safe to merge behind nothing.
Phase 3 (MCP) is the highest-value slice for the stated workflow — agents can
upload/discover/install skills without leaving a session — so if we want to cut
scope for a first release: **phases 1–3 + a minimal `skills pull`** covers
requirements 1–3 and most of 4, deferring presets.
