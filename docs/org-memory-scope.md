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

This feature adds an **org-scoped memory store** with three moving parts:

1. **Extraction** — after each builder turn, an async LLM pass distills durable
   facts (brand voice, palette, product names, structural preferences,
   recurring corrections) into memory rows.
2. **Retrieval** — before each generation, the top-k relevant memories (plus all
   pinned ones) are injected into the system context of the agent loop.
3. **Curation** — a dashboard Memory page where org admins view, pin, edit,
   disable, and delete what Dropway has learned.

### Goals

- New site builds start pre-loaded with company context; users stop restating
  brand facts.
- Cross-site awareness: "make the pricing section like our launch site" works.
- Memory is visible, editable, and deletable per org (trust + compliance).
- Memory is strictly org-isolated with the same guarantees as every other
  tenant table.

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
```

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
| `source_kind` | `text NOT NULL CHECK (source_kind IN ('ai_session','chat_log','site_version','manual'))` | provenance for the UI |
| `source_id` | `uuid` | no FK — sources may be GC'd while the memory persists |
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

### 6.4 Site-content indexing (phase 2 of this scope)

Cross-site awareness ("like our launch site's pricing section") needs published
site content indexed, not just conversation. Deferred to P2 within this scope:

- New table `app.org_content_chunks` (`org_id`, `site_id`, `version_id`, `path`,
  `chunk_seq`, `content`, `embedding`, RLS as above), populated on publish by
  hooking the ingest path (`services/api/internal/ai/ingest.go` and the deploy
  ingest), text-extracting HTML → ~1 KB chunks.
- Retrieval extends §6.2 with a second search over chunks (own token budget).
- GC rows when versions are GC'd (`store/gc.go`).

P1 ships conversation-derived memory only; the schema above doesn't block P2.

## 7. HTTP API — memory CRUD

New handler file `services/api/internal/handlers/aimemory.go`, mounted under the
existing authenticated `/v1` router; all admin-gated writes re-check live
membership like other org-settings endpoints:

| method & path | behavior |
|---|---|
| `GET /v1/ai/memories` | list (filter: kind, pinned, disabled, text query; paginated) — member |
| `POST /v1/ai/memories` | create `manual` memory (server embeds; 422 over size cap) — admin |
| `PATCH /v1/ai/memories/{id}` | edit content (re-embed), pin/unpin, disable/enable — admin |
| `DELETE /v1/ai/memories/{id}` | hard delete — admin |
| `GET /v1/ai/memory/settings` · `PATCH …` | `memory_enabled` toggle + usage (count vs quota) — admin write |

503 when the feature is unwired (no embeddings config), 403-style policy error
when `memory_enabled=false`, matching the AI routes' conventions. Audit-log
writes (`store/audit.go`) for create/edit/delete/toggle.

## 8. Dashboard — `apps/dashboard`

- **Memory page** (org settings → "Company memory", `components/ai/` +
  `app/(dashboard)/settings/memory` per the app's routing): table of memories
  with kind badge, provenance (source_kind + link where the source still
  exists), pinned toggle, disable toggle, inline edit, delete with confirm,
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

## 12. Open decisions (need answers before implementation starts)

1. **Embedding provider & model** — proposal: OpenAI `text-embedding-3-small`,
   1536 dims. Locks the column dimension; also decides whether self-host OSS
   users need an OpenAI key (they can point `EMBEDDINGS_BASE_URL` at Ollama).
2. **Retrieval latency budget** — proposal: hard 2 s timeout, fail-open
   memory-less. Alternative: prefetch embeddings at message-received time.
3. **Cloud tier gating** — is memory available on free/Pro, or Business+ only?
   Affects only mountCloud gating, not the schema.
4. **Extraction trigger for chat_logs** — turns are the natural trigger for
   `ai_sessions`; shared chat logs have no "turn end". Proposal: extract on
   `append_chat`/`share_chat` MCP writes, watermarked the same way. Confirm
   product wants chat logs in scope for P1.
5. **P2 items in or out of first release train** — site-content chunks (§6.4)
   and MCP `search_memory`/`add_memory` tools on the MCP server
   (`services/mcp/`); both additive.

## 13. Delivery phases

| phase | contents | prerequisite decisions |
|---|---|---|
| **P0** | Migration 0017, store + sqlc, `internal/embeddings`, RLS tests | §12.1 |
| **P1** | Retrieval + extraction in the agent loop, CRUD API, dashboard Memory page + toggle, quotas/metering, rollout steps 1–3 | §12.2–3 |
| **P2** | Site-content chunk indexing, MCP memory tools, backfill job, `memory_used` UI polish | §12.4–5 |

Each phase is independently shippable; P0/P1 together deliver the user-visible
"agent knows your company" loop.
