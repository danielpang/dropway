// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Feed-metadata length bounds (defensive caps; the dashboard also limits input).
const (
	maxFeedTitleLen       = 120
	maxFeedDescriptionLen = 500
)

// ---------------------------------------------------------------------------
// GET /v1/feed — the org feed (sites + skills)
// ---------------------------------------------------------------------------

// feedPostResponse is one post in the unified org feed. Kind ("site" | "skill")
// tags what the post is so the dashboard can render the right card, link, and
// badges; the site-only fields (access_mode, live_url, storage_bytes) and
// skill-only fields (is_seeded, size_bytes) are omitted for the other kind. Every
// post carries the shared social metadata (score, the caller's vote, comment
// count) so the feed renders in one round-trip.
type feedPostResponse struct {
	Kind             string    `json:"kind"`
	ID               string    `json:"id"`
	OrgID            string    `json:"org_id"`
	Slug             string    `json:"slug"`
	OwnerID          string    `json:"owner_id"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	CurrentVersionID *string   `json:"current_version_id,omitempty"`
	FeedVisible      bool      `json:"feed_visible"`
	CreatedAt        time.Time `json:"created_at"`

	// Site-only.
	AccessMode   string `json:"access_mode,omitempty"`
	LiveURL      string `json:"live_url,omitempty"`
	StorageBytes int64  `json:"storage_bytes,omitempty"`

	// Skill-only.
	IsSeeded  bool  `json:"is_seeded,omitempty"`
	SizeBytes int64 `json:"size_bytes,omitempty"`

	// Social (both kinds).
	Score        int64 `json:"score"`
	MyVote       int   `json:"my_vote"`
	CommentCount int64 `json:"comment_count"`
}

// ListFeed returns the active org's unified feed: every site AND skill any member
// has shared (not marked private), newest first, so freshly created/published
// posts sit at the top. Any org member may read it (RLS scopes the rows to their
// org). A site or skill joins the feed automatically on create / publish and
// leaves it only when its owner (or an admin) makes it private.
//
// Each item carries its title/description, its net vote score + the caller's own
// vote, and its comment count, plus a `kind` tag so the client renders sites and
// skills distinctly.
func (a *API) ListFeed(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	// Skills lazily seed on first touch, so the feed shows the preset starter set
	// too (same as the skills page).
	a.ensureSkillsSeeded(r, t)

	sites, err := a.Store.ListFeedSites(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	orgSlug, err := a.Store.OrgSlug(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Logical storage per site (one query for the whole org), keyed by site id so
	// each site item carries its size without an N+1 — same approach as ListSites.
	storage, err := a.Store.ListSiteStorage(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	bytesBySite := make(map[string]int64, len(storage))
	for _, s := range storage {
		bytesBySite[s.SiteID] = s.Bytes
	}

	skills, err := a.Store.ListFeedSkills(r.Context(), t)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	out := make([]feedPostResponse, 0, len(sites)+len(skills))
	for _, s := range sites {
		sr := a.toSiteResponse(s.Site, orgSlug, bytesBySite[s.ID])
		out = append(out, feedPostResponse{
			Kind:             "site",
			ID:               sr.ID,
			OrgID:            sr.OrgID,
			Slug:             sr.Slug,
			OwnerID:          sr.OwnerID,
			Title:            sr.Title,
			Description:      sr.Description,
			CurrentVersionID: sr.CurrentVersionID,
			FeedVisible:      sr.FeedVisible,
			CreatedAt:        sr.CreatedAt,
			AccessMode:       sr.AccessMode,
			LiveURL:          sr.LiveURL,
			StorageBytes:     sr.StorageBytes,
			Score:            s.Score,
			MyVote:           s.MyVote,
			CommentCount:     s.CommentCount,
		})
	}
	for _, s := range skills {
		out = append(out, feedPostResponse{
			Kind:             "skill",
			ID:               s.ID,
			OrgID:            s.OrgID,
			Slug:             s.Slug,
			OwnerID:          s.OwnerUserID,
			Title:            s.Title,
			Description:      s.Description,
			CurrentVersionID: s.CurrentVersionID,
			FeedVisible:      s.FeedVisible,
			CreatedAt:        s.CreatedAt,
			IsSeeded:         s.OwnerUserID == store.SeedOwnerUserID,
			SizeBytes:        s.SizeBytes,
			Score:            s.Score,
			MyVote:           s.MyVote,
			CommentCount:     s.CommentCount,
		})
	}
	// Merge the two newest-first lists into one newest-first list (stable so a tie
	// keeps sites-before-skills ordering deterministic).
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"posts": out})
}

// ---------------------------------------------------------------------------
// PUT /v1/sites/{id}/vote  and  PUT /v1/skills/{id}/vote  {value}
// ---------------------------------------------------------------------------

type setVoteRequest struct {
	// Value is +1 (upvote), -1 (downvote), or 0 (clear the caller's vote).
	Value int `json:"value"`
}

// SetSiteVote records the caller's up/down vote on a site feed post (or clears
// it). Any org member may vote (RLS scopes the vote to their org). Voting is a
// FEED interaction, so a site pulled from the feed accepts no votes.
func (a *API) SetSiteVote(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	site, err := a.Store.GetSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !site.FeedVisible {
		httpx.WriteError(w, fmt.Errorf("%w: this site is not on the org feed", httpx.ErrNotFound))
		return
	}
	a.setPostVote(w, r, t, store.SubjectSite, siteID)
}

// SetSkillVote records the caller's up/down vote on a skill feed post (or clears
// it). Mirror of SetSiteVote: any org member may vote; a skill made private
// accepts no votes.
func (a *API) SetSkillVote(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	skillID := chi.URLParam(r, "id")

	skill, err := a.Store.GetSkill(r.Context(), t, skillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !skill.FeedVisible {
		httpx.WriteError(w, fmt.Errorf("%w: this skill is not on the org feed", httpx.ErrNotFound))
		return
	}
	a.setPostVote(w, r, t, store.SubjectSkill, skillID)
}

// setPostVote is the shared vote body: decode + validate the value, upsert the
// caller's vote on the (kind, id) subject, and return the new net score + vote.
func (a *API) setPostVote(w http.ResponseWriter, r *http.Request, t store.Tenant, subjectType, subjectID string) {
	var req setVoteRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	if req.Value < -1 || req.Value > 1 {
		httpx.WriteError(w, fmt.Errorf("%w: value must be -1, 0, or 1", httpx.ErrBadRequest))
		return
	}

	score, myVote, err := a.Store.SetPostVote(r.Context(), t, subjectType, subjectID, req.Value)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":    subjectType,
		"id":      subjectID,
		"score":   score,
		"my_vote": myVote,
	})
}

// ---------------------------------------------------------------------------
// PUT /v1/sites/{id}/feed  and  PUT /v1/skills/{id}/feed  {visible}
// ---------------------------------------------------------------------------

type setFeedVisibilityRequest struct {
	// Visible shares the post to the org feed (true) or makes it private (false).
	Visible bool `json:"visible"`
}

// SetSiteFeedVisibility shares a site to the org feed or makes it private. A
// site's OWNER may toggle their own site; admins/owners may toggle any. Feed
// visibility is orthogonal to access_mode, so this changes nothing at the edge.
func (a *API) SetSiteFeedVisibility(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	site, err := a.Store.GetSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Owner-or-admin, with the owner's membership re-checked live (an ex-member
	// must not manage their old site on a stale JWT).
	if site.OwnerUserID == t.UserID {
		if !a.requireOrgMember(w, r, t) {
			return
		}
	} else if !a.requireAdmin(w, r, t) {
		return
	}

	var req setFeedVisibilityRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}

	updated, err := a.Store.SetSiteFeedVisible(r.Context(), t, siteID, req.Visible)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	logger(r).Info("site feed visibility changed",
		"site_id", siteID, "visible", updated.FeedVisible, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSiteFeedVisibility, "site:"+siteID, map[string]any{
		"visible": updated.FeedVisible,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":         "site",
		"id":           siteID,
		"feed_visible": updated.FeedVisible,
	})
}

// SetSkillFeedVisibility shares a skill to the org feed or makes it private.
// Owner-or-admin (the same gate skill deletes use). Mirror of the site toggle.
func (a *API) SetSkillFeedVisibility(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	skillID := chi.URLParam(r, "id")

	skill, err := a.Store.GetSkill(r.Context(), t, skillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireSkillOwnerOrAdmin(w, r, t, skill) {
		return
	}

	var req setFeedVisibilityRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}

	updated, err := a.Store.SetSkillFeedVisible(r.Context(), t, skillID, req.Visible)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	logger(r).Info("skill feed visibility changed",
		"skill_id", skillID, "visible", updated.FeedVisible, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSkillFeedVisibility, "skill:"+skillID, map[string]any{
		"visible": updated.FeedVisible,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":         "skill",
		"id":           skillID,
		"feed_visible": updated.FeedVisible,
	})
}

// ---------------------------------------------------------------------------
// PUT /v1/sites/{id}/feed-meta  and  PUT /v1/skills/{id}/feed-meta  {title, description}
// ---------------------------------------------------------------------------

type setFeedMetaRequest struct {
	// Title / Description are the human feed metadata. Empty clears the field.
	Title       string `json:"title"`
	Description string `json:"description"`
}

// validateFeedMeta trims + bounds-checks a feed-meta request. On a bad length it
// writes the 400 and returns ok=false.
func (a *API) validateFeedMeta(w http.ResponseWriter, r *http.Request) (title, description string, ok bool) {
	var req setFeedMetaRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return "", "", false
	}
	title = strings.TrimSpace(req.Title)
	description = strings.TrimSpace(req.Description)
	if len(title) > maxFeedTitleLen {
		httpx.WriteError(w, fmt.Errorf("%w: title must be at most %d characters", httpx.ErrBadRequest, maxFeedTitleLen))
		return "", "", false
	}
	if len(description) > maxFeedDescriptionLen {
		httpx.WriteError(w, fmt.Errorf("%w: description must be at most %d characters", httpx.ErrBadRequest, maxFeedDescriptionLen))
		return "", "", false
	}
	return title, description, true
}

// SetSiteFeedMeta sets the owner-facing Title + Description a site shows in the
// org feed. Owner or org admin/owner. Empty strings clear the field (NULL).
func (a *API) SetSiteFeedMeta(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	siteID := chi.URLParam(r, "id")

	site, err := a.Store.GetSite(r.Context(), t, siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if site.OwnerUserID == t.UserID {
		if !a.requireOrgMember(w, r, t) {
			return
		}
	} else if !a.requireAdmin(w, r, t) {
		return
	}

	title, description, ok := a.validateFeedMeta(w, r)
	if !ok {
		return
	}

	updated, err := a.Store.SetSiteFeedMeta(r.Context(), t, siteID, title, description)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	logger(r).Info("site feed metadata changed", "site_id", siteID, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSiteFeedMeta, "site:"+siteID, map[string]any{
		"title_set":       updated.Title != "",
		"description_set": updated.Description != "",
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":        "site",
		"id":          siteID,
		"title":       updated.Title,
		"description": updated.Description,
	})
}

// SetSkillFeedMeta sets a skill's Title + Description (which double as its feed
// post's title/description). Owner-or-admin. Empty strings clear the field.
func (a *API) SetSkillFeedMeta(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !a.requireStore(w) {
		return
	}
	skillID := chi.URLParam(r, "id")

	skill, err := a.Store.GetSkill(r.Context(), t, skillID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !a.requireSkillOwnerOrAdmin(w, r, t, skill) {
		return
	}

	title, description, ok := a.validateFeedMeta(w, r)
	if !ok {
		return
	}

	updated, err := a.Store.SetSkillMeta(r.Context(), t, skillID, title, description)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	logger(r).Info("skill feed metadata changed", "skill_id", skillID, "org_id", t.OrgID)
	a.recordAudit(r, t, audit.ActionSkillFeedMeta, "skill:"+skillID, map[string]any{
		"title_set":       updated.Title != "",
		"description_set": updated.Description != "",
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":        "skill",
		"id":          skillID,
		"title":       updated.Title,
		"description": updated.Description,
	})
}
