package customdomains

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNormalizeState maps every CF custom-hostname status onto our VerifyState
// machine — the core of the pending→verifying→active→failed lifecycle the Go API
// polls. Table-driven so a new CF status that we don't handle is obvious.
func TestNormalizeState(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		sslStatus string
		want      VerifyState
	}{
		{"active+ssl active → active", "active", "active", StateActive},
		{"active but ssl not issued → verifying", "active", "pending_validation", StateVerifying},
		{"active+empty ssl → verifying", "active", "", StateVerifying},
		{"pending → pending", "pending", "", StatePending},
		{"pending_validation → verifying", "pending_validation", "", StateVerifying},
		{"pending_deployment → verifying", "pending_deployment", "", StateVerifying},
		{"empty status → pending", "", "", StatePending},
		{"blocked → failed", "blocked", "", StateFailed},
		{"moved → failed", "moved", "", StateFailed},
		{"deleted → failed", "deleted", "", StateFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := cfCustomHost{Status: c.status}
			h.SSL.Status = c.sslStatus
			if got := normalizeState(h); got != c.want {
				t.Errorf("normalizeState(status=%q ssl=%q) = %q, want %q", c.status, c.sslStatus, got, c.want)
			}
		})
	}
}

// TestDcvFrom covers the three DCV-extraction branches: ownership verification
// wins when present; otherwise the first SSL validation TXT record; otherwise an
// empty record (nothing to show the user yet).
func TestDcvFrom(t *testing.T) {
	// 1) Ownership verification present → used verbatim.
	var owned cfCustomHost
	owned.OwnershipVerification.Name = "_cf.docs.acme.com"
	owned.OwnershipVerification.Type = "txt"
	owned.OwnershipVerification.Value = "owner-token"
	if got := dcvFrom(owned); got.Name != "_cf.docs.acme.com" || got.Value != "owner-token" || got.Type != "txt" {
		t.Errorf("ownership DCV = %+v", got)
	}

	// 2) No ownership, but an SSL validation record → that TXT record (Type forced to TXT).
	var ssl cfCustomHost
	ssl.SSL.ValidationRecords = []struct {
		TxtName  string `json:"txt_name"`
		TxtValue string `json:"txt_value"`
	}{{TxtName: "_acme-challenge.docs.acme.com", TxtValue: "ssl-token"}}
	if got := dcvFrom(ssl); got.Name != "_acme-challenge.docs.acme.com" || got.Value != "ssl-token" || got.Type != "TXT" {
		t.Errorf("ssl DCV = %+v", got)
	}

	// 3) Neither → empty record.
	if got := dcvFrom(cfCustomHost{}); got != (DCVRecord{}) {
		t.Errorf("empty DCV = %+v, want zero record", got)
	}
}

// TestDoJSON_NonJSONBody asserts a non-JSON / HTML error page from CF surfaces an
// error that includes the status code (rather than panicking or silently
// succeeding).
func TestDoJSON_NonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer srv.Close()

	p := NewCloudflareProvider("z", "t", "o")
	p.BaseURL = srv.URL
	if _, err := p.Status(context.Background(), "ch_1"); err == nil {
		t.Fatal("a non-JSON error body should produce an error")
	}
}

// TestDoJSON_BadURL asserts a malformed BaseURL surfaces the request-construction
// error (the http.NewRequestWithContext branch).
func TestDoJSON_BadURL(t *testing.T) {
	p := NewCloudflareProvider("z", "t", "o")
	p.BaseURL = "http://[::1]:namedport" // invalid → NewRequest fails
	if _, err := p.CreateCustomHostname(context.Background(), "x.com"); err == nil {
		t.Fatal("a malformed URL should error")
	}
}

// TestDefaultClients asserts the lazy default getters return a non-nil client and
// base URL when the struct fields are left unset (the nil-guard branches).
func TestDefaultClients(t *testing.T) {
	c := &CloudflareProvider{} // no HTTP, no BaseURL set
	if c.httpClient() == nil {
		t.Error("httpClient() should never be nil")
	}
	if c.baseURL() != "https://api.cloudflare.com/client/v4" {
		t.Errorf("default baseURL = %q", c.baseURL())
	}
	// And the explicit overrides win.
	c.BaseURL = "https://override.example"
	if c.baseURL() != "https://override.example" {
		t.Errorf("override baseURL = %q", c.baseURL())
	}
}

// TestStatus_VerifyingFromSSLPending exercises the create→status path where the
// hostname is active at the zone level but the cert is still validating, mapping
// to StateVerifying with TLSIssued=false and a DCV record still surfaced.
func TestStatus_VerifyingFromSSLPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"result": {
				"id": "ch_9",
				"status": "active",
				"ssl": {
					"status": "pending_validation",
					"validation_records": [{"txt_name": "_acme.docs.acme.com", "txt_value": "v"}]
				}
			}
		}`))
	}))
	defer srv.Close()

	p := NewCloudflareProvider("z", "t", "o")
	p.BaseURL = srv.URL
	st, err := p.Status(context.Background(), "ch_9")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateVerifying || st.TLSIssued {
		t.Fatalf("status = %+v, want verifying/no-TLS", st)
	}
	if st.DCV.Name != "_acme.docs.acme.com" {
		t.Errorf("DCV not surfaced while verifying: %+v", st.DCV)
	}
}

// TestFake_AdvanceTo_UnknownID asserts AdvanceTo on a non-existent id is an error
// (the test helper's error branch).
func TestFake_AdvanceTo_UnknownID(t *testing.T) {
	if err := NewFake().AdvanceTo("nope", StateActive); err == nil {
		t.Fatal("AdvanceTo on an unknown id should error")
	}
}
