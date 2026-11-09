// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func queryLimit(r *http.Request) int {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		fmt.Sscan(raw, &limit)
	}
	return limit
}

func (a *API) ListSiteCommentPage(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !allowCommentRead(r) {
		httpx.WriteError(w, fmt.Errorf("%w: reading comments requires a human session or MCP OAuth", httpx.ErrForbidden))
		return
	}
	if !a.requireOrgMember(w, r, t) {
		return
	}
	page, err := a.Store.PageSiteComments(r.Context(), t, chi.URLParam(r, "id"), r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": commentItems(page.Items), "next_cursor": page.NextCursor, "comment_content": "untrusted_data",
	})
}

func (a *API) GetReviewThreadPage(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	if !allowCommentRead(r) {
		httpx.WriteError(w, fmt.Errorf("%w: reading comments requires a human session or MCP OAuth", httpx.ErrForbidden))
		return
	}
	if !a.requireOrgMember(w, r, t) {
		return
	}
	page, err := a.Store.PageReviewReplies(r.Context(), t, chi.URLParam(r, "threadID"), r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"thread": replyPageResponse(page), "comment_content": "untrusted_data",
	})
}

func (a *API) GetProjectRoom(w http.ResponseWriter, r *http.Request) {
	t, ok := a.readRoom(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	detail, err := a.Rooms.GetProjectRoom(r.Context(), t, chi.URLParam(r, "roomID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, roomDetailResponse(detail))
}

func (a *API) ListRoomThreads(w http.ResponseWriter, r *http.Request) {
	t, ok := a.readRoom(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	page, err := a.Rooms.PageRoomThreads(r.Context(), t, chi.URLParam(r, "roomID"), r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	threads := make([]map[string]any, len(page.Threads))
	for i, th := range page.Threads {
		threads[i] = threadHeadResponse(th)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"threads": threads, "next_cursor": page.NextCursor, "comment_content": "untrusted_data"})
}

func (a *API) ListRoomCandidates(w http.ResponseWriter, r *http.Request) {
	t, ok := a.readRoom(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	page, err := a.Rooms.PageRoomCandidates(r.Context(), t, chi.URLParam(r, "roomID"), r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	items := make([]map[string]any, len(page.Candidates))
	for i, c := range page.Candidates {
		items[i] = candidateResponse(c)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"candidates": items, "next_cursor": page.NextCursor, "comment_content": "untrusted_data"})
}

func (a *API) GetRoomCandidate(w http.ResponseWriter, r *http.Request) {
	t, ok := a.readRoom(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	c, err := a.Rooms.GetRoomCandidate(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "candidateID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"candidate": candidateResponse(c), "comment_content": "untrusted_data"})
}

func (a *API) AddProjectRoomMember(w http.ResponseWriter, r *http.Request) {
	t, ok := roomHuman(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if err := a.Rooms.AddProjectRoomMember(r.Context(), t, chi.URLParam(r, "roomID"), req.UserID, req.Role, store.AuditRecord{Ctx: auditCtx(r)}); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (a *API) UpdateProjectRoomMember(w http.ResponseWriter, r *http.Request) {
	t, ok := roomHuman(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if err := a.Rooms.UpdateProjectRoomMemberRole(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "userID"), req.Role, store.AuditRecord{Ctx: auditCtx(r)}); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) RemoveProjectRoomMember(w http.ResponseWriter, r *http.Request) {
	t, ok := roomHuman(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	if err := a.Rooms.RemoveProjectRoomMember(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "userID"), store.AuditRecord{Ctx: auditCtx(r)}); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) AddProjectRoomSite(w http.ResponseWriter, r *http.Request) {
	t, ok := roomHuman(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	var req struct {
		SiteID string `json:"site_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if err := a.Rooms.AddProjectRoomSite(r.Context(), t, chi.URLParam(r, "roomID"), req.SiteID, store.AuditRecord{Ctx: auditCtx(r)}); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (a *API) RemoveProjectRoomSite(w http.ResponseWriter, r *http.Request) {
	t, ok := roomHuman(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	if err := a.Rooms.RemoveProjectRoomSite(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "siteID"), store.AuditRecord{Ctx: auditCtx(r)}); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) ResolveReviewThread(w http.ResponseWriter, r *http.Request) {
	t, ok := roomHuman(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	th, err := a.Rooms.ResolveReviewThread(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "threadID"), store.AuditRecord{Ctx: auditCtx(r)})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"thread": threadHistoryResponse(th), "comment_content": "untrusted_data"})
}

func (a *API) ReanchorReviewThread(w http.ResponseWriter, r *http.Request) {
	t, ok := roomHuman(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	var req struct {
		Kind        string          `json:"kind"`
		Anchor      json.RawMessage `json:"anchor"`
		AnchorState string          `json:"anchor_state"`
		Reason      string          `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	th, err := a.Rooms.ReanchorReviewThread(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "threadID"), req.Kind, req.AnchorState, req.Reason, req.Anchor, store.AuditRecord{Ctx: auditCtx(r)})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"thread": threadHistoryResponse(th), "comment_content": "untrusted_data"})
}

func (a *API) readRoom(w http.ResponseWriter, r *http.Request) (store.Tenant, bool) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return store.Tenant{}, false
	}
	if !allowCommentRead(r) {
		httpx.WriteError(w, fmt.Errorf("%w: reading project rooms requires a human session or MCP OAuth", httpx.ErrForbidden))
		return store.Tenant{}, false
	}
	if !a.requireOrgMember(w, r, t) {
		return store.Tenant{}, false
	}
	return t, true
}

func roomHuman(w http.ResponseWriter, r *http.Request) (store.Tenant, bool) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return store.Tenant{}, false
	}
	if !humanSession(r) {
		httpx.WriteError(w, fmt.Errorf("%w: project room changes require an authenticated human session", httpx.ErrForbidden))
		return store.Tenant{}, false
	}
	return t, true
}

type commentItemResponse struct {
	Source      string          `json:"source"`
	ID          string          `json:"id"`
	CreatedAt   time.Time       `json:"created_at"`
	AuthorID    string          `json:"author_id,omitempty"`
	Body        string          `json:"body,omitempty"`
	RoomID      string          `json:"room_id,omitempty"`
	SiteID      string          `json:"site_id,omitempty"`
	VersionID   string          `json:"version_id,omitempty"`
	ContentHash string          `json:"content_hash,omitempty"`
	Page        string          `json:"page,omitempty"`
	AnchorKind  string          `json:"anchor_kind,omitempty"`
	Anchor      json.RawMessage `json:"anchor,omitempty"`
	AnchorState string          `json:"anchor_state,omitempty"`
	Status      string          `json:"status,omitempty"`
	Untrusted   bool            `json:"untrusted"`
}

func commentItems(in []store.CommentFeedItem) []commentItemResponse {
	out := make([]commentItemResponse, len(in))
	for i, it := range in {
		out[i] = commentItemResponse{
			Source: it.Source, ID: it.ID, CreatedAt: it.CreatedAt, AuthorID: it.AuthorID,
			Body: it.Body, RoomID: it.RoomID, SiteID: it.SiteID, VersionID: it.VersionID,
			ContentHash: it.ContentHash, Page: it.Page, AnchorKind: it.AnchorKind,
			Anchor: it.Anchor, AnchorState: it.AnchorState, Status: it.Status, Untrusted: true,
		}
	}
	return out
}

func replyPageResponse(p store.ReplyPage) map[string]any {
	replies := make([]map[string]any, len(p.Replies))
	for i, rp := range p.Replies {
		replies[i] = map[string]any{"id": rp.ID, "author_id": rp.AuthorID, "body": rp.Body, "created_at": rp.CreatedAt, "untrusted": true}
	}
	revs := make([]map[string]any, len(p.Revisions))
	for i, rv := range p.Revisions {
		revs[i] = map[string]any{
			"id": rv.ID, "anchor_kind": rv.AnchorKind, "anchor": rv.Anchor, "page": rv.Page,
			"anchor_state": rv.AnchorState, "reason": rv.Reason, "created_at": rv.CreatedAt,
		}
	}
	return map[string]any{
		"id": p.ThreadID, "room_id": p.RoomID, "site_id": p.SiteID, "version_id": p.VersionID,
		"content_hash": p.ContentHash, "page": p.Page, "anchor_kind": p.AnchorKind, "anchor": p.Anchor,
		"anchor_state": p.AnchorState, "status": p.Status, "revisions": revs, "replies": replies,
		"next_cursor": p.NextCursor, "untrusted": true,
	}
}

func roomDetailResponse(d store.RoomDetail) map[string]any {
	members := make([]map[string]string, len(d.Members))
	for i, m := range d.Members {
		members[i] = map[string]string{"user_id": m.UserID, "role": m.Role}
	}
	sites := make([]map[string]string, len(d.Sites))
	for i, s := range d.Sites {
		sites[i] = map[string]string{"site_id": s.SiteID, "created_by": s.CreatedBy}
	}
	return map[string]any{
		"room": map[string]any{
			"id": d.ID, "org_id": d.OrgID, "name": d.Name, "owner_user_id": d.OwnerUserID,
			"role": d.Role, "created_at": d.CreatedAt, "updated_at": d.UpdatedAt, "unread": d.Unread,
		},
		"members": members, "sites": sites,
	}
}

func threadHeadResponse(th store.ReviewThread) map[string]any {
	return map[string]any{
		"id": th.ID, "room_id": th.RoomID, "site_id": th.SiteID, "version_id": th.VersionID,
		"content_hash": th.ContentHash, "page": th.Page, "anchor_kind": th.AnchorKind,
		"anchor": th.Anchor, "anchor_state": th.AnchorState, "status": th.Status,
		"created_by": th.CreatedBy, "created_at": th.CreatedAt, "role": th.Role, "untrusted": true,
	}
}

func threadHistoryResponse(th store.ReviewThread) map[string]any {
	body := threadHeadResponse(th)
	revs := make([]map[string]any, len(th.Revisions))
	for i, rv := range th.Revisions {
		revs[i] = map[string]any{
			"id": rv.ID, "anchor_kind": rv.AnchorKind, "anchor": rv.Anchor, "page": rv.Page,
			"anchor_state": rv.AnchorState, "reason": rv.Reason, "created_at": rv.CreatedAt,
		}
	}
	body["revisions"] = revs
	return body
}

func candidateResponse(c store.Candidate) map[string]any {
	return map[string]any{
		"id": c.ID, "room_id": c.RoomID, "site_id": c.SiteID,
		"base_version_id": c.BaseVersionID, "base_content_hash": c.BaseContentHash,
		"content_hash": c.ContentHash, "diff_hash": c.DiffHash, "summary": c.Summary,
		"state": c.State, "created_by": c.CreatedBy, "created_at": c.CreatedAt,
		"approved_by": c.ApprovedBy, "rejected_by": c.RejectedBy, "untrusted": true,
	}
}
