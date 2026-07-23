# Org Memory — "Your agent knows your company"

Engineering requirements for goal 2 of the agent-database initiative: build a
per-org memory of sites, conversations, and chats that is retrieved into the AI
builder so output becomes company-specific over time, and that the org can see
and edit.

Status: proposed · Owner: TBD · Related: agent-database evaluation (Mem0 / Letta
/ Qdrant, July 2026)

---

## 1. Summary

Today every builder session starts cold: session 47 knows nothing session 1
learned. The org's activity already produces the raw material — full transcripts
in `app.ai_messages`, shared chats in `app.chat_messages`, published site
content in R2 (`app.site_versions`) — but none of it feeds forward.

This feature adds an **org-scoped memory store** with four moving parts:

1. **Extraction** — after each builder turn, an async LLM pass distills durable
   facts (brand voice, palette, product names, structural preferences,
   recurring corrections) into memory rows.
2. **Retrieval** — before each generation, the top-k relevant memories (plus all
   pinned ones) are injected into the system context of the agent loop.
3. **Curation** — a dashboard Memory page where org admins view, pin, edit,
   disable, and delete what Dropway has learned.
4. **External agent access** — the same memory is exposed through the Dropway
   MCP server and CLI, so outside coding agents (Cursor, Claude Code, Codex,
   …) building for the org can read — and contribute to — company memory, not
   just the built-in AI builder. Dropway becomes the org's memory provider
   across tools.

### Goals

- New site builds start pre-loaded with company context; users stop restating
  brand facts.
- Cross-site awareness: "make the pricing section like our launch site" works.
- Memory is visible, editable, and deletable per org (trust + compliance).
- Memory is strictly org-isolated with the same guarantees as every other
  tenant table.
- Memory follows the user into external coding agents: MCP tools
  (`search_memory`, `add_memory`, …) and `dropway memory` CLI commands give
  Cursor / Claude Code / Codex the same org context the builder gets.

### Non-goals (this phase)

- Org analytics / trends dashboards / knowledge graph (goal 1 — separate scope).
- Cross-org learning of any kind.
- Adopting an external memory framework (Mem0/Letta). We build a thin extraction
  + retrieval layer in the Go API; see §2.

## 2. Architecture decision: pgvector in the existing Postgres, behind a store interface

**v1 stores embeddings in the existing Postgres (Supabase in prod) using the
`pgvector` extension**, not a separate vector database.

Rationale (from the July 2026 evaluation):

- It is the only option with **true RLS-enforced org isolation** — memory rows
  are ordinary tenant rows behind the existing `app.current_org_id` discipline
  (`internal/middleware/rlstx.go`), covered by the same `orgscope`/RLS tests.
  Qdrant/Mem0/Letta all reduce to app-enforced filtering.
- Zero new infrastructure: no new Fly app, volume, backup story, or auth
  surface. Supabase supports `pgvector` natively; self-host compose uses the
  `pgvector/pgvector` Postgres image (see §11).
- Expected scale (thousands of memories per org, not millions) is far below
  where Postgres ANN performance becomes the bottleneck.

All vector access goes through store methods on `services/api/internal/store`
(the existing pattern), and the `ai` package depends on a narrow `MemoryStore`
interface — so a Qdrant-backed implementation can be swapped in later without
touching the agent loop. Qdrant remains the designated scale-out path
(single-collection, `org_id` payload partitioning, `is_tenant` index).

Extraction and generation continue to use the existing OpenRouter seam
(`internal/openrouter`). Embeddings need a **separate provider** — OpenRouter
does not offer an embeddings endpoint and `internal/openrouter` is
chat-completions only (see §5 and Open Decisions).

### Data flow

