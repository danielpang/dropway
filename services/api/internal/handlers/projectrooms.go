// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/services/api/internal/apikeygrant"
	"github.com/danielpang/dropway/services/api/internal/orchestrate"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/sprites"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// ProjectRoomStore is the room, review, and candidate data API.
type ProjectRoomStore interface {
	ProjectRoomsEnabled(ctx context.Context, t store.Tenant) (bool, error)
	SetProjectRoomsEnabled(ctx context.Context, t store.Tenant, enabled bool, rec store.AuditRecord) error
	CreateProjectRoom(ctx context.Context, t store.Tenant, name string, rec store.AuditRecord) (store.Room, error)
	ListProjectRooms(ctx context.Context, t store.Tenant) ([]store.Room, error)
	CreateReviewThread(ctx context.Context, t store.Tenant, roomID, siteID, versionID, page, kind string, anchor json.RawMessage) (store.ReviewThread, error)
	AddReviewReply(ctx context.Context, t store.Tenant, roomID, threadID, body string) (store.ReviewReply, error)
	SignoffVersion(ctx context.Context, t store.Tenant, roomID, siteID, versionID string) (store.VersionSignoff, error)
	SubmitCandidate(ctx context.Context, t store.Tenant, roomID, siteID, baseVersionID, contentHash, diffHash, summary string) (store.Candidate, error)
	ApproveCandidate(ctx context.Context, t store.Tenant, roomID string, req projectroom.ApprovalRequest, rec store.AuditRecord) (store.CandidateDecision, error)
	RejectCandidate(ctx context.Context, t store.Tenant, roomID string, req projectroom.ApprovalRequest, rec store.AuditRecord) (store.CandidateDecision, error)
	ApplyCandidate(ctx context.Context, t store.Tenant, roomID string, req projectroom.ApprovalRequest) (store.Candidate, error)
	RoomAccess(ctx context.Context, t store.Tenant, roomID, siteID string) (string, projectroom.SiteRights, error)
	GetProjectRoom(ctx context.Context, t store.Tenant, roomID string) (store.RoomDetail, error)
	AddProjectRoomMember(ctx context.Context, t store.Tenant, roomID, userID, role string, rec store.AuditRecord) error
	UpdateProjectRoomMemberRole(ctx context.Context, t store.Tenant, roomID, userID, role string, rec store.AuditRecord) error
	RemoveProjectRoomMember(ctx context.Context, t store.Tenant, roomID, userID string, rec store.AuditRecord) error
	AddProjectRoomSite(ctx context.Context, t store.Tenant, roomID, siteID string, rec store.AuditRecord) error
	RemoveProjectRoomSite(ctx context.Context, t store.Tenant, roomID, siteID string, rec store.AuditRecord) error
	PageRoomThreads(ctx context.Context, t store.Tenant, roomID, cursor string, limit int) (store.ThreadPage, error)
	PageRoomCandidates(ctx context.Context, t store.Tenant, roomID, cursor string, limit int) (store.CandidatePage, error)
	GetRoomCandidate(ctx context.Context, t store.Tenant, roomID, candidateID string) (store.Candidate, error)
	ResolveReviewThread(ctx context.Context, t store.Tenant, roomID, threadID string, rec store.AuditRecord) (store.ReviewThread, error)
	ReanchorReviewThread(ctx context.Context, t store.Tenant, roomID, threadID, kind, state, reason string, anchor json.RawMessage, rec store.AuditRecord) (store.ReviewThread, error)
}

// rejectAgentLiveMutation was a caller-controlled header check. Credential
// class now comes only from the middleware that verified the request.
func allowCommentRead(r *http.Request) bool {
	class, ok := middleware.CredentialClassFromContext(r.Context())
	return ok && (class == middleware.CredentialHumanSession || class == middleware.CredentialMCPOAuth)
}

func humanSession(r *http.Request) bool {
	class, ok := middleware.CredentialClassFromContext(r.Context())
	return ok && class == middleware.CredentialHumanSession
}

