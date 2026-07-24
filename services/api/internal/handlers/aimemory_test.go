// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/services/api/internal/store"
)

// fakeMemory is an in-memory MemoryStore (a separate fake, like the Keys
// pattern — Memory is its own API field, not part of SiteStore).
type fakeMemory struct {
	enabled    bool
	enabledErr error
	rows       map[string]store.OrgMemory
	pinned     []store.OrgMemory
	searchResp []store.OrgMemory
	count      int64
	quotaHit   bool // force ErrMemoryQuota from Upsert

	lastUpsert   store.NewMemoryInput
	lastSearchK  int32
	touchedIDs   []string
	setEnabledTo *bool
}

func newFakeMemory() *fakeMemory {
	return &fakeMemory{enabled: true, rows: map[string]store.OrgMemory{}}
}

func (f *fakeMemory) MemoryEnabled(_ context.Context, _ store.Tenant) (bool, error) {
	return f.enabled, f.enabledErr
}
func (f *fakeMemory) SetMemoryEnabled(_ context.Context, _ store.Tenant, enabled bool) error {
	f.enabled = enabled
	f.setEnabledTo = &enabled
	return nil
}
func (f *fakeMemory) UpsertOrgMemory(_ context.Context, t store.Tenant, in store.NewMemoryInput, _ int) (store.OrgMemory, bool, error) {
	f.lastUpsert = in
	if f.quotaHit {
		return store.OrgMemory{}, false, store.ErrMemoryQuota
	}
	hash := store.MemoryContentHash(in.Content)
	for _, m := range f.rows {
		if m.ContentHash == hash {
			return m, false, nil // dedupe → refresh
		}
	}
	m := store.OrgMemory{ID: "m_" + hash[:8], OrgID: t.OrgID, Kind: in.Kind, Content: in.Content, ContentHash: hash, SourceKind: in.SourceKind, SourceTool: in.SourceTool}
	f.rows[m.ID] = m
	return m, true, nil
}
func (f *fakeMemory) SearchOrgMemories(_ context.Context, _ store.Tenant, _ []float32, _ string, k int32) ([]store.OrgMemory, error) {
	f.lastSearchK = k
	return f.searchResp, nil
}
func (f *fakeMemory) ListPinnedOrgMemories(_ context.Context, _ store.Tenant, _ int32) ([]store.OrgMemory, error) {
	return f.pinned, nil
}
func (f *fakeMemory) ListOrgMemories(_ context.Context, _ store.Tenant, _ store.MemoryFilter) ([]store.OrgMemory, error) {
	out := make([]store.OrgMemory, 0, len(f.rows))
	for _, m := range f.rows {
		out = append(out, m)
	}
	return out, nil
}
func (f *fakeMemory) GetOrgMemory(_ context.Context, _ store.Tenant, id string) (store.OrgMemory, error) {
	m, ok := f.rows[id]
	if !ok {
		return store.OrgMemory{}, store.ErrNotFound
	}
	return m, nil
}
func (f *fakeMemory) UpdateOrgMemoryContent(_ context.Context, _ store.Tenant, id, content string, _ []float32, model, kind string) (store.OrgMemory, error) {
	m, ok := f.rows[id]
	if !ok {
		return store.OrgMemory{}, store.ErrNotFound
	}
	m.Content, m.Kind, m.EmbeddingModel = content, kind, model
	f.rows[id] = m
	return m, nil
}
func (f *fakeMemory) SetOrgMemoryPinned(_ context.Context, _ store.Tenant, id string, pinned bool) (store.OrgMemory, error) {
	m, ok := f.rows[id]
	if !ok {
		return store.OrgMemory{}, store.ErrNotFound
	}
	m.Pinned = pinned
	f.rows[id] = m
	return m, nil
}
func (f *fakeMemory) SetOrgMemoryDisabled(_ context.Context, _ store.Tenant, id string, disabled bool) (store.OrgMemory, error) {
	m, ok := f.rows[id]
	if !ok {
		return store.OrgMemory{}, store.ErrNotFound
	}
	m.Disabled = disabled
	f.rows[id] = m
	return m, nil
}
func (f *fakeMemory) DeleteOrgMemory(_ context.Context, _ store.Tenant, id string) error {
	if _, ok := f.rows[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}
func (f *fakeMemory) CountOrgMemories(_ context.Context, _ store.Tenant) (int64, error) {
	return f.count, nil
}
func (f *fakeMemory) TouchOrgMemoriesUsed(_ context.Context, _ store.Tenant, ids []string) error {
	f.touchedIDs = append(f.touchedIDs, ids...)
	return nil
}

// fakeMemEmbedder is a deterministic MemoryEmbedder; fail makes every call error
// (the provider-outage path).
type fakeMemEmbedder struct {
	fail  bool
	calls int
}

func (f *fakeMemEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("embeddings down")
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{1, 2, 3}
	}
	return out, nil
}
func (f *fakeMemEmbedder) ModelID() string { return "test-embed-model" }

