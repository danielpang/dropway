// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
)

// siteReadRouterFor mirrors the production /sites routes needed to publish a site
// and then read its files back (the MCP read path). Local to this test to avoid
// the router→handlers import cycle, like routerFor in deployments_test.go.
func siteReadRouterFor(a *API, orgID, userID string) http.Handler {
	v := fakeVerifier{claims: claims(userID, orgID, "owner")}
	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Use(a.EnsureOrgProvisioned)
		r.Route("/sites", func(r chi.Router) {
			r.Post("/", a.CreateSite)
			r.Post("/{id}/deployments", a.FinalizeDeployment)
			r.Post("/{id}/publish", a.Publish)
			r.Get("/{id}/files", a.ListSiteFiles)
			r.Get("/{id}/files/content", a.ReadSiteFile)
			r.Get("/{id}/download", a.DownloadSite)
		})
	})
	return r
}

// publishTwoFileSite creates a site and publishes a version with a text file
// (index.html) and a binary file (logo.png), returning the site id.
func publishTwoFileSite(t *testing.T, h http.Handler, obj *storage.Fake) string {
	t.Helper()
	rr := do(t, h, http.MethodPost, "/v1/sites", `{"slug":"mysite","access_mode":"public"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rr.Code, rr.Body.String())
	}
	var site siteResponse
	mustJSON(t, rr, &site)

	idx := []byte("<h1>hi</h1>")
	logo := []byte{0xff, 0xd8, 0xff, 0x00, 0x01} // invalid utf8 → base64
	idxSHA, logoSHA := sha(idx), sha(logo)
	must(t, obj.PutBlobBytes(context.Background(), "org_1", idxSHA, idx))
	must(t, obj.PutBlobBytes(context.Background(), "org_1", logoSHA, logo))

	files := []ManifestFile{
		{Path: "index.html", SHA256: idxSHA, Size: int64(len(idx)), ContentType: "text/html"},
		{Path: "logo.png", SHA256: logoSHA, Size: int64(len(logo)), ContentType: "image/png"},
	}
	mf, _ := json.Marshal(files)
	digest := manifest.Digest([]manifest.File{
		{Path: "index.html", SHA256: idxSHA},
		{Path: "logo.png", SHA256: logoSHA},
	})
	rr = do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/deployments",
		`{"digest":"`+digest+`","manifest":`+string(mf)+`}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("finalize: %d %s", rr.Code, rr.Body.String())
	}
	var fin finalizeResponse
	mustJSON(t, rr, &fin)

	rr = do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/publish",
		`{"version_id":"`+fin.VersionID+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", rr.Code, rr.Body.String())
	}
	return site.ID
}

func newSiteReadAPI(t *testing.T) (*API, http.Handler, *storage.Fake) {
	t.Helper()
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	a := NewFull(quota.Unlimited{}, fs, obj, projection.NewLocal())
	return a, siteReadRouterFor(a, "org_1", "user_1"), obj
}

func TestListSiteFiles_SortedMeta(t *testing.T) {
	_, h, obj := newSiteReadAPI(t)
	siteID := publishTwoFileSite(t, h, obj)

	rr := do(t, h, http.MethodGet, "/v1/sites/"+siteID+"/files", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list files: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Files []siteFileMeta `json:"files"`
	}
	mustJSON(t, rr, &out)
	if len(out.Files) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(out.Files), out.Files)
	}
	// Sorted by path: index.html, logo.png.
	if out.Files[0].Path != "index.html" || out.Files[1].Path != "logo.png" {
		t.Fatalf("files not sorted: %+v", out.Files)
	}
	if out.Files[0].ContentType != "text/html" || out.Files[0].SHA256 == "" || out.Files[0].Size == 0 {
		t.Errorf("index.html meta wrong: %+v", out.Files[0])
	}
}

func TestReadSiteFile_TextAndBinary(t *testing.T) {
	_, h, obj := newSiteReadAPI(t)
	siteID := publishTwoFileSite(t, h, obj)

	// Text file → utf8 inline.
	rr := do(t, h, http.MethodGet, "/v1/sites/"+siteID+"/files/content?path=index.html", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("read index.html: %d %s", rr.Code, rr.Body.String())
	}
	var text siteFilePayload
	mustJSON(t, rr, &text)
	if text.Encoding != "utf8" || text.Content != "<h1>hi</h1>" || text.ContentType != "text/html" {
		t.Errorf("index.html payload wrong: %+v", text)
	}
	if text.Size != int64(len("<h1>hi</h1>")) {
		t.Errorf("index.html size = %d, want %d", text.Size, len("<h1>hi</h1>"))
	}

	// Binary file → base64.
	rr = do(t, h, http.MethodGet, "/v1/sites/"+siteID+"/files/content?path=logo.png", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("read logo.png: %d %s", rr.Code, rr.Body.String())
	}
	var bin siteFilePayload
	mustJSON(t, rr, &bin)
	if bin.Encoding != "base64" || bin.Content == "" {
		t.Errorf("logo.png should be base64: %+v", bin)
	}
}

func TestReadSiteFile_UnknownPathIs404(t *testing.T) {
	_, h, obj := newSiteReadAPI(t)
	siteID := publishTwoFileSite(t, h, obj)

	rr := do(t, h, http.MethodGet, "/v1/sites/"+siteID+"/files/content?path=secret.txt", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("a path absent from the manifest should 404, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestReadSiteFile_MissingPathParamIs400(t *testing.T) {
	_, h, obj := newSiteReadAPI(t)
	siteID := publishTwoFileSite(t, h, obj)

	rr := do(t, h, http.MethodGet, "/v1/sites/"+siteID+"/files/content", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing ?path should 400, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestDownloadSite_AllFiles(t *testing.T) {
	_, h, obj := newSiteReadAPI(t)
	siteID := publishTwoFileSite(t, h, obj)

	rr := do(t, h, http.MethodGet, "/v1/sites/"+siteID+"/download", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("download: %d %s", rr.Code, rr.Body.String())
	}
	var out siteDownloadResponse
	mustJSON(t, rr, &out)
	if out.Truncated || len(out.Files) != 2 {
		t.Fatalf("want 2 files, not truncated: %+v", out)
	}
	if out.Files[0].Path != "index.html" || out.Files[0].Content != "<h1>hi</h1>" || out.Files[0].Encoding != "utf8" {
		t.Errorf("index.html wrong: %+v", out.Files[0])
	}
	if out.Files[1].Path != "logo.png" || out.Files[1].Encoding != "base64" || out.Files[1].Size != 5 {
		t.Errorf("logo.png wrong: %+v", out.Files[1])
	}
}

// A site with no published version has no files to read: the read endpoints
// reject it (the MCP short-circuits this case before ever calling the API, but
// the endpoint must still be well-behaved when hit directly).
func TestSiteFiles_UnpublishedSiteRejected(t *testing.T) {
	_, h, _ := newSiteReadAPI(t)
	rr := do(t, h, http.MethodPost, "/v1/sites", `{"slug":"draft","access_mode":"public"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rr.Code, rr.Body.String())
	}
	var site siteResponse
	mustJSON(t, rr, &site)

	rr = do(t, h, http.MethodGet, "/v1/sites/"+site.ID+"/files", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unpublished site /files should 400, got %d %s", rr.Code, rr.Body.String())
	}
}
