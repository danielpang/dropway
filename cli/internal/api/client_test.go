package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/manifest"
	"github.com/danielpang/dropway/internal/contenttype"
)

// TestManifestFromBuild proves the build-manifest → wire-shape conversion fills in
// the per-file content-type from the extension (the server records it in the
// stored manifest so the serving Worker sends the right Content-Type).
func TestManifestFromBuild(t *testing.T) {
	m := &manifest.Manifest{Files: []manifest.Entry{
		{Path: "index.html", SHA256: "h1", Size: 11},
		{Path: "assets/app.js", SHA256: "h2", Size: 22},
		{Path: "data.bin", SHA256: "h3", Size: 33},
	}}
	got := ManifestFromBuild(m)
	if len(got) != 3 {
		t.Fatalf("want 3 files, got %d", len(got))
	}
	// Path / sha / size carried through verbatim.
	if got[0].Path != "index.html" || got[0].SHA256 != "h1" || got[0].Size != 11 {
		t.Errorf("file[0] = %+v", got[0])
	}
	// Content-type derived from the extension.
	if !strings.Contains(got[0].ContentType, "text/html") {
		t.Errorf("index.html content-type = %q, want text/html", got[0].ContentType)
	}
	if !strings.Contains(got[1].ContentType, "javascript") {
		t.Errorf("app.js content-type = %q, want javascript", got[1].ContentType)
	}
	// An unknown extension falls back to the binary default.
	if got[2].ContentType != "application/octet-stream" {
		t.Errorf("data.bin content-type = %q, want application/octet-stream", got[2].ContentType)
	}
}

// TestContentTypeFor_FallbackTable covers the explicit fallback switch the stdlib
// mime table can miss (the cases that exercise the non-stdlib branch).
// TestContentTypeFor_FallbackTable covers the extensions the stdlib mime table can
// miss — the CLI keeps this even though inference moved to internal/contenttype, so
// the deploy output it relies on stays pinned.
func TestContentTypeFor_FallbackTable(t *testing.T) {
	cases := map[string]string{
		"app.mjs":          "text/javascript",
		"x.wasm":           "application/wasm",
		"site.webmanifest": "application/manifest+json",
		"NoExtAtAll":       "application/octet-stream",
		"archive.tar.zst":  "application/octet-stream", // unknown → default
	}
	for path, want := range cases {
		if got := contenttype.ForPath(path); !strings.Contains(got, want) {
			t.Errorf("ForPath(%q) = %q, want substring %q", path, got, want)
		}
	}
}

// TestHTTPClient_ErrorBodies asserts every endpoint surfaces a non-2xx with the
// status code so a deploy fails loudly instead of proceeding on a bad response.
func TestHTTPClient_ErrorBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal","message":"boom"}`))
	}))
	defer srv.Close()

	c := &HTTPClient{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	if _, err := c.PrepareDeployment(ctx, "s1", PrepareRequest{}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("PrepareDeployment err = %v, want a 500 surfaced", err)
	}
	if _, err := c.FinalizeDeployment(ctx, "s1", FinalizeRequest{}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("FinalizeDeployment err = %v, want a 500 surfaced", err)
	}
	if _, err := c.Publish(ctx, "s1", PublishRequest{}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("Publish err = %v, want a 500 surfaced", err)
	}
}

// TestUploadBlob_StoreError surfaces a non-2xx from the object store (a failed
// presigned PUT) with the status code.
func TestUploadBlob_StoreError(t *testing.T) {
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("AccessDenied"))
	}))
	defer store.Close()

	c := &HTTPClient{BaseURL: "unused", Token: "t"}
	err := c.UploadBlob(context.Background(), store.URL+"/blobs/x", []byte("data"))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("UploadBlob err = %v, want a 403 surfaced", err)
	}
}

// TestHTTPClient_HTTPGetter covers the default-client fallback (HTTP == nil uses
// http.DefaultClient) by driving a real round-trip with no explicit client set.
func TestHTTPClient_DefaultHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"site_1","slug":"demo"}`))
	}))
	defer srv.Close()

	c := &HTTPClient{BaseURL: srv.URL, Token: "tok"} // HTTP left nil → DefaultClient
	site, err := c.CreateSite(context.Background(), CreateSiteRequest{Slug: "demo"})
	if err != nil || site.ID != "site_1" {
		t.Fatalf("CreateSite with default client: %v %+v", err, site)
	}
}
