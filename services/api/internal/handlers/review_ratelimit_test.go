// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/middleware"
)

func TestReviewRateLimitUsesAuthenticatedUserAcrossPaths(t *testing.T) {
	limit := reviewReadLimiter(newRateLimiter(1, 1, time.Minute))
	request := func(user, org, method, path string) *httptest.ResponseRecorder {
		h := middleware.Auth(classedVerifier{claims: claims(user, org, "member"), class: auth.ClassMCPOAuth})(
			limit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer test")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr
	}
	if got := request("user_1", "org_1", "GET", "/sites/a/comment-pages"); got.Code != 204 {
		t.Fatal(got.Code)
	}
	if got := request("user_1", "org_1", "GET", "/project-rooms/b/activity"); got.Code != 429 || got.Header().Get("Retry-After") == "" {
		t.Fatal(got.Code, got.Header())
	}
	if got := request("user_2", "org_1", "GET", "/project-rooms/b/activity"); got.Code != 204 {
		t.Fatal("another user's budget was consumed")
	}
	if got := request("user_1", "org_2", "GET", "/project-rooms/b/activity"); got.Code != 204 {
		t.Fatal("another org's budget was consumed")
	}
	if got := request("user_1", "org_1", "POST", "/project-rooms/b/read"); got.Code != 204 {
		t.Fatal("read limiter blocked a mutation")
	}
}