```
builder turn (ai.Runner.RunTurn)
  ├─ [retrieval, sync, pre-generation]
  │    embed(userText) → store.SearchOrgMemories(t, vec, k) + pinned
  │    → injected as a <company_memory> block after the system prompt
  ├─ ... existing tool loop, unchanged ...
  └─ [extraction, async, post-turn]
       turn transcript → LLM extraction prompt → candidate memories
       → embed → dedupe (content hash + similarity) → upsert org_memories
       → cost recorded to ai_usage (same ledger/meter as generations)

external agents (Cursor / Claude Code / Codex / CLI)
  ├─ MCP tools (services/mcp) ──┐   search_memory / list_memories / add_memory
  └─ dropway memory … (cli/) ───┴─→ Go API /v1/ai/memories* endpoints
                                     (API owns the embedder; MCP/CLI never
                                      embed — one vendor seam, one quota path)
```

The Go API is the **only** component that talks to the embeddings provider and
the only writer of memory rows. The MCP server and CLI are thin clients of the
API's memory endpoints (§7.1), exactly like the MCP write tools forward to the
API today (`services/mcp/internal/apiclient`).

## 3. Database changes — migration `db/migrations/app/0017_org_memory.sql`

Follows the conventions of `0010_ai_builder.sql` (goose, `-- +goose
StatementBegin` blocks, RLS on every new table, `db/sqlc/schema.sql` updated in
the same change).

### 3.1 Extension

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

Prod note: on Supabase the extension must be enabled for the database
(dashboard → Extensions) before this migration runs; local/self-host needs the
`pgvector/pgvector` Postgres image (§11).

### 3.2 New table `app.org_memories`

| column | type | notes |
|---|---|---|
| `id` | `uuid PK DEFAULT gen_random_uuid()` | |
| `org_id` | `uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE` | org delete wipes memory (compliance for free) |
| `kind` | `text NOT NULL CHECK (kind IN ('fact','preference','style','correction','manual'))` | `manual` = user-authored via API/dashboard |
| `content` | `text NOT NULL` | the memory sentence(s); bounded (~2 KB) at the store layer |
| `content_hash` | `text NOT NULL` | sha256 of normalized content; `UNIQUE (org_id, content_hash)` for cheap exact dedupe |
| `embedding` | `vector(1536)` | dimension pinned by the chosen embedding model (§5); nullable so a failed embed can be repaired by sweep |
| `embedding_model` | `text NOT NULL` | which model produced `embedding` (e.g. `text-embedding-3-small`); makes a future model switch a lazy re-embed instead of a big-bang migration (§3.5) |
| `source_kind` | `text NOT NULL CHECK (source_kind IN ('ai_session','chat_log','site_version','manual'))` | provenance for the UI; MCP/CLI-authored rows are `manual` |
| `source_id` | `uuid` | no FK — sources may be GC'd while the memory persists |
| `source_tool` | `text` | attribution for externally added memories (`'cursor'`, `'claude-code'`, `'cli'`, …), mirroring `chat_logs.source_tool`; NULL for extracted/dashboard rows |
| `pinned` | `boolean NOT NULL DEFAULT false` | pinned memories are always injected |
| `disabled` | `boolean NOT NULL DEFAULT false` | user-suppressed; never retrieved, kept so extraction dedupe doesn't resurrect it |
| `created_by` | `uuid` | NULL for extracted, user id for manual |
| `created_at` / `updated_at` | `timestamptz NOT NULL DEFAULT now()` | |
| `last_used_at` | `timestamptz` | stamped on retrieval (best-effort, batched) for the UI and future decay |

Indexes:

- `org_memories_org_idx` btree `(org_id, pinned DESC, updated_at DESC)` — list/UI.
- `org_memories_embedding_idx` **HNSW** `(embedding vector_cosine_ops)` — ANN.
  Queries always filter `org_id = current org` first; with RLS applied the
  planner combines the tenant filter with the ANN scan. Acceptable at v1 scale;
  revisit (partitioning or Qdrant) if per-org recall degrades.

RLS: same policy shape as `ai_sessions` — all four verbs scoped to
`current_setting('app.current_org_id')`, forced for `dropway_app`.

