package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInspectProjectInvitationOperationalFailure(t *testing.T) {
	// A closed pool fails without contacting a database.
	pool, err := pgxpool.New(context.Background(), "postgres://test:test@localhost/test")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	api := &API{Rooms: store.New(pool, nil)}
	for _, tc := range []struct {
		token  string
		status int
	}{
		{strings.Repeat("A", 43), 500},
		{"bad", 403},
	} {
		req := httptest.NewRequest("POST", "/v1/project-invitations/inspect", strings.NewReader(`{"token":"`+tc.token+`"}`))
		rec := httptest.NewRecorder()
		api.InspectProjectInvitation(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), tc.token) || strings.Contains(rec.Body.String(), "pool") {
			t.Fatalf("lookup exposed internal details: %s", rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("lookup must not be cached")
		}
	}
}
