// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/chatspec"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// chatLogResponse is the API representation of a shared chat log.
type chatLogResponse struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
	// SiteID is the attached site (absent = unattached library entry).
	SiteID       *string   `json:"site_id,omitempty"`
	Title        string    `json:"title"`
	SourceTool   string    `json:"source_tool"`
	PanelEnabled bool      `json:"panel_enabled"`
	MessageCount int64     `json:"message_count"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

func toChatLogResponse(l store.ChatLog) chatLogResponse {
	return chatLogResponse{
		ID: l.ID, OrgID: l.OrgID, SiteID: l.SiteID, Title: l.Title,
		SourceTool: l.SourceTool, PanelEnabled: l.PanelEnabled,
		MessageCount: l.MessageCount, CreatedBy: l.CreatedBy, CreatedAt: l.CreatedAt,
	}
}

// chatMessageResponse is one chat-log entry.
type chatMessageResponse struct {
	Seq     int32  `json:"seq"`
	Role    string `json:"role"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	// Meta is the raw action metadata of a kind="action" row.
	Meta json.RawMessage `json:"meta,omitempty"`
	// VersionID stamps the deploy version current at append time.
	VersionID *string   `json:"version_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func toChatMessageResponse(m store.ChatMessage) chatMessageResponse {
	out := chatMessageResponse{
		Seq: m.Seq, Role: m.Role, Kind: m.Kind, Content: m.Content,
		VersionID: m.VersionID, CreatedAt: m.CreatedAt,
	}
	if len(m.Meta) > 0 {
		out.Meta = json.RawMessage(m.Meta)
	}
	return out
}

// chatMessageInput is one explicit message in a create/append request.
type chatMessageInput struct {
	Kind    string               `json:"kind"`
	Role    string               `json:"role"`
	Content string               `json:"content"`
	Meta    *chatspec.ActionMeta `json:"meta,omitempty"`
}

// chatImportPayload is the shared ingest shape: an inline raw export
// (normalized server-side) and/or explicit canonical messages, appended in
// that order.
type chatImportPayload struct {
	// Transcript is a raw export (Claude Code JSONL, ChatGPT JSON, plain text).
	Transcript string `json:"transcript,omitempty"`
	// Format hints the transcript parser ("auto" default).
	Format string `json:"format,omitempty"`
	// DeriveActions condenses tool activity in the transcript into
	// kind="action" rows instead of dropping it.
	DeriveActions bool `json:"derive_actions,omitempty"`
	// Messages are explicit canonical messages (a live agent appending turns
	// or action annotations).
	Messages []chatMessageInput `json:"messages,omitempty"`
}

// parse normalizes/validates the payload into storable messages. dropped
// reports transcript messages discarded by the import bound (disclosed).
func (p chatImportPayload) parse() (msgs []chatspec.Message, dropped int, err error) {
	if p.Transcript != "" {
		msgs, dropped, err = chatspec.Normalize([]byte(p.Transcript), p.Format, p.DeriveActions)
		if err != nil {
			return nil, 0, err
		}
	}
	for i, in := range p.Messages {
		m := chatspec.Message{Kind: in.Kind, Role: in.Role, Content: in.Content, Meta: in.Meta}
		if m.Kind == "" {
			m.Kind = chatspec.KindChat
		}
		if m.Role == "" && m.Kind == chatspec.KindAction {
			m.Role = chatspec.RoleAssistant
		}
		if err := chatspec.Validate(m); err != nil {
			return nil, 0, fmt.Errorf("message %d: %w", i, err)
		}
		msgs = append(msgs, m)
	}
	return msgs, dropped, nil
}

// requireChatLogs gates every chat endpoint on the org kill switch (mirrors
// the AI builder's requireAI). Fail-soft true when the row is missing.
func (a *API) requireChatLogs(w http.ResponseWriter, r *http.Request, t store.Tenant) bool {
	enabled, err := a.Store.ChatLogsEnabled(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !enabled {
		httpx.WriteError(w, fmt.Errorf("%w: chat logs are disabled for this organization", httpx.ErrForbidden))
		return false
	}
	return true
}

// requireChatLogOwnerOrAdmin gates a chat-log mutation to its creator (who
// must still be a live org member) or an org admin/owner.
func (a *API) requireChatLogOwnerOrAdmin(w http.ResponseWriter, r *http.Request, t store.Tenant, l store.ChatLog) bool {
	if l.CreatedBy == t.UserID {
		return a.requireOrgMember(w, r, t)
	}
	return a.requireAdmin(w, r, t)
}

// syncChatSurface refreshes the served chat surface after a mutation: the
// compiled transcript object the Worker reads at /__dropway/chat, and — when
// the log's site binding or panel flag changed — RouteValue.chat_id on the
// affected sites' routes. Postgres committed first and stays authoritative;
// a projection/storage failure here is surfaced so the caller can retry (the
// rebuild path backstops it), matching the publish → PutRoute posture.
func (a *API) syncChatSurface(r *http.Request, t store.Tenant, logID string, siteIDs ...*string) error {
	if a.Objects != nil && logID != "" {
		transcript, err := a.Store.CompileChatTranscript(r.Context(), t, logID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			// The log is gone (delete path) — drop the served object instead.
			if derr := a.Objects.DeleteChatTranscript(r.Context(), t.OrgID, logID); derr != nil {
				logger(r).Error("chat transcript delete failed", "chat_id", logID, "err", derr)
			}
		case err != nil:
			return err
		default:
			if err := a.Objects.PutChatTranscript(r.Context(), t.OrgID, logID, transcript); err != nil {
				return err
			}
		}
	}
	if a.Projection == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, sid := range siteIDs {
		if sid == nil || *sid == "" {
			continue
		}
		if _, dup := seen[*sid]; dup {
			continue
		}
		seen[*sid] = struct{}{}
		updates, err := a.Store.SiteChatRoutes(r.Context(), t, *sid)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue // site vanished under us; nothing at the edge to refresh
			}
			return err
		}
		for _, ru := range updates {
			if err := a.Projection.PutRoute(r.Context(), ru.Host, ru.Route); err != nil {
				logger(r).Error("chat route refresh failed", "host", ru.Host, "chat_id", logID, "err", err)
				return err
			}
		}
	}
	return nil
}

// CreateChatLog creates a log — optionally attached to a site, optionally
// seeded with an inline import. POST /v1/chats.
func (a *API) CreateChatLog(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	var req struct {
		Title      string  `json:"title"`
		SourceTool string  `json:"source_tool"`
		SiteID     *string `json:"site_id,omitempty"`
		chatImportPayload
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if len(req.Title) > chatspec.MaxTitleLen {
		httpx.WriteError(w, fmt.Errorf("%w: title exceeds %d characters", httpx.ErrBadRequest, chatspec.MaxTitleLen))
		return
	}
	msgs, dropped, err := req.parse()
	if err != nil && (req.Transcript != "" || len(req.Messages) > 0) {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}

	log, err := a.Store.CreateChatLog(r.Context(), t, req.Title, req.SourceTool, req.SiteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	var res store.AppendChatResult
	if len(msgs) > 0 {
		res, err = a.Store.AppendChatMessages(r.Context(), t, log.ID, msgs)
		if err != nil {
			// The inline import failed (e.g. a hard-cap 402): don't leave a
			// half-created empty log behind — create+import is one gesture.
			if derr := a.Store.DeleteChatLog(r.Context(), t, log.ID); derr != nil {
				logger(r).Error("chat log cleanup after failed import", "chat_id", log.ID, "err", derr)
			}
			writeStoreError(w, err)
			return
		}
		log.MessageCount = int64(len(res.Messages)) - res.Pruned
		if err := a.syncChatSurface(r, t, log.ID, log.SiteID); err != nil {
			httpx.WriteError(w, err)
			return
		}
	} else if log.SiteID != nil {
		if err := a.syncChatSurface(r, t, log.ID, log.SiteID); err != nil {
			httpx.WriteError(w, err)
			return
		}
	}

	logger(r).Info("chat log created", "chat_id", log.ID, "org_id", t.OrgID,
		"messages", len(res.Messages), "pruned", res.Pruned)
	a.recordAudit(r, t, audit.ActionChatLogCreate, "chatlog:"+log.ID, map[string]any{
		"title": log.Title, "source_tool": log.SourceTool,
		"site_id": strPtrOr(log.SiteID, ""), "imported": len(res.Messages),
	})
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"chat_log": toChatLogResponse(log),
		"appended": len(res.Messages),
		"pruned":   res.Pruned,
		"window":   res.Window,
		"dropped":  dropped,
	})
}

// ListChatLogs returns the org's chat library. GET /v1/chats.
func (a *API) ListChatLogs(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	logs, err := a.Store.ListChatLogs(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]chatLogResponse, len(logs))
	for i, l := range logs {
		out[i] = toChatLogResponse(l)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chat_logs": out})
}

// GetChatLog returns one log. GET /v1/chats/{id}.
func (a *API) GetChatLog(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	log, err := a.Store.GetChatLog(r.Context(), t, chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chat_log": toChatLogResponse(log)})
}

// DeleteChatLog removes a log and its messages. DELETE /v1/chats/{id}.
func (a *API) DeleteChatLog(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	log, err := a.Store.GetChatLog(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireChatLogOwnerOrAdmin(w, r, t, log) {
		return
	}
	if err := a.Store.DeleteChatLog(r.Context(), t, id); err != nil {
		writeStoreError(w, err)
		return
	}
	// Tear down the served surface: transcript object + chat_id on the
	// previously-attached site's routes. syncChatSurface handles the
	// gone-log case by deleting the object.
	if err := a.syncChatSurface(r, t, id, log.SiteID); err != nil {
		logger(r).Error("chat surface teardown failed", "chat_id", id, "err", err)
	}
	a.recordAudit(r, t, audit.ActionChatLogDelete, "chatlog:"+id, map[string]any{
		"title": log.Title, "site_id": strPtrOr(log.SiteID, ""),
	})
	w.WriteHeader(http.StatusNoContent)
}

// AppendChatMessages appends turns/annotations (or a normalized import) to a
// log. POST /v1/chats/{id}/messages.
func (a *API) AppendChatMessages(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	log, err := a.Store.GetChatLog(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireChatLogOwnerOrAdmin(w, r, t, log) {
		return
	}
	a.appendToLog(w, r, t, log)
}

// appendToLog is the shared append body for the log-scoped and site-scoped
// append endpoints (authz already checked by the caller).
func (a *API) appendToLog(w http.ResponseWriter, r *http.Request, t store.Tenant, log store.ChatLog) {
	var req chatImportPayload
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	msgs, dropped, err := req.parse()
	if err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if len(msgs) == 0 {
		httpx.WriteError(w, fmt.Errorf("%w: no messages to append", httpx.ErrBadRequest))
		return
	}
	res, err := a.Store.AppendChatMessages(r.Context(), t, log.ID, msgs)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := a.syncChatSurface(r, t, log.ID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	out := make([]chatMessageResponse, len(res.Messages))
	for i, m := range res.Messages {
		out[i] = toChatMessageResponse(m)
	}
	logger(r).Info("chat messages appended", "chat_id", log.ID, "org_id", t.OrgID,
		"appended", len(out), "pruned", res.Pruned)
	a.recordAudit(r, t, audit.ActionChatLogAppend, "chatlog:"+log.ID, map[string]any{
		"appended": len(out), "pruned": res.Pruned, "dropped": dropped,
	})
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"messages": out,
		"pruned":   res.Pruned,
		"window":   res.Window,
		"dropped":  dropped,
	})
}

// ListChatMessages pages a log's messages forward. GET
// /v1/chats/{id}/messages?after_seq=&limit=.
func (a *API) ListChatMessages(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	afterSeq, _ := strconv.Atoi(r.URL.Query().Get("after_seq"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	msgs, err := a.Store.ListChatMessages(r.Context(), t, chi.URLParam(r, "id"), int32(afterSeq), int32(limit))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]chatMessageResponse, len(msgs))
	for i, m := range msgs {
		out[i] = toChatMessageResponse(m)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": out})
}

// DeleteChatMessage removes one message by seq (mistakes, pasted secrets).
// DELETE /v1/chats/{id}/messages/{seq}.
func (a *API) DeleteChatMessage(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	seq, err := strconv.Atoi(chi.URLParam(r, "seq"))
	if err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: invalid seq", httpx.ErrBadRequest))
		return
	}
	log, err := a.Store.GetChatLog(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireChatLogOwnerOrAdmin(w, r, t, log) {
		return
	}
	if err := a.Store.DeleteChatMessage(r.Context(), t, id, int32(seq)); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := a.syncChatSurface(r, t, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionChatLogMessageDelete, "chatlog:"+id, map[string]any{"seq": seq})
	w.WriteHeader(http.StatusNoContent)
}

// SetChatLogSite attaches/detaches/moves a log's site binding. PUT
// /v1/chats/{id}/site {"site_id": "<id>" | null}.
func (a *API) SetChatLogSite(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		SiteID *string `json:"site_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	before, err := a.Store.GetChatLog(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireChatLogOwnerOrAdmin(w, r, t, before) {
		return
	}
	log, err := a.Store.SetChatLogSite(r.Context(), t, id, req.SiteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Refresh BOTH sides of a move: the site that lost the panel and the one
	// that gained it.
	if err := a.syncChatSurface(r, t, id, before.SiteID, log.SiteID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionChatLogAttach, "chatlog:"+id, map[string]any{
		"from_site": strPtrOr(before.SiteID, ""), "to_site": strPtrOr(log.SiteID, ""),
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chat_log": toChatLogResponse(log)})
}

// SetChatLogPanel flips the served-panel flag. PUT /v1/chats/{id}/panel.
func (a *API) SetChatLogPanel(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	before, err := a.Store.GetChatLog(r.Context(), t, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireChatLogOwnerOrAdmin(w, r, t, before) {
		return
	}
	log, err := a.Store.SetChatLogPanel(r.Context(), t, id, req.Enabled)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := a.syncChatSurface(r, t, id, log.SiteID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionChatLogPanel, "chatlog:"+id, map[string]any{"enabled": req.Enabled})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chat_log": toChatLogResponse(log)})
}

// GetSiteChat returns a site's attached log + messages (the dashboard's
// site-page panel). GET /v1/sites/{id}/chat.
func (a *API) GetSiteChat(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	log, err := a.Store.GetChatLogForSite(r.Context(), t, chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	msgs, err := a.Store.ListChatMessages(r.Context(), t, log.ID, 0, 0)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]chatMessageResponse, len(msgs))
	for i, m := range msgs {
		out[i] = toChatMessageResponse(m)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"chat_log": toChatLogResponse(log),
		"messages": out,
	})
}

// AppendSiteChat appends to a site's attached log, creating one if absent —
// the one-call agent flow (deploy, then narrate). POST /v1/sites/{id}/chat.
func (a *API) AppendSiteChat(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireChatLogs(w, r, t) {
		return
	}
	siteID := chi.URLParam(r, "id")
	log, err := a.Store.GetChatLogForSite(r.Context(), t, siteID)
	if errors.Is(err, store.ErrNotFound) {
		sid := siteID
		log, err = a.Store.CreateChatLog(r.Context(), t, "", "", &sid)
		if err == nil {
			a.recordAudit(r, t, audit.ActionChatLogCreate, "chatlog:"+log.ID, map[string]any{
				"site_id": siteID, "via": "site_append",
			})
		}
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireChatLogOwnerOrAdmin(w, r, t, log) {
		return
	}
	a.appendToLog(w, r, t, log)
}

// GetChatSettings reads the org kill switch. GET /v1/orgs/chat-logs.
func (a *API) GetChatSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	enabled, err := a.Store.ChatLogsEnabled(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled})
}

// PatchChatSettings flips the org kill switch (admin/owner only). PATCH
// /v1/orgs/chat-logs.
func (a *API) PatchChatSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) || !a.requireAdmin(w, r, t) {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if err := a.Store.SetChatLogsEnabled(r.Context(), t, req.Enabled); err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, t, audit.ActionChatLogSettings, "org:"+t.OrgID, map[string]any{"enabled": req.Enabled})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled})
}

// strPtrOr dereferences p or returns fallback (audit metadata convenience).
func strPtrOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// Compile-time proof the handler uses the storage key contract the Worker
// derives (chat-transcripts/<org>/<chat_id>.json) — see storage.ChatTranscriptKey.
var _ = storage.ChatTranscriptKey
