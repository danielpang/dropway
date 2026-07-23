// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// MemoryStore is the org-memory data surface (satisfied by *store.Store).
// Separate from SiteStore so the feature composes without widening the large
// interface and its test fakes — the same pattern as Keys/APIKeyStore.
type MemoryStore interface {
	MemoryEnabled(ctx context.Context, t store.Tenant) (bool, error)
	SetMemoryEnabled(ctx context.Context, t store.Tenant, enabled bool) error
	UpsertOrgMemory(ctx context.Context, t store.Tenant, in store.NewMemoryInput, maxPerOrg int) (store.OrgMemory, bool, error)
	SearchOrgMemories(ctx context.Context, t store.Tenant, embedding []float32, model string, k int32) ([]store.OrgMemory, error)
	ListPinnedOrgMemories(ctx context.Context, t store.Tenant, limit int32) ([]store.OrgMemory, error)
	ListOrgMemories(ctx context.Context, t store.Tenant, f store.MemoryFilter) ([]store.OrgMemory, error)
	GetOrgMemory(ctx context.Context, t store.Tenant, id string) (store.OrgMemory, error)
	UpdateOrgMemoryContent(ctx context.Context, t store.Tenant, id, content string, embedding []float32, model, kind string) (store.OrgMemory, error)
	SetOrgMemoryPinned(ctx context.Context, t store.Tenant, id string, pinned bool) (store.OrgMemory, error)
	SetOrgMemoryDisabled(ctx context.Context, t store.Tenant, id string, disabled bool) (store.OrgMemory, error)
	DeleteOrgMemory(ctx context.Context, t store.Tenant, id string) error
	CountOrgMemories(ctx context.Context, t store.Tenant) (int64, error)
	TouchOrgMemoriesUsed(ctx context.Context, t store.Tenant, ids []string) error
}

// MemoryEmbedder turns text into vectors for the search/create endpoints (the
// same seam the AI runner uses; wired to *embeddings.Client in main).
type MemoryEmbedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	ModelID() string
}

// MemoryExtractor runs the async memory-extraction pass over a chat log
// (satisfied by *ai.Runner). Optional: nil → chat writes never feed memory.
type MemoryExtractor interface {
	ExtractChatLogMemories(ctx context.Context, t store.Tenant, chatLogID string)
}

// MemoryIndexer chunks + embeds published content into org_content_chunks
// (satisfied by *ai.Runner). Optional: nil → publishes/skill uploads are not
// indexed for retrieval.
type MemoryIndexer interface {
	IndexSiteVersion(ctx context.Context, t store.Tenant, siteID, versionID string)
	IndexSkill(ctx context.Context, t store.Tenant, skillID, versionID string)
}

// indexPublishedContentAsync kicks off content indexing after a publish /
// skill finalize. Fire-and-forget like extraction (the indexer detaches +
// bounds its own context).
func (a *API) indexSiteVersionAsync(r *http.Request, t store.Tenant, siteID, versionID string) {
	if a.MemoryIndex == nil {
		return
	}
	go a.MemoryIndex.IndexSiteVersion(r.Context(), t, siteID, versionID)
}

func (a *API) indexSkillAsync(r *http.Request, t store.Tenant, skillID, versionID string) {
	if a.MemoryIndex == nil {
		return
	}
	go a.MemoryIndex.IndexSkill(r.Context(), t, skillID, versionID)
}

// extractChatMemoriesAsync kicks off extraction for a chat log after a
// successful share/append — the path by which external agents' sessions teach
// Dropway about the org. Fire-and-forget: the write's outcome never depends
// on it (the extractor detaches + bounds its own context).
func (a *API) extractChatMemoriesAsync(r *http.Request, t store.Tenant, logID string) {
	if a.MemoryExtract == nil {
		return
	}
	go a.MemoryExtract.ExtractChatLogMemories(r.Context(), t, logID)
}

// maxMemoryContentBytes bounds one memory's content (~2 KB per the scope doc).
const maxMemoryContentBytes = 2048

// MemoryGate decides whether an org may use org memory beyond the org-level
// memory_enabled switch. The cloud build gates it to Pro and above; OSS
// leaves it nil (allow all — self-host is BYO embeddings key). Same shape as
// AIGate; the cloud adapter satisfies both.
type MemoryGate interface {
	AllowMemory(ctx context.Context, t store.Tenant) (allowed bool, reason string, err error)
}

