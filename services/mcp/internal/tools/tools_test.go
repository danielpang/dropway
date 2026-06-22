// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/danielpang/dropway/services/mcp/internal/apiclient"
	"github.com/danielpang/dropway/services/mcp/internal/store"
)

func ptr(s string) *string { return &s }

// --- fakes ------------------------------------------------------------------

type fakeStore struct {
	sites  []store.Site
	bySlug map[string]store.Site
	err    error
}

func (f *fakeStore) ListSites(_ context.Context, _ store.Tenant) ([]store.Site, error) {
	return f.sites, f.err
}
func (f *fakeStore) SiteBySlug(_ context.Context, _ store.Tenant, slug string) (store.Site, error) {
	s, ok := f.bySlug[slug]
	if !ok {
		return store.Site{}, store.ErrNotFound
	}
	return s, nil
}

type fakeBlobs struct {
	manifest []byte
	manErr   error
	blobs    map[string][]byte // sha256 → content
}

func (f *fakeBlobs) GetManifest(_ context.Context, _, _, _ string) ([]byte, error) {
	return f.manifest, f.manErr
}
func (f *fakeBlobs) GetBlob(_ context.Context, _, sha string) (io.ReadCloser, error) {
	b, ok := f.blobs[sha]
	if !ok {
		return nil, store.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// fakeAPI records the control-plane calls the write tools make.
type fakeAPI struct {
	createToken, createSlug, createMode string
	createResp                          apiclient.Site
	createErr                           error

	setToken, setSiteID, setMode, setPassword string
	setErr                                    error

	deployToken, deploySiteID string
	deployFiles               []apiclient.DeployFile
	deployPublish             bool
	deployResp                apiclient.DeployResult
	deployErr                 error
}

func (f *fakeAPI) CreateSite(_ context.Context, token, slug, accessMode string) (apiclient.Site, error) {
	f.createToken, f.createSlug, f.createMode = token, slug, accessMode
	if f.createErr != nil {
		return apiclient.Site{}, f.createErr
	}
	return f.createResp, nil
}
func (f *fakeAPI) SetAccess(_ context.Context, token, siteID, mode, password string) error {
	f.setToken, f.setSiteID, f.setMode, f.setPassword = token, siteID, mode, password
	return f.setErr
}
func (f *fakeAPI) Deploy(_ context.Context, token, siteID string, files []apiclient.DeployFile, publish bool) (apiclient.DeployResult, error) {
	f.deployToken, f.deploySiteID, f.deployFiles, f.deployPublish = token, siteID, files, publish
	if f.deployErr != nil {
		return apiclient.DeployResult{}, f.deployErr
	}
	return f.deployResp, nil
}

const manifestJSON = `{"schema_version":1,"files":{
	"index.html":{"sha256":"aaa","content_type":"text/html"},
	"assets/app.js":{"sha256":"bbb","content_type":"application/javascript"},
	"logo.png":{"sha256":"ccc","content_type":"image/png"}
}}`

var tnt = store.Tenant{OrgID: "org-1", UserID: "user-1"}

// --- list_sites -------------------------------------------------------------

func TestListSites(t *testing.T) {
	svc := &Service{Store: &fakeStore{sites: []store.Site{
		{Slug: "docs", AccessMode: "public", CurrentVersionID: ptr("v1"), Host: ptr("acme--docs.dropwaycontent.com")},
		{Slug: "draft", AccessMode: "org_only", CurrentVersionID: nil, Host: nil},
	}}}

	out, err := svc.ListSites(context.Background(), tnt)
	if err != nil {
		t.Fatalf("ListSites: %v", err)
	}
	if len(out.Sites) != 2 {
		t.Fatalf("want 2 sites, got %d", len(out.Sites))
	}
	if s := out.Sites[0]; s.Slug != "docs" || s.AccessMode != "public" || !s.Live || s.URL != "https://acme--docs.dropwaycontent.com" {
		t.Errorf("site[0] wrong: %+v", s)
	}
	if s := out.Sites[1]; !(s.Slug == "draft" && !s.Live && s.URL == "") {
		t.Errorf("non-live site should have Live=false and no URL: %+v", s)
	}
}

func TestListSites_StoreError(t *testing.T) {
	svc := &Service{Store: &fakeStore{err: errors.New("boom")}}
	if _, err := svc.ListSites(context.Background(), tnt); err == nil {
		t.Fatal("expected store error to propagate")
	}
}

// --- list_files -------------------------------------------------------------

func TestListFiles_SortedPaths(t *testing.T) {
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{
			"docs": {ID: "s1", Slug: "docs", CurrentVersionID: ptr("v1")},
		}},
		Blobs: &fakeBlobs{manifest: []byte(manifestJSON)},
	}
	out, err := svc.ListFiles(context.Background(), tnt, "docs")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	want := []string{"assets/app.js", "index.html", "logo.png"}
	if len(out.Files) != len(want) {
		t.Fatalf("want %v, got %v", want, out.Files)
	}
	for i := range want {
		if out.Files[i] != want[i] {
			t.Errorf("files[%d] = %q, want %q (sorted)", i, out.Files[i], want[i])
		}
	}
}