// allowControlledSiteMutation fail-closes live source writes and publish on
// sites that belong to an enabled project room. Org API keys and MCP OAuth
// are not human sessions. Sites outside a room keep the existing CI path.
func (a *API) allowControlledSiteMutation(w http.ResponseWriter, r *http.Request, t store.Tenant, siteID string) bool {
	controlled, err := a.Store.ProjectReviewControlled(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !controlled {
		return true
	}
	if humanSession(r) {
		return true
	}
	httpx.WriteError(w, fmt.Errorf("%w: this site is under project review; publish, rollback, and source writes require an authenticated human session", httpx.ErrForbidden))
	return false
}

// EnforceAPIKeyGrant applies stored key scopes and site_id. Human sessions pass.
func EnforceAPIKeyGrant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, keyed := middleware.APIKeyIDFromContext(r.Context()); !keyed {
			next.ServeHTTP(w, r)
			return
		}
		grant, ok := middleware.APIKeyGrantFromContext(r.Context())
		if !ok {
			httpx.WriteError(w, fmt.Errorf("%w: api key grant missing", httpx.ErrForbidden))
			return
		}
		if err := apikeygrant.Decide(r.Method, r.URL.Path, grant.Scopes, grant.SiteID); err != nil {
			httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrForbidden, err.Error()))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) requireRooms(w http.ResponseWriter) bool {
	if a.Rooms == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, httpx.ErrorBody{Error: "unavailable", Message: "project rooms are not configured"})
		return false
	}
	return true
}

func (a *API) GetProjectRoomSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireRooms(w) {
		return
	}
	on, err := a.Rooms.ProjectRoomsEnabled(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": on})
}

func (a *API) SetProjectRoomSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: human session required", httpx.ErrForbidden))
		return
	}
	if !a.requireRooms(w) || !a.requireAdmin(w, r, t) {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		httpx.WriteError(w, fmt.Errorf("%w: enabled is required", httpx.ErrBadRequest))
		return
	}
	if err := a.Rooms.SetProjectRoomsEnabled(r.Context(), t, *req.Enabled, store.AuditRecord{Ctx: auditCtx(r)}); err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": *req.Enabled})
}

func (a *API) CreateProjectRoom(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok || !a.requireRooms(w) {
		if ok {
			return
		}
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: project room changes require an authenticated human session", httpx.ErrForbidden))
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	room, err := a.Rooms.CreateProjectRoom(r.Context(), t, req.Name, store.AuditRecord{Ctx: auditCtx(r)})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"room": room})
}

