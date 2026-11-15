package handlers

import (
	"context"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
	"net/http"
)

type aiPreferencesStore interface {
	GetAIPreferences(context.Context, store.Tenant, string) (store.AIPreferences, error)
	SaveAIPreferences(context.Context, store.Tenant, string, store.AIPreferences) error
}

func (a *API) AIPreferences(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return
	}
	if !humanSession(r) {
		writeHostedError(w, hosted.ErrDenied)
		return
	}
	if !a.requireStore(w) || !a.requireOrgMember(w, r, t) {
		return
	}
	s, ok := a.Store.(aiPreferencesStore)
	if !ok {
		httpx.WriteJSON(w, 503, httpx.ErrorBody{Error: "unavailable"})
		return
	}
	roomID := chi.URLParam(r, "roomID")
	if r.Method == http.MethodPut {
		var in store.AIPreferences
		if !decodeHosted(w, r, &in, 2048) {
			return
		}
		if err := s.SaveAIPreferences(store.WithHumanSession(r.Context(), true), t, roomID, in); err != nil {
			writeHostedError(w, err)
			return
		}
	}
	out, err := s.GetAIPreferences(r.Context(), t, roomID)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, out)
}
