// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Comment bounds (defensive; the dashboard also limits input).
const (
	maxCommentLen      = 4000
	maxCommentMentions = 50
)

// commentResponse is one row of a site's comment thread.
type commentResponse struct {
	ID               string    `json:"id"`
	SiteID           string    `json:"site_id"`
	AuthorID         string    `json:"author_id"`
	Body             string    `json:"body"`
	MentionedUserIDs []string  `json:"mentioned_user_ids"`
	CreatedAt        time.Time `json:"created_at"`
}

func toCommentResponse(c store.SiteComment) commentResponse {
	mentions := c.MentionedUserIDs
	if mentions == nil {
		mentions = []string{}
	}
	return commentResponse{
		ID:               c.ID,
		SiteID:           c.SiteID,
		AuthorID:         c.AuthorUserID,
		Body:             c.Body,
		MentionedUserIDs: mentions,
		CreatedAt:        c.CreatedAt,
	}
}

type createCommentRequest struct {
	Body string `json:"body"`
	// MentionedUserIDs are the org users tagged in the comment. Ids that aren't
	// current members of the org are dropped server-side.
	MentionedUserIDs []string `json:"mentioned_user_ids,omitempty"`
}

// ListComments returns a site's comment thread, oldest first. Any org member may
// read it (org-scoped by RLS). 404 for an absent/other-tenant site.
func (a *API) ListComments(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	// Resolve the site first so an absent/other-tenant site is a clean 404 (rather
	// than an empty thread that hides the difference).
	if _, err := a.Store.GetSite(r.Context(), t, siteID); err != nil {
		writeStoreError(w, err)
		return
	}

	comments, err := a.Store.ListSiteComments(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]commentResponse, len(comments))
	for i, c := range comments {
		out[i] = toCommentResponse(c)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"comments": out})
}

// AddComment posts a comment to a site, optionally tagging teammates. Any org
// member may comment (the discussion is org-internal; RLS keeps it scoped). The
// author is the authenticated caller. Mentioned ids that aren't current org
// members are dropped so a tag always points at a real teammate.
func (a *API) AddComment(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	if _, err := a.Store.GetSite(r.Context(), t, siteID); err != nil {
		writeStoreError(w, err)
		return
	}

	var req createCommentRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		httpx.WriteError(w, fmt.Errorf("%w: comment body is required", httpx.ErrBadRequest))
		return
	}
	if len(body) > maxCommentLen {
		httpx.WriteError(w, fmt.Errorf("%w: comment must be at most %d characters", httpx.ErrBadRequest, maxCommentLen))
		return
	}

	mentions, err := a.validMentions(r, t, req.MentionedUserIDs)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	comment, err := a.Store.CreateSiteComment(r.Context(), t, store.CreateSiteCommentParams{
		SiteID:           siteID,
		Body:             body,
		MentionedUserIDs: mentions,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}

	logger(r).Info("site comment added",
		"site_id", siteID, "org_id", t.OrgID, "mentions", len(mentions))
	httpx.WriteJSON(w, http.StatusCreated, toCommentResponse(comment))
}

// validMentions filters the requested mention ids down to DISTINCT users who are
// current members of the active org (so a tag always resolves to a real teammate),
// capped at maxCommentMentions. Order is preserved as sent.
func (a *API) validMentions(r *http.Request, t store.Tenant, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return []string{}, nil
	}
	members, err := a.Store.ListMembers(r.Context(), t.OrgID)
	if err != nil {
		// A self-host without the Better Auth member table can't validate mentions;
		// fail safe by dropping them rather than erroring the whole comment.
		if err.Error() == store.ErrAuthSchemaUnavailable.Error() {
			return []string{}, nil
		}
		return nil, err
	}
	isMember := make(map[string]struct{}, len(members))
	for _, m := range members {
		isMember[m.UserID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(requested))
	out := make([]string, 0, len(requested))
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		if _, ok := isMember[id]; !ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= maxCommentMentions {
			break
		}
	}
	return out, nil
}