func (a *API) ListProjectRooms(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok || !a.requireRooms(w) {
		if ok {
			return
		}
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !allowCommentRead(r) {
		httpx.WriteError(w, fmt.Errorf("%w: reading project rooms requires a human session or MCP OAuth", httpx.ErrForbidden))
		return
	}
	rooms, err := a.Rooms.ListProjectRooms(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if rooms == nil {
		rooms = []store.Room{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

func (a *API) CreateReviewThread(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok || !a.requireRooms(w) {
		if ok {
			return
		}
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: project room changes require an authenticated human session", httpx.ErrForbidden))
		return
	}
	var req struct {
		SiteID    string          `json:"site_id"`
		VersionID string          `json:"version_id"`
		Page      string          `json:"page"`
		Kind      string          `json:"kind"`
		Anchor    json.RawMessage `json:"anchor"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	th, err := a.Rooms.CreateReviewThread(r.Context(), t, chi.URLParam(r, "roomID"), req.SiteID, req.VersionID, req.Page, req.Kind, req.Anchor)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"thread": th})
}

func (a *API) AddReviewReply(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok || !a.requireRooms(w) {
		if ok {
			return
		}
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: project room changes require an authenticated human session", httpx.ErrForbidden))
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	reply, err := a.Rooms.AddReviewReply(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "threadID"), req.Body)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"reply": reply})
}

func (a *API) SignoffVersion(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok || !a.requireRooms(w) {
		if ok {
			return
		}
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: project room changes require an authenticated human session", httpx.ErrForbidden))
		return
	}
	var req struct {
		SiteID    string `json:"site_id"`
		VersionID string `json:"version_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	sig, err := a.Rooms.SignoffVersion(r.Context(), t, chi.URLParam(r, "roomID"), req.SiteID, req.VersionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version_signoff": sig})
}

type candidateBody struct {
	CandidateID     string `json:"candidate_id"`
	ContentHash     string `json:"content_hash"`
	BaseVersionID   string `json:"base_version_id"`
	BaseContentHash string `json:"base_content_hash"`
}

func candidateReq(body candidateBody, human bool) projectroom.ApprovalRequest {
	return projectroom.ApprovalRequest{
		CandidateID: body.CandidateID, ContentHash: body.ContentHash,
		BaseVersionID: body.BaseVersionID, BaseContentHash: body.BaseContentHash,
		HumanSession: human,
	}
}

func (a *API) SubmitCandidate(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok || !a.requireRooms(w) {
		if ok {
			return
		}
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: project room changes require an authenticated human session", httpx.ErrForbidden))
		return
	}
	var req struct {
		SiteID        string `json:"site_id"`
		BaseVersionID string `json:"base_version_id"`
		ContentHash   string `json:"content_hash"`
		DiffHash      string `json:"diff_hash"`
		Summary       string `json:"summary"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	c, err := a.Rooms.SubmitCandidate(r.Context(), t, chi.URLParam(r, "roomID"), req.SiteID, req.BaseVersionID, req.ContentHash, req.DiffHash, req.Summary)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"candidate": c})
}

func (a *API) ApproveCandidate(w http.ResponseWriter, r *http.Request) {
	a.decideCandidateHTTP(w, r, true)
}

func (a *API) RejectCandidate(w http.ResponseWriter, r *http.Request) {
	a.decideCandidateHTTP(w, r, false)
}

func (a *API) decideCandidateHTTP(w http.ResponseWriter, r *http.Request, approve bool) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: approve-change and reject require a human session", httpx.ErrForbidden))
		return
	}
	if !a.requireRooms(w) {
		return
	}
	var body candidateBody
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	body.CandidateID = chi.URLParam(r, "candidateID")
	req := candidateReq(body, true)
	var (
		dec store.CandidateDecision
		err error
	)
	if approve {
		dec, err = a.Rooms.ApproveCandidate(r.Context(), t, chi.URLParam(r, "roomID"), req, store.AuditRecord{Ctx: auditCtx(r)})
	} else {
		dec, err = a.Rooms.RejectCandidate(r.Context(), t, chi.URLParam(r, "roomID"), req, store.AuditRecord{Ctx: auditCtx(r)})
	}
	if err != nil && !dec.Changed && dec.Candidate.ID == "" {
		writeStoreError(w, err)
		return
	}
	status := http.StatusOK
	if errorsIsConflict(err) {
		status = http.StatusConflict
	}
	httpx.WriteJSON(w, status, map[string]any{
		"candidate": dec.Candidate,
		"changed":   dec.Changed,
		"decision":  map[bool]string{true: "approve_change", false: "reject_change"}[approve],
	})
	if err != nil && status == http.StatusOK {
		return
	}
}

func (a *API) ApplyCandidate(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: applying a change requires a human session", httpx.ErrForbidden))
		return
	}
	if !a.requireRooms(w) {
		return
	}
	var body candidateBody
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	body.CandidateID = chi.URLParam(r, "candidateID")
	_, err := a.Rooms.ApplyCandidate(r.Context(), t, chi.URLParam(r, "roomID"), candidateReq(body, true))
	if err != nil {
		writeStoreError(w, err)
		return
	}
}

func (a *API) ClaimExecution(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok || !a.requireRooms(w) {
		if ok {
			return
		}
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: execution requires an authenticated human session", httpx.ErrForbidden))
		return
	}
	var req struct {
		SiteID          string `json:"site_id"`
		VersionID       string `json:"version_id"`
		CredentialRef   string `json:"credential_ref"`
		CredentialOwner string `json:"credential_owner"`
		Provider        string `json:"provider"`
		ModelID         string `json:"model_id"`
		HarnessID       string `json:"harness_id"`
		HarnessVersion  string `json:"harness_version"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if !orchestrate.CredentialOwnedBy(t.UserID, req.CredentialOwner) && req.CredentialOwner != "" {
		httpx.WriteError(w, fmt.Errorf("%w: credential must belong to the initiating user", httpx.ErrForbidden))
		return
	}
	if req.CredentialOwner == "" {
		req.CredentialOwner = t.UserID
	}
	if !orchestrate.CredentialOwnedBy(t.UserID, req.CredentialOwner) {
		httpx.WriteError(w, fmt.Errorf("%w: credential must belong to the initiating user", httpx.ErrForbidden))
		return
	}
	if err := validateModel(req.Provider, req.ModelID); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err.Error()))
		return
	}
	if req.HarnessID != orchestrate.HarnessPi || req.HarnessVersion == "" {
		httpx.WriteError(w, fmt.Errorf("%w: harness must be pi with an explicit version", httpx.ErrBadRequest))
		return
	}
	role, rights, err := a.Rooms.RoomAccess(r.Context(), t, chi.URLParam(r, "roomID"), req.SiteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !projectroom.CanRun(projectroom.Role(role), rights) {
		httpx.WriteError(w, fmt.Errorf("%w: owner or editor with site edit rights required", httpx.ErrForbidden))
		return
	}
	snap := orchestrate.SessionSnapshot{
		OrgID: t.OrgID, RoomID: chi.URLParam(r, "roomID"), SiteID: req.SiteID, VersionID: req.VersionID,
		InitiatorUserID: t.UserID, CredentialRef: req.CredentialRef, Provider: req.Provider,
		ModelID: req.ModelID, HarnessID: req.HarnessID, HarnessVersion: req.HarnessVersion,
	}
	runner := a.runner()
	if _, err := runner.Start(r.Context(), sprites.AttemptSpec{
		OrgID: snap.OrgID, RoomID: snap.RoomID, SiteID: snap.SiteID, VersionID: snap.VersionID,
		InitiatorUserID: snap.InitiatorUserID,
	}); err != nil && err != sprites.ErrExecutionDisabled {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
		"error":   "execution_disabled",
		"message": "hosted execution is disabled",
		"pinned":  snap,
		"blockers": []string{
			"credential_store_unavailable",
			"pi_sdk_unverified",
			"sprites_execution_disabled",
			"session_persistence_deferred",
		},
	})
}

func (a *API) ClarifySession(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok || !a.requireRooms(w) {
		if ok {
			return
		}
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: execution requires an authenticated human session", httpx.ErrForbidden))
		return
	}
	var req struct {
		SiteID         string `json:"site_id"`
		VersionID      string `json:"version_id"`
		CredentialRef  string `json:"credential_ref"`
		Provider       string `json:"provider"`
		ModelID        string `json:"model_id"`
		HarnessID      string `json:"harness_id"`
		HarnessVersion string `json:"harness_version"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	role, rights, err := a.Rooms.RoomAccess(r.Context(), t, chi.URLParam(r, "roomID"), req.SiteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !projectroom.CanRun(projectroom.Role(role), rights) {
		httpx.WriteError(w, fmt.Errorf("%w: owner or editor with site edit rights required", httpx.ErrForbidden))
		return
	}
	snap := orchestrate.SessionSnapshot{
		OrgID: t.OrgID, RoomID: chi.URLParam(r, "roomID"), SiteID: req.SiteID, VersionID: req.VersionID,
		InitiatorUserID: t.UserID, CredentialRef: req.CredentialRef, Provider: req.Provider,
		ModelID: req.ModelID, HarnessID: req.HarnessID, HarnessVersion: req.HarnessVersion,
	}
	_ = a.runner().Destroy(r.Context(), sprites.Handle{Provider: sprites.ProviderFly, OrgID: t.OrgID}, "clarification")
	httpx.WriteJSON(w, http.StatusConflict, map[string]any{
		"error":         "session_persistence_deferred",
		"message":       "clarification would persist the session and release the worker and sprite; persistence is not implemented",
		"clarification": orchestrate.PlanClarification(snap),
	})
}

func (a *API) runner() sprites.Runner {
	if a.Sprites != nil {
		return a.Sprites
	}
	return sprites.Disabled{}
}

func validateModel(provider, model string) error {
	allow := map[string]map[string]bool{
		"anthropic": {
			"claude-sonnet-4-5": true,
			"claude-opus-4-1":   true,
			"claude-haiku-4-5":  true,
		},
		"openrouter": {
			"anthropic/claude-sonnet-4-5": true,
			"anthropic/claude-opus-4-1":   true,
			"anthropic/claude-haiku-4-5":  true,
		},
	}
	if strings.Contains(model, "://") || strings.Contains(provider, "://") {
		return fmt.Errorf("user-supplied base urls are not allowed")
	}
	models := allow[provider]
	if models == nil || !models[model] {
		return fmt.Errorf("model is not allowed")
	}
	return nil
}

func errorsIsConflict(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "baseline") || strings.Contains(err.Error(), "conflict"))
}
