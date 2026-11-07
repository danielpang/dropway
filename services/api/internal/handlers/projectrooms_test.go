// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/quota"
)

func TestAPIKeyCannotApproveChange(t *testing.T) {
	a := New(quota.Unlimited{})
	c := claims("user_1", "org_1", "owner")
	r := chi.NewRouter()
	r.Use(middleware.AuthWithKeys(fakeVerifier{claims: c}, keyAuthFunc(func() *middleware.KeyPrincipal {
		return &middleware.KeyPrincipal{Claims: c, KeyID: "key_1", Scopes: []string{"sites:*"}}
	})))
	r.Post("/v1/project-rooms/{roomID}/candidates/{candidateID}/approve", a.ApproveCandidate)
	req := httptest.NewRequest(http.MethodPost, "/v1/project-rooms/room/candidates/cand/approve", strings.NewReader(`{"content_hash":"aa","base_version_id":"v","base_content_hash":"bb"}`))
	req.Header.Set("Authorization", "Bearer dw_live_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body %s", rr.Code, rr.Body.String())
	}
}
