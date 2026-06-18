// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpang/dropway/internal/quota"
)

// CreateSite's access_mode handling (the default-visibility fix): omit → the org
// default (org_only via the fake); explicit public/org_only pass through;
// password/allowlist are rejected at create (they need a follow-up access config);
// garbage is a 400.
func TestCreateSite_AccessMode(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode int
		wantMode string // checked only on 201
	}{
		{"omitted → org default", `{"slug":"a"}`, http.StatusCreated, "org_only"},
		{"explicit org_only", `{"slug":"b","access_mode":"org_only"}`, http.StatusCreated, "org_only"},
		{"explicit public", `{"slug":"c","access_mode":"public"}`, http.StatusCreated, "public"},
		{"password rejected at create", `{"slug":"d","access_mode":"password"}`, http.StatusBadRequest, ""},
		{"allowlist rejected at create", `{"slug":"e","access_mode":"allowlist"}`, http.StatusBadRequest, ""},
		{"garbage → 400", `{"slug":"f","access_mode":"nonsense"}`, http.StatusBadRequest, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewFull(quota.Unlimited{}, newFakeStore(), nil, nil)
			h := authed(a.CreateSite, claims("user_1", "org_1", "member"))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, jsonReq(http.MethodPost, "/v1/sites", c.body))

			if rr.Code != c.wantCode {
				t.Fatalf("status = %d, want %d (body=%s)", rr.Code, c.wantCode, rr.Body.String())
			}
			if c.wantCode == http.StatusCreated {
				var resp struct {
					AccessMode string `json:"access_mode"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatal(err)
				}
				if resp.AccessMode != c.wantMode {
					t.Errorf("access_mode = %q, want %q", resp.AccessMode, c.wantMode)
				}
			}
		})
	}
}
