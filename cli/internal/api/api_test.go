package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/contenttype"
)

// TestHTTPClient_FullFlow drives the real HTTPClient through the entire deploy
// contract against a fake server (an integration-style test): create → prepare →
// upload (presigned PUT) → finalize → publish. It asserts each request carries
// the Bearer token, hits the right path, and that the presigned PUT goes to the
// store URL the server returned (a different host than the API).
func TestHTTPClient_FullFlow(t *testing.T) {
	var gotAuth []string

	// A stand-in object store that records presigned PUTs.
	var uploadedKey string
	var uploadedBytes []byte
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		uploadedKey = r.URL.Path
		uploadedBytes, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer store.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/sites" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(Site{ID: "site_1", Slug: "demo", LiveURL: "https://demo.dropwaycontent.com"})
		case strings.HasSuffix(r.URL.Path, "/deployments/prepare"):
			// Report the one referenced blob missing, pointing the upload at the
			// store server (a distinct origin).
			var req PrepareRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			sha := req.Manifest[0].SHA256
			_ = json.NewEncoder(w).Encode(PrepareResponse{
				Missing: []string{sha},
				Uploads: map[string]string{sha: store.URL + "/blobs/org/" + sha},
			})
		case strings.HasSuffix(r.URL.Path, "/deployments"):
			_ = json.NewEncoder(w).Encode(FinalizeResponse{VersionID: "ver_1", VersionNo: 1})
		case strings.HasSuffix(r.URL.Path, "/publish"):
			_ = json.NewEncoder(w).Encode(PublishResponse{LiveURL: "https://demo.dropwaycontent.com", VersionID: "ver_1"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	c := &HTTPClient{BaseURL: api.URL, Token: "shpd_tok"}
	ctx := context.Background()

	site, err := c.CreateSite(ctx, CreateSiteRequest{Slug: "demo"})
	if err != nil || site.ID != "site_1" {
		t.Fatalf("create: %v %+v", err, site)
	}

	files := []ManifestFile{{Path: "index.html", SHA256: strings.Repeat("a", 64), Size: 5, ContentType: "text/html"}}
	prep, err := c.PrepareDeployment(ctx, site.ID, PrepareRequest{Manifest: files})
	if err != nil || len(prep.Missing) != 1 {
		t.Fatalf("prepare: %v %+v", err, prep)
	}

	// Upload the missing blob to the presigned URL (the store server).
	url := prep.Uploads[prep.Missing[0]]
	if err := c.UploadBlob(ctx, url, []byte("hello")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if uploadedKey != "/blobs/org/"+strings.Repeat("a", 64) || string(uploadedBytes) != "hello" {
		t.Errorf("store got key=%q bytes=%q", uploadedKey, uploadedBytes)
	}

	fin, err := c.FinalizeDeployment(ctx, site.ID, FinalizeRequest{Manifest: files, Digest: strings.Repeat("b", 64)})
	if err != nil || fin.VersionID != "ver_1" {
		t.Fatalf("finalize: %v %+v", err, fin)
	}

	pub, err := c.Publish(ctx, site.ID, PublishRequest{VersionID: fin.VersionID})
	if err != nil || pub.LiveURL == "" {
		t.Fatalf("publish: %v %+v", err, pub)
	}

	// Every API call carried the Bearer token.
	for i, a := range gotAuth {
		if a != "Bearer shpd_tok" {
			t.Errorf("call %d auth = %q", i, a)
		}
	}
}

func TestHTTPClient_ErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad_request","message":"slug is reserved"}`))
	}))
	defer srv.Close()

	c := &HTTPClient{BaseURL: srv.URL, Token: "t"}
	_, err := c.CreateSite(context.Background(), CreateSiteRequest{Slug: "admin"})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v, want a 400 surfaced", err)
	}
}

// TestContentTypeFor checks the content types the CLI deploy path records. The
// inference itself now lives in the shared internal/contenttype package, but the
// CLI keeps this test so a change there can't silently break the deploy output the
// CLI depends on.
func TestContentTypeFor(t *testing.T) {
	cases := map[string]string{
		"index.html": "text/html",
		"app.js":     "javascript",
		"data.json":  "json",
		"x.unknown":  "application/octet-stream",
	}
	for path, want := range cases {
		if got := contenttype.ForPath(path); !strings.Contains(got, want) {
			t.Errorf("ForPath(%q) = %q, want substring %q", path, got, want)
		}
	}
}