### 3.3 New table `app.org_memory_ingests`

Watermark + idempotency for the async extraction pipeline (one row per
processed source):

| column | type | notes |
|---|---|---|
| `org_id` | `uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE` | |
| `source_kind` | `text NOT NULL` | `'ai_session' \| 'chat_log' \| 'site_version'` |
| `source_id` | `uuid NOT NULL` | |
| `through_seq` | `bigint NOT NULL DEFAULT 0` | for sessions: highest `ai_messages.seq` extracted, so a session is re-extracted incrementally |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` | |

`PRIMARY KEY (org_id, source_kind, source_id)`. RLS as above. This makes
extraction safely re-runnable (crash mid-extract → next sweep resumes) and
powers the backfill job (§11).

### 3.4 `app.org_meta` extension

```sql
ALTER TABLE app.org_meta ADD COLUMN memory_enabled boolean NOT NULL DEFAULT false;
```

Mirrors `ai_enabled` / `mcp_enabled` kill-switch pattern, but **defaults false**
(opt-in rollout; flip the default in a later migration once stable). Surfaced
through the same org-settings read path as `GetAISettings`
(`services/api/internal/store/ai.go`, `orgpolicy.go`).

### 3.5 Changing embedding models

Vectors from different models are not comparable, so all stored embeddings and
every query must come from the same model — which is why `EMBEDDINGS_MODEL` /
`EMBEDDINGS_DIMENSIONS` are pinned config and every row records its
`embedding_model`. If we later switch models (better quality, provider change,
self-host divergence), the procedure is:

1. **Same dimension** (e.g. 1536 → 1536): deploy the new `EMBEDDINGS_MODEL`.
   Retrieval temporarily filters to rows whose `embedding_model` matches the
   active model; a background sweep re-embeds rows `WHERE embedding_model !=
   current` in batches (content is small — the whole corpus re-embeds for
   cents). Pinned rows first, so the always-injected set recovers immediately.
2. **Different dimension**: same sweep, but via a migration that adds a second
   vector column (`embedding_v2 vector(N)` + its HNSW index), backfills, then
   drops the old column and index once the sweep completes. The `vector(n)`
   type is fixed-width, so a dimension change is always a column swap, not an
   in-place update.
3. Self-host note: OSS deployments choosing a local model (e.g. Ollama
   `nomic-embed-text`, 768 dims) set `EMBEDDINGS_DIMENSIONS` before first
   migration; the migration templates the column width from it. Their
   `embedding_model` values simply differ from cloud — nothing else changes.

During a sweep, retrieval quality degrades gracefully (un-re-embedded rows are
invisible to search but still listed/pinned in the UI); nothing is ever lost
because `content` is the source of truth and embeddings are derived data.

## 4. Store layer — `services/api/internal/store/memory.go` + sqlc

New sqlc query file `db/sqlc/queries/memory.sql` (or the repo's equivalent
layout under `store/db`), regenerated code, and a hand-written wrapper following
`store/ai.go`:

```go
// All methods take store.Tenant and run under the RLS tenant context.
UpsertOrgMemory(ctx, t, row OrgMemoryRow) (created bool, err error) // dedupe on (org_id, content_hash)
SearchOrgMemories(ctx, t, embedding []float32, k int) ([]OrgMemory, error) // cosine, excludes disabled, unions pinned
ListOrgMemories(ctx, t, filter MemoryFilter, page ...) ([]OrgMemory, error)
UpdateOrgMemory(ctx, t, id string, patch MemoryPatch) error // content re-embed handled by caller
DeleteOrgMemory(ctx, t, id string) error
CountOrgMemories(ctx, t) (int, error) // quota
GetMemoryIngest / SetMemoryIngest(ctx, t, key, throughSeq)
TouchMemoriesUsed(ctx, t, ids []string) error // batched last_used_at
```

The `ai` package consumes these via a small interface (defined in
`services/api/internal/ai`, satisfied by `*store.Store`):

```go
type MemoryStore interface {
    SearchOrgMemories(...) // + Upsert, ingest watermarks, Touch
}
```

Tests: extend `store/orgscope_test.go` and
`services/api/internal/integration/integration_rls_test.go` so `org_memories` /
`org_memory_ingests` are covered by the cross-org isolation suite like every
other tenant table.

## 5. Embeddings client — new `internal/embeddings` package

OpenRouter has **no embeddings endpoint**, so this is a new vendor seam,
mirroring the `internal/openrouter` pattern (narrow client, injected at the
composition root, mockable):

```go
type Client struct { BaseURL, APIKey, Model string; HTTPClient *http.Client }
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error)
```

- Speaks the **OpenAI-compatible `POST /v1/embeddings`** wire format so any
  compatible provider works (OpenAI, Voyage via compat proxy, Ollama for
  self-host, etc.).
- Batches inputs (provider limits), retries with backoff on 429/5xx, enforces a
  per-call input-size cap.
- Config (§10): `EMBEDDINGS_BASE_URL`, `EMBEDDINGS_API_KEY`,
  `EMBEDDINGS_MODEL`, `EMBEDDINGS_DIMENSIONS`.
- Default model proposal: `text-embedding-3-small` at 1536 dims (cheap,
  well-understood; ~$0.02 / 1M tokens). **The chosen dimension is baked into
  the `vector(1536)` column — changing models later requires a re-embed
  migration**, so this is a day-one decision (§12).

Graceful degradation, matching the repo's convention: if embeddings config is
absent, memory is disabled exactly like the AI builder without
`OPENROUTER_API_KEY` — memory routes 503/hidden, `RunTurn` skips
retrieval/extraction, everything else works.

## 6. Agent-loop integration — `services/api/internal/ai`

### 6.1 Runner wiring

`ai.Runner` (loop.go) gains optional deps, nil = feature off (same pattern as
`UsageReporter`):

```go
Memory     MemoryStore        // nil → no retrieval/extraction
Embedder   *embeddings.Client // nil → no retrieval/extraction
MemoryTopK int                // default 8
```

Wired in `services/api/cmd/api/ai.go` (`wireAIBuilder`) only when embeddings
config + DB are present.

### 6.2 Retrieval (sync, pre-generation)

In `RunTurn`, after loading history and before assembling `messages`
(loop.go:190):

1. Skip unless `Memory != nil` and org settings have `memory_enabled`.
2. `Embedder.Embed([userText])` → `Memory.SearchOrgMemories(t, vec, topK)`;
   result = all pinned memories + top-k by cosine above a floor (e.g. 0.3),
   deduped, capped to a **token budget (~1,500 tokens)**.
3. Render as a delimited block appended to the system message:

   ```
   <company_memory>
   Facts Dropway has learned about this organization. Follow them unless the
   user says otherwise.
   - [style] Brand palette is navy #0A2540 with coral accents.
   - [preference] Every page ends with a "Book a demo" CTA.
   ...
   </company_memory>
   ```

4. Failure policy: **retrieval must never fail a turn.** Any embed/search error
   logs a warning and proceeds memory-less. Time-box the whole step
   (~2 s context timeout) so a slow provider can't add visible latency.
5. Best-effort `TouchMemoriesUsed` for the injected ids.

The block is injected per-turn and **not persisted into `ai_messages`** — the
transcript stays a clean record of user/assistant/tool messages, memory stays
current rather than frozen at first mention, and history rebuilds are
unaffected.

### 6.3 Extraction (async, post-turn) — new `services/api/internal/ai/memory.go`

After a successful turn (post `tw.Flush()`, alongside `publishDraft`), spawn a
detached-context goroutine (pattern: the session-release defer in
loop.go:140-144):

1. Load the transcript slice since the session's `through_seq` watermark.
2. One non-streaming OpenRouter chat call with an extraction prompt: "extract
   durable, org-level facts/preferences/styles/corrections a future website
   build should know; return JSON; return [] if nothing durable" — explicitly
   excluding one-off/site-specific instructions and anything secret-looking.
   Model: cheap tier via new `AI_MEMORY_MODEL` config (default e.g.
   `anthropic/claude-haiku-4-5`).
3. Embed candidates (one batched call), then per candidate: exact dedupe via
   `(org_id, content_hash)`, semantic dedupe via similarity ≥ ~0.90 against
   existing rows → update `updated_at` (refresh) instead of insert; never
   resurrect `disabled` rows.
4. Enforce the per-org memory quota (§9) — at cap, only refreshes, no inserts.
5. Advance the watermark in `org_memory_ingests`.
6. **Cost accounting:** the extraction generation is recorded through the
   existing `recordUsage` path into `ai_usage` (same OpenRouter generation-id
   dedupe), so it hits the org's monthly cap and the cloud Stripe meter
   (`cloud/billing/aimeter.go`) with zero new billing code. Embedding cost is
   negligible but logged.
7. Concurrency: one in-flight extraction per session (the turn-claim
   serialization already guarantees turns don't overlap; the watermark makes
   overlap harmless anyway). Failures log and leave the watermark unmoved —
   self-healing on the next turn.

### 6.4 Site- and skill-content indexing (decided: in P1)

Cross-site awareness ("like our launch site's pricing section") needs published
site content indexed, not just conversation. Per the July 2026 scope decision,
sites AND skills feed memory in the first release:

- New table `app.org_content_chunks` (`org_id`, `source_kind`
  `'site_version'|'skill'`, `version_id`/`skill_id` FKs with `ON DELETE
  CASCADE`, `path`, `chunk_seq`, `content`, `embedding`, RLS as above),
  populated asynchronously on site publish and skill-upload finalize
  (handler hooks → `ai.Runner.IndexSiteVersion` / `IndexSkill`), reading the
  version's manifest + blobs, stripping HTML, chunking to ~1 KB.
- Retrieval extends §6.2 with a second search over chunks (site chunks limited
  to each site's CURRENT version via the search query's join).
- Chunk rows are reclaimed automatically when versions are GC'd or skills
  deleted (the CASCADE FKs — no extra sweep).

## 7. Memory access surfaces — HTTP API, MCP, CLI

The Go API is the single memory authority; the MCP server and CLI are clients
of it. This keeps one embeddings seam, one quota/rate-limit path, one audit
trail, and preserves the existing invariant that the API is the only writer
(`services/mcp/internal/apiclient` doc comment).

### 7.1 HTTP API — `services/api/internal/handlers/aimemory.go`

Mounted under the existing authenticated `/v1` router; all admin-gated writes
re-check live membership like other org-settings endpoints:

| method & path | behavior |
|---|---|
| `GET /v1/ai/memories` | list (filter: kind, pinned, disabled, text query; paginated) — member |
| `POST /v1/ai/memories/search` | **semantic search**: `{query, k?}` → ranked memories (server embeds the query; pinned included) — member. Shared by dashboard, MCP, and CLI |
| `POST /v1/ai/memories` | create `manual` memory (server embeds; 422 over size cap; accepts optional `source_tool`) — member (see §12.5) |
| `PATCH /v1/ai/memories/{id}` | edit content (re-embed), pin/unpin, disable/enable — admin |
| `DELETE /v1/ai/memories/{id}` | hard delete — admin |
| `GET /v1/ai/memory/settings` · `PATCH …` | `memory_enabled` toggle + usage (count vs quota) — admin write |

Auth: accepted principals are dashboard JWTs, **MCP-audience OAuth tokens**
(the API already accepts these when `MCP_PUBLIC_URL` is set — `config.go`
`MCPAudience`), and **org API keys** (so headless agents/CI can use memory;
rides the existing per-key rate limits, `APIKEY_RATELIMIT_*`). Search gets a
modest additional per-principal rate limit since each call costs an embedding
request.

503 when the feature is unwired (no embeddings config), 403-style policy error
when `memory_enabled=false`, matching the AI routes' conventions. Audit-log
writes (`store/audit.go`) for create/edit/delete/toggle.

### 7.2 MCP tools — `services/mcp/internal/tools/tools.go`

New tools on the existing OAuth 2.1-protected MCP server, registered only when
the control-plane client is configured (same conditional-registration pattern
as the current write tools) and gated per org on `memory_enabled` **and**
`mcp_enabled`:

| tool | maps to | notes |
|---|---|---|
| `search_memory` | `POST /v1/ai/memories/search` | input `{query, k?}`; output: ranked `{content, kind, pinned, updated_at}` list. Tool description tells the agent to call it at task start ("fetch this organization's brand, style, and preference memory before building") |
| `list_memories` | `GET /v1/ai/memories` | browse/paginate, e.g. to show the user what's known |
| `add_memory` | `POST /v1/ai/memories` | input `{content, kind?, source_tool?}`; lets an external agent record a durable fact it learned while working ("their production domain is …"). Server-side dedupe applies, so over-eager agents converge instead of duplicating |

All three go through the API (§7.1) rather than reading the DB directly —
unlike the read-only site/skill tools — because search needs the embedder and
writes must stay on the API's quota/audit path. The tenant comes from the
validated OAuth token as today (`auth.TenantFromContext`); the forwarded
bearer token authenticates the API call, identical to `create_site` et al.
Scope coverage extends `cmd/mcp/main_scopes_test.go`.

This is the headline external-agent integration: a user connects the Dropway
MCP server in Cursor / Claude Code / Codex once, and every session in those
tools can pull org memory before generating and deposit new facts after.

### 7.3 CLI — `dropway memory` command group

New cobra commands (`cli/internal/cmd/memory.go`) over a new
`cli/internal/api/memory.go` client, using the CLI's existing OAuth login (or
an org API key, once the CLI grows key auth):

```
dropway memory search "<query>" [-k 8] [--json]
dropway memory list [--kind …] [--pinned] [--json]
dropway memory add "<content>" [--kind preference] # stamps source_tool=cli
dropway memory pin <id> | unpin <id> | rm <id>
dropway memory context [--budget 1500] # top memories rendered as a ready-to-
                                       # paste <company_memory> block
