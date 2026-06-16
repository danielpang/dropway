package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpang/dropway/internal/quota"
)

// TestWriteError_QuotaExceeded402 is the required unit test for the 402 path: a
// quota.ExceededError must render as HTTP 402 with the full ExceededError JSON
// body so the dashboard/CLI can drive the upgrade flow.
func TestWriteError_QuotaExceeded402(t *testing.T) {
	ex := &quota.ExceededError{
		Limit:      quota.ResourceSitePerUser,
		Current:    10,
		Max:        10,
		PlanTier:   "free",
		NextTier:   "business",
		UpgradeURL: "https://app.dropway.dev/billing/upgrade?tier=business",
	}

	rr := httptest.NewRecorder()
	// Wrap it to prove the renderer unwraps via errors.As (AsExceeded).
	WriteError(rr, fmt.Errorf("creating site: %w", ex))

	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusPaymentRequired)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}

	var got quota.ExceededError
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, rr.Body.String())
	}
	if got.Limit != quota.ResourceSitePerUser {
		t.Errorf("limit = %q, want %q", got.Limit, quota.ResourceSitePerUser)
	}
	if got.Current != 10 || got.Max != 10 {
		t.Errorf("current/max = %d/%d, want 10/10", got.Current, got.Max)
	}
	if got.PlanTier != "free" || got.NextTier != "business" {
		t.Errorf("plan/next = %q/%q, want free/business", got.PlanTier, got.NextTier)
	}
	if got.UpgradeURL == "" {
		t.Error("upgrade_url should be present in the 402 body")
	}
}

func TestWriteError_SentinelMappings(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"unauthorized", fmt.Errorf("missing token: %w", ErrUnauthorized), http.StatusUnauthorized, "unauthorized"},
		{"forbidden", fmt.Errorf("not admin: %w", ErrForbidden), http.StatusForbidden, "forbidden"},
		{"not_found", fmt.Errorf("no such site: %w", ErrNotFound), http.StatusNotFound, "not_found"},
		{"conflict", fmt.Errorf("host taken: %w", ErrConflict), http.StatusConflict, "conflict"},
		{"bad_request", fmt.Errorf("bad json: %w", ErrBadRequest), http.StatusBadRequest, "bad_request"},
		{"unknown_500", fmt.Errorf("boom"), http.StatusInternalServerError, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			WriteError(rr, tc.err)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d", rr.Code, tc.want)
			}
			var body ErrorBody
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error != tc.code {
				t.Errorf("error code = %q, want %q", body.Error, tc.code)
			}
		})
	}
}

func TestWriteError_500HidesInternalDetail(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteError(rr, fmt.Errorf("database password is hunter2"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if got := rr.Body.String(); contains(got, "hunter2") {
		t.Errorf("internal detail leaked in 500 body: %s", got)
	}
}

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteJSON(rr, http.StatusCreated, map[string]string{"id": "site_123"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != "site_123" {
		t.Errorf("id = %q", got["id"])
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
