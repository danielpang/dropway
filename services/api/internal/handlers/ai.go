// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/quota"
	aipkg "github.com/danielpang/dropway/services/api/internal/ai"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// defaultAIMaxConcurrent bounds active AI sessions per org when unset.
const defaultAIMaxConcurrent = 2

func (a *API) aiMaxConcurrent() int {
	if a.AIMaxConcurrent > 0 {
		return a.AIMaxConcurrent
	}
	return defaultAIMaxConcurrent
}

// requireAI guards the AI routes: the runner + model catalog must be wired, the
// org kill switch on, and the AI gate (plan/card in cloud) satisfied. Returns
// the effective (possibly created) session context to the caller via the tenant.
func (a *API) requireAI(w http.ResponseWriter, r *http.Request, t store.Tenant) bool {
	if a.AI == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "the AI builder is not configured"})
		return false
	}
	settings, err := a.Store.GetAISettings(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !settings.Enabled {
		httpx.WriteError(w, fmt.Errorf("%w: the AI builder is disabled for this organization", httpx.ErrForbidden))
		return false
	}
	if a.AIGate != nil {
		allowed, reason, err := a.AIGate.AllowAI(r.Context(), t)
		if err != nil {
			writeStoreError(w, err)
			return false
		}
		if !allowed {
			httpx.WriteJSON(w, http.StatusPaymentRequired,
				httpx.ErrorBody{Error: "plan_required", Message: aiGateMessage(reason)})
			return false
		}
	}
	return true
}

func aiGateMessage(reason string) string {
	if reason == "plan_required" {
		return "the AI builder requires a paid plan with a payment method on file"
	}
	return "the AI builder is not available for this organization"
}

// ---------------------------------------------------------------------------
// POST /v1/ai/sessions   {site_id?, slug?, model?}
// Creates a builder session. When site_id is omitted a new site is created first
// (the "build a new site with AI" flow), so a session always has a site.
// ---------------------------------------------------------------------------

type createAISessionRequest struct {
	SiteID string `json:"site_id,omitempty"`
	Slug   string `json:"slug,omitempty"`  // for the create-new-site flow
	Model  string `json:"model,omitempty"` // defaults to AIDefaultModel
}

type aiSessionResponse struct {
	ID        string `json:"id"`
	SiteID    string `json:"site_id"`
	Status    string `json:"status"`
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
}