// memoryPlanRequiredMessage is the upgrade message every gated surface (API,
// and therefore MCP tools and the CLI, which relay the API's error body)
// shows a free org.
const memoryPlanRequiredMessage = "org memory requires a Pro plan or above; upgrade your plan in billing to use memory"

// requireMemoryPlan runs only the plan gate (used by the settings PATCH,
// which must be reachable to READ state but not toggleable on free).
func (a *API) requireMemoryPlan(w http.ResponseWriter, r *http.Request, t store.Tenant) bool {
	if a.MemoryGate == nil {
		return true
	}
	allowed, _, err := a.MemoryGate.AllowMemory(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !allowed {
		httpx.WriteJSON(w, http.StatusPaymentRequired,
			httpx.ErrorBody{Error: "plan_required", Message: memoryPlanRequiredMessage})
		return false
	}
	return true
}

// requireMemory guards the memory routes: the feature must be wired
// (Memory + MemoryEmbedder set), the org's plan must allow it (Pro+ on the
// hosted build), and, unless settingsOnly, the org's memory_enabled flag on.
// Mirrors requireAI's shape.
func (a *API) requireMemory(w http.ResponseWriter, r *http.Request, t store.Tenant, settingsOnly bool) bool {
	if a.Memory == nil || a.MemoryEmbedder == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "org memory is not configured"})
		return false
	}
	if settingsOnly {
		// GET settings stays readable on any plan so the dashboard can render
		// the upgrade state; writes go through requireMemoryPlan explicitly.
		return true
	}
	if !a.requireMemoryPlan(w, r, t) {
		return false
	}
	enabled, err := a.Memory.MemoryEnabled(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !enabled {
		httpx.WriteError(w, fmt.Errorf("%w: org memory is disabled for this organization", httpx.ErrForbidden))
		return false
	}
	return true
}

