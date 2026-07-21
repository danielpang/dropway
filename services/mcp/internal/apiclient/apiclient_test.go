// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package apiclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDeploy_FullLoop(t *testing.T) {
	var uploaded = map[string][]byte{}
	var finalizeBody, prepareBody, publishBody string

	// A combined server standing in for BOTH the API and the presigned blob store:
	// prepare returns an upload URL pointing back at /upload/<sha> on this same server.
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sites/s1/deployments/prepare", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("prepare missing bearer: %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		prepareBody = string(b)
		var req struct {
			Manifest []manifestFile `json:"manifest"`
		}
		_ = json.Unmarshal(b, &req)
		// Say every blob is missing; hand back upload URLs on this server.
		missing := []string{}
		uploads := map[string]string{}
		for _, m := range req.Manifest {
			missing = append(missing, m.SHA256)
			uploads[m.SHA256] = base + "/upload/" + m.SHA256
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"missing": missing, "uploads": uploads})
	})
	mux.HandleFunc("/upload/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("blob upload method = %s, want PUT", r.Method)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("presigned upload must NOT carry an Authorization header")
		}
		sha := strings.TrimPrefix(r.URL.Path, "/upload/")
		b, _ := io.ReadAll(r.Body)
		uploaded[sha] = b
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/sites/s1/deployments", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		finalizeBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "ver-1", "version_no": 1})
	})
	mux.HandleFunc("/v1/sites/s1/publish", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		publishBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"live_url": "https://acme-docs.x", "version_id": "ver-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	files := []DeployFile{
		{Path: "index.html", Data: []byte("<h1>hi</h1>")},
		{Path: "a.bin", Data: []byte{0x00, 0x01}},
	}
	res, err := New(srv.URL).Deploy(context.Background(), "tok", "s1", files, true)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	// Both blobs uploaded with the exact bytes, keyed by their real sha256.
	if len(uploaded) != 2 {
		t.Fatalf("want 2 blobs uploaded, got %d", len(uploaded))
	}
	for _, f := range files {
		sum := sha256.Sum256(f.Data)
		sha := hex.EncodeToString(sum[:])
		if !bytes.Equal(uploaded[sha], f.Data) {
			t.Errorf("blob %s bytes mismatch", f.Path)
		}
	}
	// finalize carries the digest computed from path+sha (manifest.Digest).
	if !strings.Contains(finalizeBody, `"digest"`) {
		t.Errorf("finalize body missing digest: %s", finalizeBody)
	}
	if prepareBody == "" {
		t.Error("prepare not called")
	}
	if !strings.Contains(publishBody, "ver-1") {
		t.Errorf("publish body should reference the finalized version: %s", publishBody)
	}
	if res.VersionID != "ver-1" || res.LiveURL == "" || res.FilesUploaded != 2 || !res.Published {
		t.Errorf("result wrong: %+v", res)
	}
}

func TestDeploy_NoPublishStagesOnly(t *testing.T) {
	published := false
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/v1/sites/s1/deployments/prepare", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Manifest []manifestFile `json:"manifest"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		uploads := map[string]string{}
		var missing []string
		for _, m := range req.Manifest {
			missing = append(missing, m.SHA256)
			uploads[m.SHA256] = base + "/u/" + m.SHA256
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"missing": missing, "uploads": uploads})
	})
	mux.HandleFunc("/u/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/v1/sites/s1/deployments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "ver-9"})
	})
	mux.HandleFunc("/v1/sites/s1/publish", func(w http.ResponseWriter, _ *http.Request) {
		published = true
		w.WriteHeader(200)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	res, err := New(srv.URL).Deploy(context.Background(), "tok", "s1", []DeployFile{{Path: "i.html", Data: []byte("x")}}, false)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if published {
		t.Error("publish must NOT be called when publish=false")
	}
	if res.Published || res.LiveURL != "" || res.VersionID != "ver-9" {
		t.Errorf("staged result wrong: %+v", res)
	}
}

// TestUploadBlob_RewritesHostKeepsSignedHostHeader proves the self-host upload fix:
// a presigned URL signed against the public host (which the MCP server can't reach
// from inside the compose network) is dialed at the internal endpoint, yet the
// original public host is sent as the Host header so the SigV4 signature verifies.
func TestUploadBlob_RewritesHostKeepsSignedHostHeader(t *testing.T) {
	var gotHost, gotPath, gotRawQuery string
	var gotBody []byte
	// This server stands in for the INTERNAL object store (e.g. minio:9000).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotPath, gotRawQuery = r.Host, r.URL.Path, r.URL.RawQuery
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New("http://api:8080", WithUploadEndpoint(srv.URL))

	// A presigned URL signed against the unreachable public host, with a signature
	// query string that must survive the rewrite untouched.
	presigned := "http://localhost:9000/dropway-blobs/blobs/org/abc123?X-Amz-Signature=deadbeef&X-Amz-SignedHeaders=host"
	if err := c.uploadBlob(context.Background(), presigned, []byte("payload")); err != nil {
		t.Fatalf("uploadBlob: %v", err)
	}

	if gotHost != "localhost:9000" {
		t.Errorf("Host header = %q, want the signed public host localhost:9000", gotHost)
	}
	if gotPath != "/dropway-blobs/blobs/org/abc123" {
		t.Errorf("path = %q, want it preserved", gotPath)
	}
	if gotRawQuery != "X-Amz-Signature=deadbeef&X-Amz-SignedHeaders=host" {
		t.Errorf("query = %q, want the presigned query preserved verbatim", gotRawQuery)
	}
	if string(gotBody) != "payload" {
		t.Errorf("body = %q, want the raw bytes", gotBody)
	}
}

// TestUploadBlob_NoRewriteWithoutEndpoint confirms that without an upload endpoint
// the URL is used exactly as signed (the browser/CLI host path is unchanged).
func TestUploadBlob_NoRewriteWithoutEndpoint(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// No WithUploadEndpoint → PUT goes straight to the URL's own host.
	if err := New("http://api:8080").uploadBlob(context.Background(), srv.URL+"/u/abc", []byte("x")); err != nil {
		t.Fatalf("uploadBlob: %v", err)
	}
	if gotHost != strings.TrimPrefix(srv.URL, "http://") {
		t.Errorf("Host = %q, want the URL's own host %q", gotHost, strings.TrimPrefix(srv.URL, "http://"))
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
