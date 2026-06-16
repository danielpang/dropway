package projection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCloudflareKV_PutRoute verifies the writer hits the correct KV REST path,
// sends the Bearer token, and PUTs the canonical contract JSON the Worker parses.
func TestCloudflareKV_PutRoute(t *testing.T) {
	var gotPath, gotAuth, gotMethod string
	var gotBody RouteValue
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotMethod = r.URL.Path, r.Header.Get("Authorization"), r.Method
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	c := NewCloudflareKV("acct-1", "ns-1", "token-xyz")
	c.BaseURL = srv.URL

	val := validRoute("v1")
	if err := c.PutRoute(context.Background(), "site.dropwaycontent.com", val); err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %s", gotMethod)
	}
	wantPath := "/accounts/acct-1/storage/kv/namespaces/ns-1/values/route:site.dropwaycontent.com"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if gotAuth != "Bearer token-xyz" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody != val {
		t.Errorf("body = %+v, want %+v", gotBody, val)
	}
}

func TestCloudflareKV_DeleteRoute(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewCloudflareKV("a", "n", "t")
	c.BaseURL = srv.URL
	if err := c.DeleteRoute(context.Background(), "h.dropwaycontent.com"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || !strings.HasSuffix(gotPath, "route:h.dropwaycontent.com") {
		t.Errorf("delete %s %s", gotMethod, gotPath)
	}
}

// TestCloudflareKV_SetOrgStatus verifies the org-status projection: a blocking
// status PUTs the bare status string to org_status:<orgID> (the Worker's read key),
// and "active" DELETEs the key (clearing the edge block). FIX 2.
func TestCloudflareKV_SetOrgStatus(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewCloudflareKV("acct-1", "ns-1", "tok")
	c.BaseURL = srv.URL
	const org = "org-123"

	// Blocking status → PUT the bare status string at org_status:<orgID>.
	if err := c.SetOrgStatus(context.Background(), org, "over_limit"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("blocking status method = %s, want PUT", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "org_status:"+org) {
		t.Errorf("path = %q, want suffix org_status:%s", gotPath, org)
	}
	if gotBody != "over_limit" {
		t.Errorf("body = %q, want the bare status %q", gotBody, "over_limit")
	}

	// "active" → DELETE the key (clear the edge block).
	if err := c.SetOrgStatus(context.Background(), org, "active"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("active status method = %s, want DELETE (clear)", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "org_status:"+org) {
		t.Errorf("clear path = %q, want suffix org_status:%s", gotPath, org)
	}

	// Empty org id is rejected before any HTTP call.
	if err := c.SetOrgStatus(context.Background(), "", "over_limit"); err == nil {
		t.Error("empty org id must be rejected")
	}
}

func TestCloudflareKV_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"message":"bad token"}]}`))
	}))
	defer srv.Close()

	c := NewCloudflareKV("a", "n", "t")
	c.BaseURL = srv.URL
	if err := c.PutRoute(context.Background(), "h", validRoute("v1")); err == nil {
		t.Fatal("expected error on non-2xx")
	}
}
