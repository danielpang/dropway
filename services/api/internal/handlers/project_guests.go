// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (a *API) guestStore(w http.ResponseWriter, r *http.Request) (*store.Store, string, bool) {
	c, ok := middleware.ClaimsFromContext(r.Context())
	if !ok || c == nil || !humanSession(r) {
		httpx.WriteError(w, wrapUnauthorized())
		return nil, "", false
	}
	if _, err := uuid.Parse(c.UserID()); err != nil {
		httpx.WriteError(w, wrapUnauthorized())
		return nil, "", false
	}
	for _, name := range []string{"roomID", "siteID", "versionID", "threadID", "shareID"} {
		if v := chi.URLParam(r, name); v != "" {
			if _, err := uuid.Parse(v); err != nil {
				httpx.WriteError(w, fmt.Errorf("%w: invalid identifier", httpx.ErrBadRequest))
				return nil, "", false
			}
		}
	}
	st, ok := a.Rooms.(*store.Store)
	if !ok || st == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, httpx.ErrorBody{Error: "unavailable", Message: "project sharing unavailable"})
		return nil, "", false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	return st, c.UserID(), true
}
func (a *API) ListProjectShares(w http.ResponseWriter, r *http.Request) {
	st, _, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	out, err := st.ListProjectShares(sessionCtx(r), t, chi.URLParam(r, "roomID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"shares": out})
}
func (a *API) InviteProjectGuest(w http.ResponseWriter, r *http.Request) {
	st, _, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
		Days  int    `json:"expires_in_days"`
	}
	if !decodeHosted(w, r, &body, 4096) {
		return
	}
	share, token, err := st.InviteProjectGuest(sessionCtx(r), t, chi.URLParam(r, "roomID"), body.Email, body.Role, body.Days)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"share": share, "token": token})
}
func (a *API) RevokeProjectGuest(w http.ResponseWriter, r *http.Request) {
	st, _, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if err := st.RevokeProjectGuest(sessionCtx(r), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "shareID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) ListGuestProjects(w http.ResponseWriter, r *http.Request) {
	st, user, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	out, err := st.ListGuestProjects(sessionCtx(r), user)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"projects": out})
}
func (a *API) AcceptGuestProject(w http.ResponseWriter, r *http.Request) {
	st, user, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeHosted(w, r, &body, 1024) {
		return
	}
	if len(body.Token) != 43 {
		writeStoreError(w, store.ErrRoomForbidden)
		return
	}
	out, err := st.GuestProject(sessionCtx(r), user, "", body.Token)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"project": out})
}
func (a *API) GetGuestProject(w http.ResponseWriter, r *http.Request) {
	st, user, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	out, err := st.GuestProject(sessionCtx(r), user, chi.URLParam(r, "roomID"), "")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"project": out})
}
func (a *API) GuestThreads(w http.ResponseWriter, r *http.Request) {
	st, user, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	out, err := st.GuestThreads(sessionCtx(r), user, chi.URLParam(r, "roomID"), r.URL.Query().Get("site_id"), chi.URLParam(r, "threadID"), r.URL.Query().Get("cursor"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if chi.URLParam(r, "threadID") != "" {
		httpx.WriteJSON(w, 200, map[string]any{"thread": out.Threads[0]})
	} else {
		httpx.WriteJSON(w, 200, out)
	}
}
func (a *API) GuestCreateThread(w http.ResponseWriter, r *http.Request) {
	st, user, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	var b struct {
		SiteID      string          `json:"site_id"`
		VersionID   string          `json:"version_id"`
		ContentHash string          `json:"content_hash"`
		Page        string          `json:"page"`
		AnchorKind  string          `json:"anchor_kind"`
		Anchor      json.RawMessage `json:"anchor"`
		Body        string          `json:"body"`
	}
	if !decodeHosted(w, r, &b, 16384) {
		return
	}
	out, err := st.GuestCreateThread(sessionCtx(r), user, chi.URLParam(r, "roomID"), b.SiteID, b.VersionID, b.ContentHash, b.Page, b.AnchorKind, b.Anchor, b.Body)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"thread": out})
}
func (a *API) GuestAddReply(w http.ResponseWriter, r *http.Request) {
	st, user, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	var b struct {
		Body string `json:"body"`
	}
	if !decodeHosted(w, r, &b, 8192) {
		return
	}
	out, err := st.GuestAddReply(sessionCtx(r), user, chi.URLParam(r, "roomID"), chi.URLParam(r, "threadID"), b.Body)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"reply": out})
}
func (a *API) GuestSignoff(w http.ResponseWriter, r *http.Request) {
	st, user, ok := a.guestStore(w, r)
	if !ok {
		return
	}
	var b struct {
		SiteID      string `json:"site_id"`
		VersionID   string `json:"version_id"`
		ContentHash string `json:"content_hash"`
	}
	if !decodeHosted(w, r, &b, 2048) {
		return
	}
	if err := st.GuestSignoff(sessionCtx(r), user, chi.URLParam(r, "roomID"), b.SiteID, b.VersionID, b.ContentHash); err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"version_id": b.VersionID, "content_hash": b.ContentHash, "signed_off": true})
}
func (a *API) GuestReadFile(w http.ResponseWriter, r *http.Request) {
	st, user, ok := a.guestStore(w, r)
	if !ok || !a.requireObjects(w) {
		return
	}
	room, site, version := chi.URLParam(r, "roomID"), chi.URLParam(r, "siteID"), chi.URLParam(r, "versionID")
	ver, err := st.GuestVersion(sessionCtx(r), user, room, site, version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.ContainsAny(p, "\\\x00") {
		httpx.WriteError(w, fmt.Errorf("%w: invalid artifact path", httpx.ErrBadRequest))
		return
	}
	files, err := a.verifiedManifest(r.Context(), ver.OrgID, site, version, ver.ContentHash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	target, ok := files[p]
	if !ok {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	if target.Size < 0 || target.Size > maxReviewFileBytes {
		writeReviewTooLarge(w)
		return
	}
	rc, err := a.Objects.GetBlob(r.Context(), ver.OrgID, target.SHA256)
	if err != nil {
		writeStoreError(w, store.ErrCandidateArtifact)
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxReviewFileBytes+1))
	sum := sha256.Sum256(data)
	if err != nil || int64(len(data)) != target.Size || hex.EncodeToString(sum[:]) != target.SHA256 {
		writeStoreError(w, store.ErrCandidateArtifact)
		return
	}
	if !guestPreviewAllowed(p, data) {
		writeStoreError(w, store.ErrRoomForbidden)
		return
	}
	if _, err := st.GuestVersion(sessionCtx(r), user, room, site, version); err != nil {
		writeStoreError(w, err)
		return
	}
	mime := http.DetectContentType(data)
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm", ".xhtml":
		mime = "text/html"
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	httpx.WriteJSON(w, 200, map[string]any{"site_id": site, "version_id": version, "content_hash": ver.ContentHash, "path": p, "content_base64": base64.StdEncoding.EncodeToString(data), "content_type": mime, "sha256": target.SHA256})
}

// GuestAccessLimiter keys verified user identity alone: org-less sessions and
// switching JWT org claims cannot bypass the bucket.
func GuestAccessLimiter() func(http.Handler) http.Handler {
	reads, writes := newRateLimiter(120, 30, 10*time.Minute), newRateLimiter(30, 10, 10*time.Minute)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := middleware.ClaimsFromContext(r.Context())
			if !ok || c == nil || !humanSession(r) {
				httpx.WriteError(w, wrapUnauthorized())
				return
			}
			limit := reads
			if r.Method != http.MethodGet {
				limit = writes
			}
			if allowed, retry := limit.allow(c.UserID()); !allowed {
				httpx.WriteRateLimited(w, retry, "rate_limited", "too many project sharing requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func guestPreviewAllowed(p string, data []byte) bool {
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	ext := strings.ToLower(path.Ext(p))
	mime := http.DetectContentType(data)
	switch ext {
	case ".html", ".htm", ".xhtml":
		return utf8.Valid(data) && !strings.ContainsRune(string(data), 0) && (strings.HasPrefix(mime, "text/") || strings.HasPrefix(mime, "application/xhtml+xml"))
	case ".png":
		return mime == "image/png"
	case ".jpg", ".jpeg":
		return mime == "image/jpeg"
	case ".gif":
		return mime == "image/gif"
	case ".webp":
		return mime == "image/webp"
	default:
		return false
	}
}