func TestListFiles_NotLiveIsEmpty(t *testing.T) {
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{
		"draft": {ID: "s2", Slug: "draft", CurrentVersionID: nil},
	}}}
	out, err := svc.ListFiles(context.Background(), tnt, "draft")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(out.Files) != 0 {
		t.Errorf("a non-live site should list no files, got %v", out.Files)
	}
}

func TestListFiles_UnknownSite(t *testing.T) {
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}}
	if _, err := svc.ListFiles(context.Background(), tnt, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for unknown site, got %v", err)
	}
}

// --- read_file --------------------------------------------------------------

func TestReadFile_Text(t *testing.T) {
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs", CurrentVersionID: ptr("v1")}}},
		Blobs: &fakeBlobs{manifest: []byte(manifestJSON), blobs: map[string][]byte{"aaa": []byte("<h1>hi</h1>")}},
	}
	out, err := svc.ReadFile(context.Background(), tnt, "docs", "index.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if out.Text != "<h1>hi</h1>" || out.ContentType != "text/html" || out.Base64 != "" {
		t.Errorf("text read wrong: %+v", out)
	}
}

func TestReadFile_BinaryIsBase64(t *testing.T) {
	bin := []byte{0xff, 0xd8, 0xff, 0x00, 0x01} // invalid UTF-8 (e.g. image bytes)
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs", CurrentVersionID: ptr("v1")}}},
		Blobs: &fakeBlobs{manifest: []byte(manifestJSON), blobs: map[string][]byte{"ccc": bin}},
	}
	out, err := svc.ReadFile(context.Background(), tnt, "docs", "logo.png")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if out.Base64 == "" || out.Text != "" {
		t.Errorf("binary file should be base64, not text: %+v", out)
	}
}

func TestReadFile_PathNotInManifest(t *testing.T) {
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs", CurrentVersionID: ptr("v1")}}},
		Blobs: &fakeBlobs{manifest: []byte(manifestJSON), blobs: map[string][]byte{}},
	}
	if _, err := svc.ReadFile(context.Background(), tnt, "docs", "secret.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for missing path, got %v", err)
	}
}

func TestReadFile_NotLive(t *testing.T) {
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{"draft": {ID: "s2", Slug: "draft", CurrentVersionID: nil}}}}
	if _, err := svc.ReadFile(context.Background(), tnt, "draft", "index.html"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for non-live site, got %v", err)
	}
}

// --- download_site ----------------------------------------------------------

func TestDownloadSite_AllFiles(t *testing.T) {
	bin := []byte{0xff, 0xd8, 0xff, 0x00}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs", CurrentVersionID: ptr("v1")}}},
		Blobs: &fakeBlobs{manifest: []byte(manifestJSON), blobs: map[string][]byte{
			"aaa": []byte("<h1>hi</h1>"),
			"bbb": []byte("console.log(1)"),
			"ccc": bin,
		}},
	}
	out, err := svc.DownloadSite(context.Background(), tnt, "docs")
	if err != nil {
		t.Fatalf("DownloadSite: %v", err)
	}
	if out.Truncated {
		t.Fatal("should not be truncated")
	}
	if len(out.Files) != 3 {
		t.Fatalf("want 3 files, got %d", len(out.Files))
	}
	// Sorted by path: assets/app.js, index.html, logo.png.
	if out.Files[0].Path != "assets/app.js" || out.Files[1].Path != "index.html" || out.Files[2].Path != "logo.png" {
		t.Fatalf("files not sorted: %+v", out.Files)
	}
	if out.Files[1].Text != "<h1>hi</h1>" || out.Files[1].Size != len("<h1>hi</h1>") {
		t.Errorf("index.html wrong: %+v", out.Files[1])
	}
	if out.Files[2].Base64 == "" || out.Files[2].Text != "" {
		t.Errorf("binary file should be base64: %+v", out.Files[2])
	}
}