// fakeMemGate is a MemoryGate with a switchable verdict.
type fakeMemGate struct{ allow bool }

func (g fakeMemGate) AllowMemory(context.Context, store.Tenant) (bool, string, error) {
	return g.allow, "plan_required", nil
}

// newMemoryTestAPI builds an API with memory wired, one admin and one member.
func newMemoryTestAPI(mem *fakeMemory, emb *fakeMemEmbedder) (*API, *fakeStore) {
	fs := newFakeStore()
	fs.p2().members["admin-user"] = "admin"
	fs.p2().members["member-user"] = "member"
	return &API{Store: fs, Memory: mem, MemoryEmbedder: emb, MemoryMaxPerOrg: 100}, fs
}

// callMemory invokes a memory handler through the real auth middleware with a
// chi route param, mirroring aiReq in ai_test.go.
func callMemory(t *testing.T, api *API, handler http.HandlerFunc, method, path, body, userID, role, paramID string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", "Bearer x")
	if paramID != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", paramID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	rr := httptest.NewRecorder()
	authed(handler, claims(userID, "org-1", role)).ServeHTTP(rr, req)
	return rr
}

func TestMemoryRoutes503WhenUnwired(t *testing.T) {
	api := &API{Store: newFakeStore()} // Memory + MemoryEmbedder nil
	rr := callMemory(t, api, api.ListMemories, http.MethodGet, "/v1/ai/memories", "", "member-user", "member", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestMemoryPlanGateReturns402WithUpgradeMessage(t *testing.T) {
	mem := newFakeMemory()
	api, _ := newMemoryTestAPI(mem, &fakeMemEmbedder{})
	api.MemoryGate = fakeMemGate{allow: false}

	// Every gated surface: list, create, search, patch, delete, settings PATCH.
	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
		paramID string
	}{
		{"list", api.ListMemories, http.MethodGet, "", ""},
		{"create", api.CreateMemory, http.MethodPost, `{"content":"x"}`, ""},
		{"search", api.SearchMemories, http.MethodPost, `{"query":"x"}`, ""},
		{"patch", api.PatchMemory, http.MethodPatch, `{"pinned":true}`, "m1"},
		{"delete", api.DeleteMemory, http.MethodDelete, "", "m1"},
		{"settings-patch", api.PatchMemorySettings, http.MethodPatch, `{"memory_enabled":true}`, ""},
	}
	for _, c := range cases {
		rr := callMemory(t, api, c.handler, c.method, "/v1/ai/memories", c.body, "admin-user", "admin", c.paramID)
		if rr.Code != http.StatusPaymentRequired {
			t.Errorf("%s: status = %d, want 402 (body %s)", c.name, rr.Code, rr.Body.String())
			continue
		}
		var e struct{ Error, Message string }
		_ = json.Unmarshal(rr.Body.Bytes(), &e)
		if e.Error != "plan_required" || !strings.Contains(e.Message, "Pro plan or above") {
			t.Errorf("%s: body = %s, want plan_required + upgrade message", c.name, rr.Body.String())
		}
	}

	// GET settings stays readable and reports plan_allowed=false.
	rr := callMemory(t, api, api.GetMemorySettings, http.MethodGet, "/v1/orgs/memory", "", "member-user", "member", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("settings GET status = %d, want 200", rr.Code)
	}
	var s struct {
		PlanAllowed bool `json:"plan_allowed"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &s)
	if s.PlanAllowed {
		t.Errorf("plan_allowed = true for a gated org, want false")
	}
}

func TestMemoryOrgFlagOffReturns403(t *testing.T) {
	mem := newFakeMemory()
	mem.enabled = false
	api, _ := newMemoryTestAPI(mem, &fakeMemEmbedder{})
	rr := callMemory(t, api, api.ListMemories, http.MethodGet, "/v1/ai/memories", "", "member-user", "member", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rr.Code, rr.Body.String())
	}
}

func TestCreateMemoryMemberAllowedAndDeduped(t *testing.T) {
	mem := newFakeMemory()
	api, _ := newMemoryTestAPI(mem, &fakeMemEmbedder{})

	rr := callMemory(t, api, api.CreateMemory, http.MethodPost, "/v1/ai/memories", `{"content":"Navy palette","kind":"style","source_tool":"cursor"}`, "member-user", "member", "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create = %d, want 201 (body %s)", rr.Code, rr.Body.String())
	}
	if mem.lastUpsert.Kind != "style" || mem.lastUpsert.SourceTool != "cursor" || mem.lastUpsert.SourceKind != "manual" {
		t.Errorf("upsert input = %+v", mem.lastUpsert)
	}
	if mem.lastUpsert.EmbeddingModel != "test-embed-model" || len(mem.lastUpsert.Embedding) == 0 {
		t.Errorf("embedding not attached: %+v", mem.lastUpsert)
	}

	// Same content again → 200 with created=false.
	rr = callMemory(t, api, api.CreateMemory, http.MethodPost, "/v1/ai/memories", `{"content":"Navy palette"}`, "member-user", "member", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("dedupe create = %d, want 200", rr.Code)
	}
	var out struct{ Created bool }
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Created {
		t.Error("created = true on dedupe, want false")
	}
}

func TestCreateMemoryQuota422(t *testing.T) {
	mem := newFakeMemory()
	mem.quotaHit = true
	api, _ := newMemoryTestAPI(mem, &fakeMemEmbedder{})
	rr := callMemory(t, api, api.CreateMemory, http.MethodPost, "/v1/ai/memories", `{"content":"x"}`, "member-user", "member", "")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "memory limit") {
		t.Errorf("body = %s, want memory-limit message", rr.Body.String())
	}
}

func TestCreateMemorySurvivesEmbedOutage(t *testing.T) {
	mem := newFakeMemory()
	api, _ := newMemoryTestAPI(mem, &fakeMemEmbedder{fail: true})
	rr := callMemory(t, api, api.CreateMemory, http.MethodPost, "/v1/ai/memories", `{"content":"stored un-embedded"}`, "member-user", "member", "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — the write must degrade, not fail (body %s)", rr.Code, rr.Body.String())
	}
	if mem.lastUpsert.Embedding != nil {
		t.Errorf("embedding = %v, want nil (stored for later repair)", mem.lastUpsert.Embedding)
	}
}

func TestSearchMemoriesPinnedFirstAndTouched(t *testing.T) {
	mem := newFakeMemory()
	mem.pinned = []store.OrgMemory{{ID: "p1", Kind: "style", Content: "Navy", Pinned: true}}
	mem.searchResp = []store.OrgMemory{{ID: "s1", Kind: "fact", Content: "Rockets", Distance: 0.2}}
	api, _ := newMemoryTestAPI(mem, &fakeMemEmbedder{})

	rr := callMemory(t, api, api.SearchMemories, http.MethodPost, "/v1/ai/memories/search", `{"query":"branding","k":5}`, "member-user", "member", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rr.Code, rr.Body.String())
	}
	var out struct {
		Memories []struct {
			ID       string   `json:"id"`
			Distance *float64 `json:"distance"`
		} `json:"memories"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.Memories) != 2 || out.Memories[0].ID != "p1" || out.Memories[1].ID != "s1" {
		t.Fatalf("memories = %+v, want pinned first then result", out.Memories)
	}
	if out.Memories[0].Distance != nil || out.Memories[1].Distance == nil {
		t.Errorf("distance present on pinned or missing on result: %+v", out.Memories)
	}
	if mem.lastSearchK != 5 {
		t.Errorf("k = %d, want 5", mem.lastSearchK)
	}
	if len(mem.touchedIDs) != 2 {
		t.Errorf("touched = %v, want both ids", mem.touchedIDs)
	}
}

func TestSearchMemoriesEmbedOutage502(t *testing.T) {
	api, _ := newMemoryTestAPI(newFakeMemory(), &fakeMemEmbedder{fail: true})
	rr := callMemory(t, api, api.SearchMemories, http.MethodPost, "/v1/ai/memories/search", `{"query":"x"}`, "member-user", "member", "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
}

func TestPatchAndDeleteMemoryAdminOnly(t *testing.T) {
	mem := newFakeMemory()
	mem.rows["m1"] = store.OrgMemory{ID: "m1", Kind: "fact", Content: "old", ContentHash: store.MemoryContentHash("old")}
	api, _ := newMemoryTestAPI(mem, &fakeMemEmbedder{})

	// Member → 403 on both.
	if rr := callMemory(t, api, api.PatchMemory, http.MethodPatch, "/v1/ai/memories/m1", `{"pinned":true}`, "member-user", "member", "m1"); rr.Code != http.StatusForbidden {
		t.Errorf("member patch = %d, want 403", rr.Code)
	}
	if rr := callMemory(t, api, api.DeleteMemory, http.MethodDelete, "/v1/ai/memories/m1", "", "member-user", "member", "m1"); rr.Code != http.StatusForbidden {
		t.Errorf("member delete = %d, want 403", rr.Code)
	}

	// Admin edit: content + kind + pin in one patch; content re-embeds.
	emb := api.MemoryEmbedder.(*fakeMemEmbedder)
	before := emb.calls
	rr := callMemory(t, api, api.PatchMemory, http.MethodPatch, "/v1/ai/memories/m1", `{"content":"new content","kind":"preference","pinned":true}`, "admin-user", "admin", "m1")
	if rr.Code != http.StatusOK {
		t.Fatalf("admin patch = %d (body %s)", rr.Code, rr.Body.String())
	}
	if emb.calls != before+1 {
		t.Errorf("content edit did not re-embed (calls %d → %d)", before, emb.calls)
	}
	if m := mem.rows["m1"]; m.Content != "new content" || m.Kind != "preference" || !m.Pinned {
		t.Errorf("row after patch = %+v", m)
	}

	// Admin delete → 204, then 404 on repeat.
	if rr := callMemory(t, api, api.DeleteMemory, http.MethodDelete, "/v1/ai/memories/m1", "", "admin-user", "admin", "m1"); rr.Code != http.StatusNoContent {
		t.Errorf("admin delete = %d, want 204", rr.Code)
	}
	if rr := callMemory(t, api, api.DeleteMemory, http.MethodDelete, "/v1/ai/memories/m1", "", "admin-user", "admin", "m1"); rr.Code != http.StatusNotFound {
		t.Errorf("repeat delete = %d, want 404", rr.Code)
	}
}

func TestMemorySettingsToggleAdminOnly(t *testing.T) {
	mem := newFakeMemory()
	mem.enabled = false
	mem.count = 7
	api, _ := newMemoryTestAPI(mem, &fakeMemEmbedder{})

	// Member cannot toggle.
	if rr := callMemory(t, api, api.PatchMemorySettings, http.MethodPatch, "/v1/orgs/memory", `{"memory_enabled":true}`, "member-user", "member", ""); rr.Code != http.StatusForbidden {
		t.Errorf("member toggle = %d, want 403", rr.Code)
	}
	// Admin enables; response reflects the new state + count + plan_allowed.
	rr := callMemory(t, api, api.PatchMemorySettings, http.MethodPatch, "/v1/orgs/memory", `{"memory_enabled":true}`, "admin-user", "admin", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("admin toggle = %d (body %s)", rr.Code, rr.Body.String())
	}
	var s struct {
		Enabled     bool  `json:"memory_enabled"`
		Count       int64 `json:"count"`
		PlanAllowed bool  `json:"plan_allowed"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &s)
	if !s.Enabled || s.Count != 7 || !s.PlanAllowed {
		t.Errorf("settings = %+v", s)
	}
	if mem.setEnabledTo == nil || !*mem.setEnabledTo {
		t.Error("SetMemoryEnabled(true) not called")
	}
}

func TestCreateMemoryRejectsBadInput(t *testing.T) {
	api, _ := newMemoryTestAPI(newFakeMemory(), &fakeMemEmbedder{})
	for name, body := range map[string]string{
		"empty":     `{"content":"   "}`,
		"bad kind":  `{"content":"x","kind":"vibe"}`,
		"oversized": `{"content":"` + strings.Repeat("a", 3000) + `"}`,
	} {
		rr := callMemory(t, api, api.CreateMemory, http.MethodPost, "/v1/ai/memories", body, "member-user", "member", "")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rr.Code)
		}
	}
}
