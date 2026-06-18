// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateSite_PostsAndForwardsToken(t *testing.T) {
	var gotAuth, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id": "s1", "slug": "blog", "access_mode": "org_only", "url": "https://x",
		})
	}))
	defer srv.Close()

	site, err := New(srv.URL).CreateSite(context.Background(), "tok-1", "blog", "org_only")
	if err != nil {
		t.Fatalf("CreateSite: %v", err)
	}
	if gotPath != "/v1/sites" {
		t.Errorf("path = %q, want /v1/sites", gotPath)
	}
	if gotAuth != "Bearer tok-1" {
		t.Errorf("auth = %q, want Bearer tok-1", gotAuth)
	}
	if gotBody == "" || !json.Valid([]byte(gotBody)) {
		t.Errorf("body not JSON: %q", gotBody)
	}
	var sent map[string]string
	_ = json.Unmarshal([]byte(gotBody), &sent)
	if sent["slug"] != "blog" || sent["access_mode"] != "org_only" {
		t.Errorf("body fields wrong: %v", sent)
	}
	if site.ID != "s1" || site.Slug != "blog" || site.AccessMode != "org_only" {
		t.Errorf("decoded site wrong: %+v", site)
	}
}

func TestCreateSite_OmitsEmptyAccessMode(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]string{"slug": "blog"})
	}))
	defer srv.Close()

	if _, err := New(srv.URL).CreateSite(context.Background(), "tok", "blog", ""); err != nil {
		t.Fatalf("CreateSite: %v", err)
	}
	var sent map[string]string
	_ = json.Unmarshal([]byte(gotBody), &sent)
	if _, present := sent["access_mode"]; present {
		t.Errorf("empty access_mode should be omitted, body=%q", gotBody)
	}
}

func TestSetAccess_PutsToIDPath(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New(srv.URL).SetAccess(context.Background(), "tok", "site-9", "password", "pw"); err != nil {
		t.Fatalf("SetAccess: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/v1/sites/site-9/access" {
		t.Errorf("method/path = %s %s, want PUT /v1/sites/site-9/access", gotMethod, gotPath)
	}
	var sent map[string]string
	_ = json.Unmarshal([]byte(gotBody), &sent)
	if sent["mode"] != "password" || sent["password"] != "pw" {
		t.Errorf("body fields wrong: %v", sent)
	}
}

func TestErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "admin/owner role required"})
	}))
	defer srv.Close()

	err := New(srv.URL).SetAccess(context.Background(), "tok", "s1", "public", "")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *Error, got %T (%v)", err, err)
	}
	if apiErr.Status != http.StatusForbidden || apiErr.Message != "admin/owner role required" {
		t.Errorf("error not mapped: %+v", apiErr)
	}
}
