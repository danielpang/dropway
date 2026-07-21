// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/internal/quota"
)

// spyEmitter records the analytics events captured during a test.
type spyEmitter struct{ events []analytics.Event }

func (s *spyEmitter) Capture(_ context.Context, ev analytics.Event) {
	s.events = append(s.events, ev)
}

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

// CreateSite rejects malformed slugs with a 400 BEFORE they can reach the
// canonical content host or the Cloudflare KV route key (H1). The CLI and MCP
// hit this handler directly (only the dashboard slugifies client-side), so this
// server-side check is the real boundary.
func TestCreateSite_RejectsMalformedSlug(t *testing.T) {
	bad := []string{
		`{"slug":"a/b"}`,                   // path separator → KV-key path injection
		`{"slug":"a.b"}`,                   // dot → extra DNS label
		`{"slug":"a%2e"}`,                  // percent → KV-key escaping
		`{"slug":"a#x"}`,                   // fragment
		`{"slug":"Acme"}`,                  // uppercase
		`{"slug":"victimorg--victimsite"}`, // `--` reserved: legacy hosts redirect on it
		`{"slug":"-lead"}`,                 // leading hyphen
		`{"slug":"trail-"}`,                // trailing hyphen
	}
	for _, body := range bad {
		t.Run(body, func(t *testing.T) {
			a := NewFull(quota.Unlimited{}, newFakeStore(), nil, nil)
			h := authed(a.CreateSite, claims("user_1", "org_1", "member"))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, jsonReq(http.MethodPost, "/v1/sites", body))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestCreateSite_EmitsSiteCreated asserts a successful create emits a
// `site_created` product-analytics event carrying org_id (property + group) so
// the "new sites created" dashboard can roll up per org. Best-effort: the emit
// never affects the response.
func TestCreateSite_EmitsSiteCreated(t *testing.T) {
	spy := &spyEmitter{}
	a := NewFull(quota.Unlimited{}, newFakeStore(), nil, nil)
	a.Analytics = spy
	h := authed(a.CreateSite, claims("user_1", "org_1", "member"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, jsonReq(http.MethodPost, "/v1/sites", `{"slug":"rocket"}`))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rr.Code, rr.Body.String())
	}
	if len(spy.events) != 1 {
		t.Fatalf("expected 1 analytics event, got %d", len(spy.events))
	}
	ev := spy.events[0]
	if ev.Event != "site_created" {
		t.Errorf("event = %q, want site_created", ev.Event)
	}
	if ev.DistinctID != "user_1" {
		t.Errorf("distinct_id = %q, want user_1 (the acting user)", ev.DistinctID)
	}
	if ev.Properties["org_id"] != "org_1" {
		t.Errorf("org_id property = %v, want org_1", ev.Properties["org_id"])
	}
	if ev.Properties["slug"] != "rocket" {
		t.Errorf("slug property = %v, want rocket", ev.Properties["slug"])
	}
	if ev.Groups["organization"] != "org_1" {
		t.Errorf("organization group = %v, want org_1", ev.Groups["organization"])
	}
}

// TestCreateSite_NoEmitOnFailure asserts a rejected create emits nothing (the
// event fires only on a real, successful creation).
func TestCreateSite_NoEmitOnFailure(t *testing.T) {
	spy := &spyEmitter{}
	a := NewFull(quota.Unlimited{}, newFakeStore(), nil, nil)
	a.Analytics = spy
	h := authed(a.CreateSite, claims("user_1", "org_1", "member"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, jsonReq(http.MethodPost, "/v1/sites", `{"slug":"Bad/Slug"}`))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	if len(spy.events) != 0 {
		t.Fatalf("expected no analytics events on a rejected create, got %d", len(spy.events))
	}
}