type memoryResponse struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Content    string   `json:"content"`
	SourceKind string   `json:"source_kind"`
	SourceID   *string  `json:"source_id,omitempty"`
	SourceTool string   `json:"source_tool,omitempty"`
	Pinned     bool     `json:"pinned"`
	Disabled   bool     `json:"disabled"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
	LastUsedAt string   `json:"last_used_at,omitempty"`
	Distance   *float64 `json:"distance,omitempty"` // search results only
}

func memoryToResponse(m store.OrgMemory, withDistance bool) memoryResponse {
	out := memoryResponse{
		ID:         m.ID,
		Kind:       m.Kind,
		Content:    m.Content,
		SourceKind: m.SourceKind,
		SourceID:   m.SourceID,
		SourceTool: m.SourceTool,
		Pinned:     m.Pinned,
		Disabled:   m.Disabled,
		CreatedAt:  m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:  m.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if m.LastUsedAt != nil {
		out.LastUsedAt = m.LastUsedAt.UTC().Format(time.RFC3339)
	}
	if withDistance {
		d := m.Distance
		out.Distance = &d
	}
	return out
}

// ---------------------------------------------------------------------------
// GET /v1/ai/memories?kind=&q=&pinned=&disabled=&limit=&offset=
// The curation list (member).
// ---------------------------------------------------------------------------

func (a *API) ListMemories(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireMemory(w, r, t, false) {
		return
	}
	q := r.URL.Query()
	f := store.MemoryFilter{
		Kind:            q.Get("kind"),
		Query:           q.Get("q"),
		PinnedOnly:      q.Get("pinned") == "true",
		IncludeDisabled: q.Get("disabled") == "true",
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil {
		f.Limit = int32(n)
	}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n >= 0 {
		f.Offset = int32(n)
	}
	rows, err := a.Memory.ListOrgMemories(r.Context(), t, f)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]memoryResponse, 0, len(rows))
	for _, m := range rows {
		out = append(out, memoryToResponse(m, false))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"memories": out})
}

// ---------------------------------------------------------------------------
// POST /v1/ai/memories   {content, kind?, source_tool?}
// Manual create (member — external-agent deposits are the point; dedupe + the
// per-org cap bound the blast radius; edit/pin/delete stay admin-only).
// ---------------------------------------------------------------------------

type createMemoryRequest struct {
	Content    string `json:"content"`
	Kind       string `json:"kind,omitempty"`
	SourceTool string `json:"source_tool,omitempty"`
}

func (a *API) CreateMemory(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireMemory(w, r, t, false) {
		return
	}
	var req createMemoryRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		httpx.WriteError(w, fmt.Errorf("%w: content is required", httpx.ErrBadRequest))
		return
	}
	if len(req.Content) > maxMemoryContentBytes {
		httpx.WriteError(w, fmt.Errorf("%w: content exceeds %d bytes", httpx.ErrBadRequest, maxMemoryContentBytes))
		return
	}
	kind := req.Kind
	switch kind {
	case "":
		kind = "manual"
	case "fact", "preference", "style", "correction", "manual":
	default:
		httpx.WriteError(w, fmt.Errorf("%w: unknown kind %q", httpx.ErrBadRequest, kind))
		return
	}

	vecs, err := a.MemoryEmbedder.Embed(r.Context(), []string{req.Content})
	if err != nil || len(vecs) != 1 {
		// Store un-embedded rather than fail the write: content is the source
		// of truth and a later sweep can repair the embedding.
		vecs = [][]float32{nil}
	}
	uid := t.UserID
	mem, created, err := a.Memory.UpsertOrgMemory(r.Context(), t, store.NewMemoryInput{
		Kind:           kind,
		Content:        req.Content,
		Embedding:      vecs[0],
		EmbeddingModel: a.MemoryEmbedder.ModelID(),
		SourceKind:     "manual",
		SourceTool:     strings.TrimSpace(req.SourceTool),
		CreatedBy:      &uid,
	}, a.MemoryMaxPerOrg)
	if err != nil {
		if err == store.ErrMemoryQuota {
			httpx.WriteJSON(w, http.StatusUnprocessableEntity,
				httpx.ErrorBody{Error: "quota", Message: "this organization is at its memory limit; delete entries to add more"})
			return
		}
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionMemoryCreate, "memory:"+mem.ID, map[string]any{
		"kind": mem.Kind, "source_tool": mem.SourceTool, "deduped": !created,
	})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"memory": memoryToResponse(mem, false), "created": created})
}

// ---------------------------------------------------------------------------
// POST /v1/ai/memories/search   {query, k?}
// Semantic search (member): pinned rows + top-k by cosine distance. Shared by
// the dashboard, MCP search_memory, and the CLI.
// ---------------------------------------------------------------------------

type searchMemoryRequest struct {
	Query string `json:"query"`
	K     int    `json:"k,omitempty"`
}

func (a *API) SearchMemories(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireMemory(w, r, t, false) {
		return
	}
	var req searchMemoryRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		httpx.WriteError(w, fmt.Errorf("%w: query is required", httpx.ErrBadRequest))
		return
	}
	k := int32(req.K)
	if k <= 0 || k > 50 {
		k = 8
	}
	vecs, err := a.MemoryEmbedder.Embed(r.Context(), []string{req.Query})
	if err != nil || len(vecs) != 1 {
		httpx.WriteJSON(w, http.StatusBadGateway,
			httpx.ErrorBody{Error: "embeddings_unavailable", Message: "the embeddings provider is unavailable; try again shortly"})
		return
	}
	pinned, err := a.Memory.ListPinnedOrgMemories(r.Context(), t, 20)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	found, err := a.Memory.SearchOrgMemories(r.Context(), t, vecs[0], a.MemoryEmbedder.ModelID(), k)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]memoryResponse, 0, len(pinned)+len(found))
	var usedIDs []string
	for _, m := range pinned {
		out = append(out, memoryToResponse(m, false))
		usedIDs = append(usedIDs, m.ID)
	}
	for _, m := range found {
		out = append(out, memoryToResponse(m, true))
		usedIDs = append(usedIDs, m.ID)
	}
	_ = a.Memory.TouchOrgMemoriesUsed(r.Context(), t, usedIDs) // best-effort
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"memories": out})
}

// ---------------------------------------------------------------------------
// PATCH /v1/ai/memories/{id}   {content?, kind?, pinned?, disabled?}   (admin)
// DELETE /v1/ai/memories/{id}                                          (admin)
// ---------------------------------------------------------------------------

type patchMemoryRequest struct {
	Content  *string `json:"content,omitempty"`
	Kind     *string `json:"kind,omitempty"`
	Pinned   *bool   `json:"pinned,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