func sessionResponse(s store.AISession) aiSessionResponse {
	return aiSessionResponse{
		ID: s.ID, SiteID: s.SiteID, Status: s.Status, Model: s.Model,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// CreateAISession starts a builder session.
func (a *API) CreateAISession(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAI(w, r, t) {
		return
	}

	var req createAISessionRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	model := req.Model
	if model == "" {
		model = a.AIDefaultModel
	}
	if model == "" {
		httpx.WriteError(w, fmt.Errorf("%w: model is required", httpx.ErrBadRequest))
		return
	}

	siteID := req.SiteID
	var baseVersionID *string
	if siteID == "" {
		// Create-new-site flow: make the site first (quota-checked), start blank.
		if req.Slug == "" {
			httpx.WriteError(w, fmt.Errorf("%w: slug is required when creating a new site", httpx.ErrBadRequest))
			return
		}
		site, err := a.Store.CreateSite(r.Context(), t, req.Slug, "public")
		if err != nil {
			writeStoreError(w, err)
			return
		}
		siteID = site.ID
	} else {
		site, err := a.Store.GetSite(r.Context(), t, siteID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		baseVersionID = site.CurrentVersionID // edit the live version (nil = blank)
	}

	sess, err := a.Store.StartAISession(r.Context(), t, siteID, model, baseVersionID, a.aiMaxConcurrent())
	if err != nil {
		if err == store.ErrAIConcurrencyLimit {
			httpx.WriteError(w, fmt.Errorf("%w: too many active AI sessions; finish or close one first", httpx.ErrTooManyRequests))
			return
		}
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionAISessionStart, "site:"+siteID, map[string]any{
		"session_id": sess.ID, "model": model,
	})
	if a.Analytics != nil {
		a.Analytics.Capture(r.Context(), analytics.Event{
			DistinctID: t.UserID,
			Event:      "ai_session_started",
			Properties: map[string]any{"org_id": t.OrgID, "site_id": siteID, "model": model},
			Groups:     map[string]string{"organization": t.OrgID},
		})
	}
	httpx.WriteJSON(w, http.StatusCreated, sessionResponse(sess))
}

// ListAISessions lists a site's sessions (?site_id=).
func (a *API) ListAISessions(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := r.URL.Query().Get("site_id")
	if siteID == "" {
		httpx.WriteError(w, fmt.Errorf("%w: site_id is required", httpx.ErrBadRequest))
		return
	}
	sessions, err := a.Store.ListAISessionsForSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]aiSessionResponse, len(sessions))
	for i, s := range sessions {
		out[i] = sessionResponse(s)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// GetAISession returns a session + its transcript.
func (a *API) GetAISession(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	id := chi.URLParam(r, "id")
	sess, err := a.Store.GetAISession(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	msgs, err := a.Store.ListAIMessages(r.Context(), t, id, 0)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]any{
			"seq": m.Seq, "role": m.Role, "content": json.RawMessage(m.Content),
			"created_at": m.CreatedAt.UTC().Format(time.RFC3339),
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"session": sessionResponse(sess), "messages": out,
	})
}

// DeleteAISession archives/removes a session.
func (a *API) DeleteAISession(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.Store.DeleteAISession(r.Context(), t, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// POST /v1/ai/sessions/{id}/messages   {text}   (streams the turn as SSE)
// ---------------------------------------------------------------------------

type aiMessageRequest struct {
	Text string `json:"text"`
}

// PostAIMessage runs one builder turn and streams its events as SSE on the same
// response. The model's tokens, tool activity, and the final draft_ready (with
// the preview URL) arrive live. The turn is bounded by a server-side deadline.
func (a *API) PostAIMessage(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAI(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	sess, err := a.Store.GetAISession(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	var req aiMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.Text == "" {
		httpx.WriteError(w, fmt.Errorf("%w: text is required", httpx.ErrBadRequest))
		return
	}

	// Atomically claim the session for this turn. A second concurrent turn (double
	// click, second tab, reconnect) is rejected here rather than racing on the
	// ai_messages seq unique key and dying with a raw DB error. The claim is
	// released when RunTurn resets the status to active.
	claimed, err := a.Store.TryBeginAITurn(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !claimed {
		httpx.WriteError(w, fmt.Errorf("%w: a turn is already running for this session", httpx.ErrConflict))
		return
	}
	// If we fail to start streaming below (no Flusher), release the claim so the
	// session isn't stuck 'running'.
	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = a.Store.SetAISessionStatus(r.Context(), t, id, "active")
		httpx.WriteJSON(w, http.StatusInternalServerError,
			httpx.ErrorBody{Error: "internal_error", Message: "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Bound the whole turn (tool loop + build). The dashboard reconnects to the
	// events endpoint for the persisted transcript if the connection drops.
	ctx, cancel := contextWithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	emit := func(ev aipkg.Event) {
		writeSSE(w, flusher, ev)
	}
	ctx = aipkg.WithEmit(ctx, emit)

	if err := a.AI.RunTurn(ctx, t, sess, req.Text, a.previewTTL(), a.ContentURL); err != nil {
		if _, capped := quota.AsExceeded(err); capped && a.Analytics != nil {
			a.Analytics.Capture(r.Context(), analytics.Event{
				DistinctID: t.UserID,
				Event:      "ai_cap_hit",
				Properties: map[string]any{"org_id": t.OrgID, "session_id": id},
				Groups:     map[string]string{"organization": t.OrgID},
			})
		}
		// Surface a terminal error event, then close the stream.
		emit(aipkg.Event{Type: "error", Error: aiErrorMessage(err)})
		return
	}
	emit(aipkg.Event{Type: "done"})
}

// GetAIEvents replays a session's persisted transcript as SSE, resuming after
// the Last-Event-ID (= message seq). It is the reconnect/history path; live
// turn events come from PostAIMessage's streamed response.
func (a *API) GetAIEvents(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := a.Store.GetAISession(r.Context(), t, id); err != nil {
		writeStoreError(w, err)
		return
	}
	var after int32
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, perr := parseInt32(v); perr == nil {
			after = n
		}
	}
	msgs, err := a.Store.ListAIMessages(r.Context(), t, id, after)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteJSON(w, http.StatusInternalServerError,
			httpx.ErrorBody{Error: "internal_error", Message: "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	for _, m := range msgs {
		writeSSERaw(w, flusher, m.Seq, map[string]any{
			"type": "message", "role": m.Role, "content": json.RawMessage(m.Content),
		})
	}
	writeSSERaw(w, flusher, 0, map[string]any{"type": "replay_done"})
}

// ---------------------------------------------------------------------------
// GET /v1/ai/models
// ---------------------------------------------------------------------------

// ListAIModels proxies the OpenRouter catalog (server-side, so the key stays
// server-side and self-host gets the same picker).
func (a *API) ListAIModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenant(r.Context()); !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if a.AIModels == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable,
			httpx.ErrorBody{Error: "unavailable", Message: "the AI builder is not configured"})
		return
	}
	models, err := a.AIModels.Models(r.Context())
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway,
			httpx.ErrorBody{Error: "upstream_error", Message: "could not load the model catalog"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"models": models, "default": a.AIDefaultModel,
	})
}

// ---------------------------------------------------------------------------
// GET /v1/orgs/ai  and  PATCH /v1/orgs/ai   (kill switch + spend cap + usage)
// ---------------------------------------------------------------------------

type aiSettingsResponse struct {
	Enabled       bool    `json:"ai_enabled"`
	MonthlyCapUSD float64 `json:"ai_monthly_cap_usd"`
	SpentUSD      float64 `json:"spent_usd"`
}

// GetAIOrgSettings returns the org AI settings + current-period spend.
func (a *API) GetAIOrgSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	settings, err := a.Store.GetAISettings(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Sum spend over the SAME window the cap is enforced against (the Stripe
	// billing period on cloud, the calendar month otherwise), so the displayed
	// figure can't disagree with a 402 the user hits.
	spent, err := a.Store.AISpendSince(r.Context(), t, a.aiSpendPeriodStart(r.Context(), t))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, aiSettingsResponse{
		Enabled: settings.Enabled, MonthlyCapUSD: settings.MonthlyCapUSD, SpentUSD: spent,
	})
}

type patchAISettingsRequest struct {
	Enabled       *bool    `json:"ai_enabled,omitempty"`
	MonthlyCapUSD *float64 `json:"ai_monthly_cap_usd,omitempty"`
}

// PatchAIOrgSettings updates the org AI kill switch and/or spend cap (admin).
func (a *API) PatchAIOrgSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !a.requireAdmin(w, r, t) {
		return
	}
	var req patchAISettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.Enabled != nil {
		if err := a.Store.SetAIEnabled(r.Context(), t, *req.Enabled); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if req.MonthlyCapUSD != nil {
		if *req.MonthlyCapUSD < 0 {
			httpx.WriteError(w, fmt.Errorf("%w: cap must be non-negative", httpx.ErrBadRequest))
			return
		}
		if err := a.Store.SetAIMonthlyCap(r.Context(), t, *req.MonthlyCapUSD); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	a.recordAudit(r, t, audit.ActionAISettings, "org:"+t.OrgID, map[string]any{
		"enabled": req.Enabled, "cap": req.MonthlyCapUSD,
	})
	a.GetAIOrgSettings(w, r)
}