func TestDownloadSite_Truncated(t *testing.T) {
	orig := maxDownloadBytes
	maxDownloadBytes = 12 // tiny cap
	defer func() { maxDownloadBytes = orig }()

	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs", CurrentVersionID: ptr("v1")}}},
		Blobs: &fakeBlobs{manifest: []byte(manifestJSON), blobs: map[string][]byte{
			"aaa": []byte("0123456789"),    // 10 bytes (assets/app.js? no — index.html=aaa)
			"bbb": []byte("0123456789ABC"), // 13 bytes
			"ccc": []byte("xx"),
		}},
	}
	out, err := svc.DownloadSite(context.Background(), tnt, "docs")
	if err != nil {
		t.Fatalf("DownloadSite: %v", err)
	}
	if !out.Truncated {
		t.Fatal("expected Truncated=true past the size cap")
	}
	// First file (assets/app.js → bbb, 13 bytes) already exceeds the 12-byte cap → nothing fits.
	if len(out.Files) != 0 {
		t.Fatalf("expected 0 files under a 12-byte cap, got %d: %+v", len(out.Files), out.Files)
	}
}

func TestDownloadSite_NotLive(t *testing.T) {
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{"draft": {ID: "s2", Slug: "draft", CurrentVersionID: nil}}}}
	out, err := svc.DownloadSite(context.Background(), tnt, "draft")
	if err != nil {
		t.Fatalf("DownloadSite: %v", err)
	}
	if len(out.Files) != 0 || out.Truncated {
		t.Errorf("non-live site should download nothing: %+v", out)
	}
}

// --- create_site ------------------------------------------------------------

func TestCreateSite_ForwardsAndMaps(t *testing.T) {
	api := &fakeAPI{createResp: apiclient.Site{Slug: "blog", AccessMode: "org_only", URL: "https://acme--blog.dropwaycontent.com"}}
	svc := &Service{API: api}
	out, err := svc.CreateSite(context.Background(), "tok-123", "blog", "org_only")
	if err != nil {
		t.Fatalf("CreateSite: %v", err)
	}
	if api.createToken != "tok-123" || api.createSlug != "blog" || api.createMode != "org_only" {
		t.Errorf("API not called with the right args: %+v", api)
	}
	if out.Slug != "blog" || out.AccessMode != "org_only" || out.URL == "" {
		t.Errorf("result not mapped from the API response: %+v", out)
	}
}

func TestCreateSite_APIErrorPropagates(t *testing.T) {
	api := &fakeAPI{createErr: &apiclient.Error{Status: 402, Message: "site limit reached"}}
	svc := &Service{API: api}
	_, err := svc.CreateSite(context.Background(), "tok", "blog", "")
	if err == nil {
		t.Fatal("expected the API error to propagate")
	}
}

// A loose agent-supplied slug is normalized to the canonical grammar BEFORE the
// API call (mirroring the dashboard/CLI), so it never 400s on shape (H1).
func TestCreateSite_NormalizesSlug(t *testing.T) {
	api := &fakeAPI{createResp: apiclient.Site{Slug: "my-blog", AccessMode: "org_only"}}
	svc := &Service{API: api}
	if _, err := svc.CreateSite(context.Background(), "tok", "My Blog!", "org_only"); err != nil {
		t.Fatalf("CreateSite: %v", err)
	}
	if api.createSlug != "my-blog" {
		t.Errorf("API called with slug %q, want %q (normalized)", api.createSlug, "my-blog")
	}
}

// A slug with no usable characters errors locally without an API call.
func TestCreateSite_RejectsUnusableSlug(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{API: api}
	if _, err := svc.CreateSite(context.Background(), "tok", "!!!", ""); err == nil {
		t.Fatal("expected an error for a slug with no usable characters")
	}
	if api.createSlug != "" {
		t.Errorf("API should not have been called; got slug %q", api.createSlug)
	}
}

// --- set_site_access --------------------------------------------------------

func TestSetAccess_ResolvesSlugToID(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "site-xyz", Slug: "docs"}}},
		API:   api,
	}
	out, err := svc.SetAccess(context.Background(), tnt, "tok-7", "docs", "public", "")
	if err != nil {
		t.Fatalf("SetAccess: %v", err)
	}
	if api.setSiteID != "site-xyz" {
		t.Errorf("slug not resolved to id: setSiteID=%q, want site-xyz", api.setSiteID)
	}
	if api.setToken != "tok-7" || api.setMode != "public" {
		t.Errorf("API not called with the right args: %+v", api)
	}
	if out.Slug != "docs" || out.Mode != "public" {
		t.Errorf("result wrong: %+v", out)
	}
}

func TestSetAccess_UnknownSiteDoesNotCallAPI(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}, API: api}
	if _, err := svc.SetAccess(context.Background(), tnt, "tok", "ghost", "public", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for unknown site, got %v", err)
	}
	if api.setSiteID != "" {
		t.Error("API must not be called when the slug doesn't resolve")
	}
}