func (a *API) PatchMemory(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireMemory(w, r, t, false) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	var req patchMemoryRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}

	cur, err := a.Memory.GetOrgMemory(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	if req.Content != nil || req.Kind != nil {
		content := cur.Content
		if req.Content != nil {
			content = strings.TrimSpace(*req.Content)
		}
		if content == "" || len(content) > maxMemoryContentBytes {
			httpx.WriteError(w, fmt.Errorf("%w: content must be 1..%d bytes", httpx.ErrBadRequest, maxMemoryContentBytes))
			return
		}
		kind := cur.Kind
		if req.Kind != nil {
			kind = *req.Kind
		}
		switch kind {
		case "fact", "preference", "style", "correction", "manual":
		default:
			httpx.WriteError(w, fmt.Errorf("%w: unknown kind %q", httpx.ErrBadRequest, kind))
			return
		}
		vecs, err := a.MemoryEmbedder.Embed(r.Context(), []string{content})
		if err != nil || len(vecs) != 1 {
			vecs = [][]float32{nil} // repairable later; the edit still lands
		}
		cur, err = a.Memory.UpdateOrgMemoryContent(r.Context(), t, id, content, vecs[0], a.MemoryEmbedder.ModelID(), kind)
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if req.Pinned != nil {
		cur, err = a.Memory.SetOrgMemoryPinned(r.Context(), t, id, *req.Pinned)
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if req.Disabled != nil {
		cur, err = a.Memory.SetOrgMemoryDisabled(r.Context(), t, id, *req.Disabled)
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	a.recordAudit(r, t, audit.ActionMemoryUpdate, "memory:"+id, map[string]any{
		"edited": req.Content != nil, "pinned": req.Pinned, "disabled": req.Disabled,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"memory": memoryToResponse(cur, false)})
}

func (a *API) DeleteMemory(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireMemory(w, r, t, false) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.Memory.DeleteOrgMemory(r.Context(), t, id); err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionMemoryDelete, "memory:"+id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// GET  /v1/orgs/memory  → {memory_enabled, count, max}
// PATCH /v1/orgs/memory {memory_enabled}   (admin)
// Settings work even while the org flag is off (that's how it gets turned on).
// ---------------------------------------------------------------------------

type memorySettingsResponse struct {
	Enabled bool  `json:"memory_enabled"`
	Count   int64 `json:"count"`
	Max     int   `json:"max,omitempty"`
	// PlanAllowed is false when the org's plan tier excludes memory (free on
	// the hosted build) — the dashboard renders an upgrade prompt instead of
	// the toggle. Always true on OSS/self-host.
	PlanAllowed bool `json:"plan_allowed"`
}

func (a *API) GetMemorySettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireMemory(w, r, t, true) {
		return
	}
	planAllowed := true
	if a.MemoryGate != nil {
		allowed, _, err := a.MemoryGate.AllowMemory(r.Context(), t)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		planAllowed = allowed
	}
	enabled, err := a.Memory.MemoryEnabled(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	count, err := a.Memory.CountOrgMemories(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, memorySettingsResponse{
		Enabled: enabled, Count: count, Max: a.MemoryMaxPerOrg, PlanAllowed: planAllowed,
	})
}

type patchMemorySettingsRequest struct {
	Enabled *bool `json:"memory_enabled,omitempty"`
}

func (a *API) PatchMemorySettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireMemory(w, r, t, true) {
		return
	}
	// A free org can READ the settings (upgrade prompt) but not enable memory.
	if !a.requireMemoryPlan(w, r, t) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	var req patchMemorySettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.Enabled != nil {
		if err := a.Memory.SetMemoryEnabled(r.Context(), t, *req.Enabled); err != nil {
			writeStoreError(w, err)
			return
		}
		a.recordAudit(r, t, audit.ActionMemoryToggle, "org:"+t.OrgID, map[string]any{"enabled": *req.Enabled})
	}
	a.GetMemorySettings(w, r)
}
