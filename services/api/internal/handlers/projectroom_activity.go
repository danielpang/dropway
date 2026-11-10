// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (a *API) ListRoomActivity(w http.ResponseWriter, r *http.Request) {
	t, ok := a.readRoom(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	page, err := a.Rooms.PageRoomActivity(r.Context(), t, chi.URLParam(r, "roomID"), r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeReviewJSON(w, page)
}

func (a *API) MarkProjectRoomRead(w http.ResponseWriter, r *http.Request) {
	t, ok := roomHuman(w, r)
	if !ok || !a.requireRooms(w) {
		return
	}
	if !a.requireOrgMember(w, r, t) {
		return
	}
	at, err := a.Rooms.MarkProjectRoomRead(sessionCtx(r), t, chi.URLParam(r, "roomID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"last_read_at": at})
}