func TestSetAccess_PasswordForwarded(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs"}}},
		API:   api,
	}
	if _, err := svc.SetAccess(context.Background(), tnt, "tok", "docs", "password", "hunter2"); err != nil {
		t.Fatalf("SetAccess: %v", err)
	}
	if api.setMode != "password" || api.setPassword != "hunter2" {
		t.Errorf("password mode not forwarded: %+v", api)
	}
}

// --- deploy_site ------------------------------------------------------------

func TestDeploySite_DecodesAndForwards(t *testing.T) {
	api := &fakeAPI{deployResp: apiclient.DeployResult{
		VersionID: "v1", LiveURL: "https://acme--docs.dropwaycontent.com", FilesUploaded: 2, Published: true,
	}}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "site-1", Slug: "docs"}}},
		API:   api,
	}
	files := []deployFileIn{
		{Path: "index.html", Text: "<h1>hi</h1>"},
		{Path: "logo.png", Base64: "AAEC"}, // 3 bytes: 0x00 0x01 0x02
	}
	out, err := svc.DeploySite(context.Background(), tnt, "tok-9", "docs", files, true)
	if err != nil {
		t.Fatalf("DeploySite: %v", err)
	}
	// Slug resolved to id, token + publish forwarded.
	if api.deploySiteID != "site-1" || api.deployToken != "tok-9" || !api.deployPublish {
		t.Errorf("forwarded args wrong: %+v", api)
	}
	// Files decoded: text → raw bytes, base64 → decoded bytes.
	if len(api.deployFiles) != 2 {
		t.Fatalf("want 2 files, got %d", len(api.deployFiles))
	}
	if string(api.deployFiles[0].Data) != "<h1>hi</h1>" {
		t.Errorf("text file not decoded: %q", api.deployFiles[0].Data)
	}
	if want := []byte{0x00, 0x01, 0x02}; !bytes.Equal(api.deployFiles[1].Data, want) {
		t.Errorf("base64 file decoded wrong: %v, want %v", api.deployFiles[1].Data, want)
	}
	// Result mapped from the API.
	if out.VersionID != "v1" || out.LiveURL == "" || out.FilesUploaded != 2 || !out.Published {
		t.Errorf("result not mapped: %+v", out)
	}
}

func TestDeploySite_StageWithoutPublish(t *testing.T) {
	api := &fakeAPI{deployResp: apiclient.DeployResult{VersionID: "v2", Published: false}}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs"}}},
		API:   api,
	}
	out, err := svc.DeploySite(context.Background(), tnt, "tok", "docs", []deployFileIn{{Path: "index.html", Text: "x"}}, false)
	if err != nil {
		t.Fatalf("DeploySite: %v", err)
	}
	if api.deployPublish {
		t.Error("publish=false should be forwarded")
	}
	if out.Published || out.LiveURL != "" {
		t.Errorf("staged deploy should not be published: %+v", out)
	}
}

func TestDeploySite_InvalidBase64(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs"}}},
		API:   api,
	}
	_, err := svc.DeploySite(context.Background(), tnt, "tok", "docs", []deployFileIn{{Path: "x", Base64: "!!!notb64"}}, true)
	if err == nil {
		t.Fatal("expected an invalid-base64 error")
	}
	if api.deploySiteID != "" {
		t.Error("API must not be called when a file fails to decode")
	}
}

func TestDeploySite_NoFiles(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs"}}},
		API:   api,
	}
	if _, err := svc.DeploySite(context.Background(), tnt, "tok", "docs", nil, true); err == nil {
		t.Fatal("expected an error for an empty file set")
	}
}

// The deploy_site input schema must publish `files` as a plain "array" and
// `publish` as a plain "boolean" — NOT "[null, …]" unions, which some MCP clients
// coerce to strings (the array/bool then fails validation). Regression guard.
func TestDeploySite_SchemaHasPlainTypes(t *testing.T) {
	s := inputSchema[deploySiteIn]()
	files := s.Properties["files"]
	if files == nil || files.Type != "array" || len(files.Types) != 0 {
		t.Fatalf("files type = %q / %v, want plain \"array\"", files.Type, files.Types)
	}
	if files.Items == nil || files.Items.Type != "object" {
		t.Fatalf("files.items should be an object schema, got %+v", files.Items)
	}
	pub := s.Properties["publish"]
	if pub == nil || pub.Type != "boolean" || len(pub.Types) != 0 {
		t.Fatalf("publish type = %q / %v, want plain \"boolean\"", pub.Type, pub.Types)
	}
	if s.Type != "object" {
		t.Fatalf("top-level type = %q, want object", s.Type)
	}
}

func TestDeploySite_UnknownSite(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}, API: api}
	if _, err := svc.DeploySite(context.Background(), tnt, "tok", "ghost", []deployFileIn{{Path: "i", Text: "x"}}, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if api.deploySiteID != "" {
		t.Error("API must not be called for an unknown site")
	}
}
