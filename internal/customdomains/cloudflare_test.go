package customdomains

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCloudflareProvider_CreateAndStatus drives the real REST impl against an
// httptest stand-in for the Cloudflare API (no network), asserting the request
// shape and the response → state-machine mapping.
func TestCloudflareProvider_CreateAndStatus(t *testing.T) {
	var createBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/custom_hostnames"):
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"id":     "ch_123",
					"status": "pending",
					"ssl":    map[string]any{"status": "pending_validation"},
					"ownership_verification": map[string]any{
						"name": "_cf.docs.acme.com", "type": "txt", "value": "abc123",
					},
				},
			})
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"id":     "ch_123",
					"status": "active",
					"ssl":    map[string]any{"status": "active"},
				},
			})
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	p := NewCloudflareProvider("zone1", "token1", "dropwaycontent.com")
	p.BaseURL = srv.URL

	created, err := p.CreateCustomHostname(context.Background(), "docs.acme.com")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "ch_123" {
		t.Fatalf("id = %q", created.ID)
	}
	if created.DCV.Name != "_cf.docs.acme.com" || created.DCV.Value != "abc123" {
		t.Fatalf("dcv = %+v", created.DCV)
	}
	if createBody["hostname"] != "docs.acme.com" {
		t.Fatalf("create body hostname = %v", createBody["hostname"])
	}
	if createBody["custom_origin_server"] != "dropwaycontent.com" {
		t.Fatalf("create body origin = %v", createBody["custom_origin_server"])
	}

	st, err := p.Status(context.Background(), "ch_123")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateActive || !st.TLSIssued {
		t.Fatalf("status = %+v", st)
	}
}

func TestCloudflareProvider_ErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  []map[string]any{{"code": 1234, "message": "bad"}},
		})
	}))
	defer srv.Close()
	p := NewCloudflareProvider("z", "t", "o")
	p.BaseURL = srv.URL
	if _, err := p.CreateCustomHostname(context.Background(), "x.com"); err == nil {
		t.Fatal("expected error on !success envelope")
	}
}