```

`--json` output and `memory context` are the agent-facing affordances: a
coding agent (or a user's shell script / pre-commit hook / agent config) can
inject `dropway memory context` into a prompt, and agents that prefer shelling
out over MCP get the same data. Rendering reuses the same block format as §6.2
so context looks identical regardless of which surface produced it.

## 8. Dashboard — `apps/dashboard`

- **Memory page** (org settings → "Company memory", `components/ai/` +
  `app/(dashboard)/settings/memory` per the app's routing): table of memories
  with kind badge, provenance (source_kind, `source_tool` attribution for
  externally added rows, and a link where the source still exists), pinned
  toggle, disable toggle, inline edit, delete with confirm,
  search box; "Add memory" for manual entries; empty/disabled states explaining
  the feature.
- **Settings toggle**: `memory_enabled` switch beside the existing AI toggle,
  with copy: memory is org-private, never shared across customers, deletable.
- **Builder chat surfacing** (`components/ai/builder-chat.tsx`): small "using
  company memory (n)" indicator per turn — new `memory_used` SSE event
  (`Event{Type: "status"}`-adjacent, additive so old clients ignore it).

## 9. Quotas, billing, and abuse bounds

- **Per-org memory cap**: constant in OSS (e.g. 2,000 rows), per-tier in cloud
  via the existing quota provider (`internal/quota`, `cloud/quota`). Enforced in
  the store on insert (manual → 4xx quota error; extraction → refresh-only).
- **Extraction spend** rides the existing per-turn/monthly USD caps because it
  books into `ai_usage` (§6.3.6) — no new cap machinery.
- **Content bounds**: memory content ≤ ~2 KB; extraction returns ≤ ~10
  candidates per turn; retrieval block ≤ ~1,500 tokens.
- **Cloud gating**: `memory_enabled` can additionally be tier-gated in the cloud
  build the same way AI is (mountCloud), if product wants it paid-only.

## 10. Configuration — `services/api/internal/config/config.go` + `deploy/.env.example`

| env var | default | purpose |
|---|---|---|
| `EMBEDDINGS_BASE_URL` | — (empty → memory disabled) | OpenAI-compatible embeddings endpoint |
| `EMBEDDINGS_API_KEY` | — | provider key (Fly: `fly secrets set`) |
| `EMBEDDINGS_MODEL` | `text-embedding-3-small` | must match column dimension |
| `EMBEDDINGS_DIMENSIONS` | `1536` | validated against the model at startup |
| `AI_MEMORY_MODEL` | `anthropic/claude-haiku-4-5` | extraction model (OpenRouter id) |
| `AI_MEMORY_TOPK` | `8` | retrieval k |
| `AI_MEMORY_MAX_PER_ORG` | `2000` | OSS per-org row cap |

Documented in `deploy/.env.example`; prod values via `fly secrets set` per the
existing convention (never in `fly.toml`).

## 11. Rollout & migration plan

1. **Infra pre-req**: enable `pgvector` on the Supabase project; switch
   `deploy/docker-compose.yml`'s `postgres` service to a `pgvector/pgvector`
   image (verify goose migration passes on both).
2. **Migration 0017** ships with the feature dark (`memory_enabled` default
   false, no embeddings config in prod yet).
3. **Deploy** API with embeddings secrets; enable `memory_enabled` for internal
   org(s); validate extraction quality and retrieval latency.
4. **Backfill** (optional, admin-triggered per org): walk existing
   `ai_sessions`/`chat_logs` through the same extraction pipeline using
   `org_memory_ingests` watermarks; run at low concurrency with a spend budget.
5. **GA**: enable per org from settings; later migration may flip the default.
6. **Rollback**: feature-off is config-only (unset embeddings vars or toggle
   org flag); tables are additive and inert when off.

## 12. Decisions (resolved 2026-07-23)

1. **Embedding provider & model** — DECIDED: OpenAI `text-embedding-3-small`,
   1536 dims (`vector(1536)`). Self-host OSS users may point
   `EMBEDDINGS_BASE_URL` at any OpenAI-compatible server. A later switch is
   recoverable via the per-row `embedding_model` tag and the §3.5 procedure.
2. **Retrieval latency budget** — DECIDED (default taken): hard 2 s timeout,
   fail-open memory-less.
3. **Cloud tier gating** — DECIDED (default taken): no extra tier gate at
   launch beyond the per-org `memory_enabled` flag; cloud gating can be added
   in mountCloud later without schema changes.
4. **Extraction sources** — DECIDED: builder sessions, shared chat logs
   (extraction fires on share/append), AND published site + skill content
   (chunk indexing, §6.4) are all in P1.
5. **Who may `add_memory`** — DECIDED: any org member (dashboard, MCP, CLI);
   edit/pin/delete stay admin-only.
6. **Remaining P2 items** — the extraction backfill job over historical
   transcripts and the `memory_used` builder-UI indicator.

## 13. Delivery phases

| phase | contents | status |
|---|---|---|
| **P0** | Migration 0017, store + sqlc, `internal/embeddings`, RLS tests | implemented |
| **P1** | Retrieval + extraction in the agent loop; chat-log extraction; site + skill content indexing (§6.4); CRUD + search API (§7.1); MCP memory tools (§7.2); `dropway memory` CLI (§7.3); dashboard Memory page + toggle; quotas/metering | implemented |
| **P2** | Extraction backfill job over historical transcripts, `memory_used` UI polish | future |

Rollout steps 1–3 of §11 (enable pgvector on Supabase, deploy dark, internal
validation) remain the operational path to production.
