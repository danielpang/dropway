// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"context"
	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type activityRoomStore struct {
	ProjectRoomStore
	read, marked bool
}

func (s *activityRoomStore) PageRoomActivity(_ context.Context, _ store.Tenant, room, cursor string, limit int) (store.ActivityPage, error) {
	s.read = true
	return store.ActivityPage{Items: []store.ReviewActivity{{ID: "event", RoomID: room, Kind: "candidate_submitted"}}, Unread: 1}, nil
}
func (s *activityRoomStore) MarkProjectRoomRead(ctx context.Context, t store.Tenant, room string) (time.Time, error) {
	if !store.HumanSessionFromContext(ctx) {
		return time.Time{}, store.ErrRoomForbidden
	}
	s.marked = true
	return time.Now().UTC(), nil
}
func TestRoomActivityCredentialBoundary(t *testing.T) {
	for _, class := range []string{auth.ClassHumanSession, auth.ClassMCPOAuth, "", "unknown"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(class+method, func(t *testing.T) {
				fs := newFakeStore()
				fs.p2().members["user_1"] = store.RoleMember
				a := NewFull(quota.Unlimited{}, fs, nil, projection.NewLocal())
				rooms := &activityRoomStore{}
				a.Rooms = rooms
				r := chi.NewRouter()
				r.Use(middleware.Auth(classedVerifier{claims: claims("user_1", "org_1", "member"), class: class}))
				r.Get("/rooms/{roomID}/activity", a.ListRoomActivity)
				r.Post("/rooms/{roomID}/read", a.MarkProjectRoomRead)
				path := "/rooms/room/activity"
				if method == http.MethodPost {
					path = "/rooms/room/read"
				}
				req := httptest.NewRequest(method, path, nil)
				req.Header.Set("Authorization", "Bearer test")
				rr := httptest.NewRecorder()
				r.ServeHTTP(rr, req)
				allowed := class == auth.ClassHumanSession || (class == auth.ClassMCPOAuth && method == http.MethodGet)
				want := http.StatusForbidden
				if allowed {
					want = http.StatusOK
				}
				if rr.Code != want {
					t.Fatalf("status %d want %d: %s", rr.Code, want, rr.Body.String())
				}
				if !allowed && (rooms.read || rooms.marked) {
					t.Fatal("denied credential reached store")
				}
				if method == http.MethodGet && rooms.marked {
					t.Fatal("read acknowledged activity")
				}
			})
		}
	}
}
