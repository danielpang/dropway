// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

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

// fakeSkills satisfies SkillStore, recording the filter args list_skills passes.
type fakeSkills struct {
	skills  []store.Skill
	listErr error

	listQuery, listFolder string
	listPresetsOnly       bool

	bySlug map[string]store.Skill

	folders    []store.SkillFolder
	foldersErr error

	folderSkills map[string][]store.Skill // folder id → skills
}

func (f *fakeSkills) ListSkills(_ context.Context, _ store.Tenant, query, folderSlug string, presetsOnly bool) ([]store.Skill, error) {
	f.listQuery, f.listFolder, f.listPresetsOnly = query, folderSlug, presetsOnly
	return f.skills, f.listErr
}
func (f *fakeSkills) SkillBySlug(_ context.Context, _ store.Tenant, slug string) (store.Skill, error) {
	s, ok := f.bySlug[slug]
	if !ok {
		return store.Skill{}, store.ErrNotFound
	}
	return s, nil
}
func (f *fakeSkills) ListSkillFolders(_ context.Context, _ store.Tenant) ([]store.SkillFolder, error) {
	return f.folders, f.foldersErr
}
func (f *fakeSkills) SkillFolderBySlug(_ context.Context, _ store.Tenant, slug string) (store.SkillFolder, error) {
	for _, fo := range f.folders {
		if fo.Slug == slug {
			return fo, nil
		}
	}
	return store.SkillFolder{}, store.ErrNotFound
}
func (f *fakeSkills) ListFolderSkills(_ context.Context, _ store.Tenant, folderID string) ([]store.Skill, error) {
	return f.folderSkills[folderID], nil
}

type fakeBlobs struct {
	manifest []byte
	manErr   error
	blobs    map[string][]byte // sha256 → content

	skillManifests map[string][]byte // skillID/versionID → manifest JSON
}

func (f *fakeBlobs) GetManifest(_ context.Context, _, _, _ string) ([]byte, error) {
	return f.manifest, f.manErr
}
func (f *fakeBlobs) GetSkillManifest(_ context.Context, _, skillID, versionID string) ([]byte, error) {
	b, ok := f.skillManifests[skillID+"/"+versionID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return b, nil
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

	createSkillToken, createSkillSlug, createSkillTitle string
	createSkillFolders                                  []string
	createSkillResp                                     apiclient.SkillInfo
	createSkillErr                                      error

	uploadToken, uploadSkillID string
	uploadFiles                []apiclient.FileUpload
	uploadResp                 apiclient.UploadResult
	uploadErr                  error

	setFoldersToken, setFoldersSkillID string
	setFoldersIDs                      []string
	setFoldersCalls                    int
	setFoldersErr                      error

	chatCreateToken, chatCreateTitle, chatCreateSource, chatCreateSiteID string
	chatCreateImport                                                     apiclient.ChatImport
	chatCreateResp                                                       apiclient.ChatCreateResult
	chatCreateErr                                                        error

	chatAppendToken, chatAppendID string
	chatAppendImport              apiclient.ChatImport
	chatAppendResp                apiclient.ChatAppendResult
	chatAppendErr                 error

	siteChatToken, siteChatSiteID string
	siteChatImport                apiclient.ChatImport
	siteChatResp                  apiclient.ChatAppendResult
	siteChatErr                   error
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
func (f *fakeAPI) CreateSkill(_ context.Context, token, slug, title string, folders []string) (apiclient.SkillInfo, error) {
	f.createSkillToken, f.createSkillSlug, f.createSkillTitle, f.createSkillFolders = token, slug, title, folders
	if f.createSkillErr != nil {
		return apiclient.SkillInfo{}, f.createSkillErr
	}
	return f.createSkillResp, nil
}
func (f *fakeAPI) UploadSkill(_ context.Context, token, skillID string, files []apiclient.FileUpload) (apiclient.UploadResult, error) {
	f.uploadToken, f.uploadSkillID, f.uploadFiles = token, skillID, files
	if f.uploadErr != nil {
		return apiclient.UploadResult{}, f.uploadErr
	}
	return f.uploadResp, nil
}
func (f *fakeAPI) SetSkillFolders(_ context.Context, token, skillID string, folderIDs []string) error {
	f.setFoldersToken, f.setFoldersSkillID, f.setFoldersIDs = token, skillID, folderIDs
	f.setFoldersCalls++
	return f.setFoldersErr
}
func (f *fakeAPI) CreateChatLog(_ context.Context, token, title, sourceTool, siteID string, imp apiclient.ChatImport) (apiclient.ChatCreateResult, error) {
	f.chatCreateToken, f.chatCreateTitle, f.chatCreateSource, f.chatCreateSiteID = token, title, sourceTool, siteID
	f.chatCreateImport = imp
	if f.chatCreateErr != nil {
		return apiclient.ChatCreateResult{}, f.chatCreateErr
	}
	return f.chatCreateResp, nil
}
func (f *fakeAPI) AppendChatMessages(_ context.Context, token, chatID string, imp apiclient.ChatImport) (apiclient.ChatAppendResult, error) {
	f.chatAppendToken, f.chatAppendID, f.chatAppendImport = token, chatID, imp
	if f.chatAppendErr != nil {
		return apiclient.ChatAppendResult{}, f.chatAppendErr
	}
	return f.chatAppendResp, nil
}
func (f *fakeAPI) AppendSiteChat(_ context.Context, token, siteID string, imp apiclient.ChatImport) (apiclient.ChatAppendResult, error) {
	f.siteChatToken, f.siteChatSiteID, f.siteChatImport = token, siteID, imp
	if f.siteChatErr != nil {
		return apiclient.ChatAppendResult{}, f.siteChatErr
	}
	return f.siteChatResp, nil
}

// fakeChats satisfies ChatStore for the get_site_chat read path.
type fakeChats struct {
	bySite map[string]store.ChatLog       // site id → attached log
	msgs   map[string][]store.ChatMessage // chat log id → messages
}

func (f *fakeChats) ChatLogBySite(_ context.Context, _ store.Tenant, siteID string) (store.ChatLog, error) {
	l, ok := f.bySite[siteID]
	if !ok {
		return store.ChatLog{}, store.ErrNotFound
	}
	return l, nil
}
func (f *fakeChats) ListChatMessages(_ context.Context, _ store.Tenant, chatLogID string) ([]store.ChatMessage, error) {
	return f.msgs[chatLogID], nil
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

// --- list_skills --------------------------------------------------------------

func TestListSkills_MapsFieldsAndOwner(t *testing.T) {
	skills := &fakeSkills{skills: []store.Skill{
		{
			ID: "sk1", Slug: "writing", Title: "Writing", Description: "House style",
			OwnerUserID: "user-9", CurrentVersionID: ptr("v1"), SizeBytes: 1234,
			Folders: []store.SkillFolderRef{{FolderID: "f1", Slug: "product", Title: "Product", IsPreset: true}},
		},
		{ID: "sk2", Slug: "seeded", OwnerUserID: "00000000-0000-0000-0000-000000000000", CurrentVersionID: ptr("v2")},
	}}
	svc := &Service{Skills: skills}

	out, err := svc.ListSkills(context.Background(), tnt, "", "", false)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(out.Skills) != 2 {
		t.Fatalf("want 2 skills, got %d", len(out.Skills))
	}
	s := out.Skills[0]
	if s.Name != "writing" || s.Title != "Writing" || s.Description != "House style" || s.SizeBytes != 1234 || s.Owner != "user-9" {
		t.Errorf("skill[0] wrong: %+v", s)
	}
	if len(s.Folders) != 1 || s.Folders[0].Slug != "product" || !s.Folders[0].IsPreset {
		t.Errorf("skill[0] folders wrong: %+v", s.Folders)
	}
	if out.Skills[1].Owner != "Dropway" {
		t.Errorf("seed-owned skill should render owner 'Dropway', got %q", out.Skills[1].Owner)
	}
}

func TestListSkills_ForwardsFilters(t *testing.T) {
	skills := &fakeSkills{}
	svc := &Service{Skills: skills}
	if _, err := svc.ListSkills(context.Background(), tnt, "style", "product", true); err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if skills.listQuery != "style" || skills.listFolder != "product" || !skills.listPresetsOnly {
		t.Errorf("filters not forwarded to the store: query=%q folder=%q presets=%v",
			skills.listQuery, skills.listFolder, skills.listPresetsOnly)
	}
}

func TestListSkills_ExposesVersion(t *testing.T) {
	skills := &fakeSkills{skills: []store.Skill{
		{ID: "sk1", Slug: "writing", CurrentVersionID: ptr("v3"), Version: 3},
	}}
	svc := &Service{Skills: skills}
	out, err := svc.ListSkills(context.Background(), tnt, "", "", false)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if out.Skills[0].Version != 3 {
		t.Errorf("want version 3, got %d", out.Skills[0].Version)
	}
}

func TestCheckSkillUpdates(t *testing.T) {
	skills := &fakeSkills{skills: []store.Skill{
		{Slug: "writing", Version: 3},
		{Slug: "review", Version: 1},
	}}
	svc := &Service{Skills: skills}
	out, err := svc.CheckSkillUpdates(context.Background(), tnt, checkSkillUpdatesIn{Installed: []installedSkill{
		{Name: "writing", Version: 1}, // behind (3 > 1) → outdated
		{Name: "review", Version: 1},  // up to date
		{Name: "gone", Version: 2},    // no longer in the org → latest 0, not outdated
	}})
	if err != nil {
		t.Fatalf("CheckSkillUpdates: %v", err)
	}
	byName := map[string]skillUpdateInfo{}
	for _, u := range out.Updates {
		byName[u.Name] = u
	}
	if u := byName["writing"]; !u.Outdated || u.LatestVersion != 3 || u.InstalledVersion != 1 {
		t.Errorf("writing update wrong: %+v", u)
	}
	if u := byName["review"]; u.Outdated || u.LatestVersion != 1 {
		t.Errorf("review should be up to date: %+v", u)
	}
	if u := byName["gone"]; u.Outdated || u.LatestVersion != 0 {
		t.Errorf("removed skill should not be outdated: %+v", u)
	}
}

// --- download_skill -----------------------------------------------------------

const skillManifestJSON = `{"schema_version":1,"files":{
	"SKILL.md":{"sha256":"sm","content_type":"text/markdown","size":11},
	"assets/logo.png":{"sha256":"sp","content_type":"image/png","size":4}
}}`

func skillFixture() (*fakeSkills, *fakeBlobs) {
	skills := &fakeSkills{bySlug: map[string]store.Skill{
		"writing": {ID: "sk1", Slug: "writing", CurrentVersionID: ptr("v1"), Version: 2},
	}}
	blobs := &fakeBlobs{
		skillManifests: map[string][]byte{"sk1/v1": []byte(skillManifestJSON)},
		blobs: map[string][]byte{
			"sm": []byte("# a skill\n"),    // valid utf8
			"sp": {0xff, 0xd8, 0xff, 0x00}, // binary
		},
	}
	return skills, blobs
}

func TestDownloadSkill_TextAndBinaryEncoding(t *testing.T) {
	skills, blobs := skillFixture()
	svc := &Service{Skills: skills, Blobs: blobs}

	out, err := svc.DownloadSkill(context.Background(), tnt, "writing")
	if err != nil {
		t.Fatalf("DownloadSkill: %v", err)
	}
	if out.Name != "writing" || out.Truncated {
		t.Fatalf("out wrong: %+v", out)
	}
	// The download carries the current version so a client can record it and later
	// detect updates (via check_skill_updates).
	if out.Version != 2 {
		t.Errorf("download should carry version 2, got %d", out.Version)
	}
	if len(out.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(out.Files))
	}
	// Sorted by path: SKILL.md, assets/logo.png.
	if out.Files[0].Path != "SKILL.md" || out.Files[0].Encoding != "utf8" || out.Files[0].Content != "# a skill\n" {
		t.Errorf("SKILL.md wrong: %+v", out.Files[0])
	}
	if out.Files[1].Path != "assets/logo.png" || out.Files[1].Encoding != "base64" || out.Files[1].Content == "" {
		t.Errorf("binary file should be base64: %+v", out.Files[1])
	}
}

func TestDownloadSkill_UnknownSkill(t *testing.T) {
	svc := &Service{Skills: &fakeSkills{bySlug: map[string]store.Skill{}}}
	if _, err := svc.DownloadSkill(context.Background(), tnt, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for unknown skill, got %v", err)
	}
}

func TestDownloadSkill_NoContentErrors(t *testing.T) {
	svc := &Service{Skills: &fakeSkills{bySlug: map[string]store.Skill{
		"empty": {ID: "sk9", Slug: "empty", CurrentVersionID: nil},
	}}}
	if _, err := svc.DownloadSkill(context.Background(), tnt, "empty"); err == nil {
		t.Fatal("expected an error for a skill with no finalized upload")
	}
}

func TestDownloadSkill_UnsafeManifestPathRejected(t *testing.T) {
	skills, blobs := skillFixture()
	blobs.skillManifests["sk1/v1"] = []byte(`{"schema_version":1,"files":{
		"SKILL.md":{"sha256":"sm","size":11},
		"../evil.md":{"sha256":"sm","size":11}
	}}`)
	svc := &Service{Skills: skills, Blobs: blobs}
	if _, err := svc.DownloadSkill(context.Background(), tnt, "writing"); err == nil {
		t.Fatal("expected an error for a manifest path containing '..'")
	}
}

// A filename that merely CONTAINS ".." (but has no ".." path SEGMENT), e.g.
// "changelog..md", is a valid server-side path and must download fine. The old
// strings.Contains(p, "..") check wrongly rejected these, hard-failing the whole
// skill; the CleanPath rule (segment-based) accepts them. Regression guard for BUG 1.
func TestDownloadSkill_DottedFilenameAllowed(t *testing.T) {
	skills := &fakeSkills{bySlug: map[string]store.Skill{
		"writing": {ID: "sk1", Slug: "writing", CurrentVersionID: ptr("v1")},
	}}
	blobs := &fakeBlobs{
		skillManifests: map[string][]byte{"sk1/v1": []byte(`{"schema_version":1,"files":{
			"SKILL.md":{"sha256":"sm","content_type":"text/markdown","size":11},
			"changelog..md":{"sha256":"cl","content_type":"text/markdown","size":3}
		}}`)},
		blobs: map[string][]byte{
			"sm": []byte("# a skill\n"),
			"cl": []byte("log"),
		},
	}
	svc := &Service{Skills: skills, Blobs: blobs}
	out, err := svc.DownloadSkill(context.Background(), tnt, "writing")
	if err != nil {
		t.Fatalf("DownloadSkill: %v", err)
	}
	if len(out.Files) != 2 {
		t.Fatalf("want 2 files (incl. changelog..md), got %d: %+v", len(out.Files), out.Files)
	}
	// Sorted: SKILL.md, changelog..md.
	if out.Files[1].Path != "changelog..md" || out.Files[1].Content != "log" {
		t.Errorf("dotted filename not downloaded: %+v", out.Files[1])
	}
}

func TestDownloadSkill_OverCapTruncated(t *testing.T) {
	orig := maxDownloadBytes
	maxDownloadBytes = 8 // below the manifest's 15 declared bytes
	defer func() { maxDownloadBytes = orig }()

	skills, blobs := skillFixture()
	svc := &Service{Skills: skills, Blobs: blobs}
	out, err := svc.DownloadSkill(context.Background(), tnt, "writing")
	if err != nil {
		t.Fatalf("DownloadSkill: %v", err)
	}
	if !out.Truncated || len(out.Files) != 0 {
		t.Errorf("an over-cap skill should be truncated with no files: %+v", out)
	}
}

// --- download_skill_folder ------------------------------------------------------

func TestDownloadSkillFolder_SharedCapTruncation(t *testing.T) {
	orig := maxDownloadBytes
	maxDownloadBytes = 20 // fits skill a (15 declared bytes) but not also skill b
	defer func() { maxDownloadBytes = orig }()

	skills := &fakeSkills{
		folders: []store.SkillFolder{{ID: "f1", Slug: "product", Title: "Product", ItemCount: 2}},
		folderSkills: map[string][]store.Skill{"f1": {
			{ID: "sk1", Slug: "a", CurrentVersionID: ptr("v1")},
			{ID: "sk2", Slug: "b", CurrentVersionID: ptr("v2")},
		}},
	}
	blobs := &fakeBlobs{
		skillManifests: map[string][]byte{
			"sk1/v1": []byte(skillManifestJSON), // 15 declared bytes
			"sk2/v2": []byte(skillManifestJSON), // another 15 → over the 20-byte budget
		},
		blobs: map[string][]byte{
			"sm": []byte("# a skill\n"),
			"sp": {0xff, 0xd8, 0xff, 0x00},
		},
	}
	svc := &Service{Skills: skills, Blobs: blobs}

	out, err := svc.DownloadSkillFolder(context.Background(), tnt, "product")
	if err != nil {
		t.Fatalf("DownloadSkillFolder: %v", err)
	}
	if out.Folder != "product" || len(out.Skills) != 2 {
		t.Fatalf("out wrong: %+v", out)
	}
	if out.Skills[0].Name != "a" || out.Skills[0].Truncated || len(out.Skills[0].Files) != 2 {
		t.Errorf("skill a should be fully included: %+v", out.Skills[0])
	}
	if out.Skills[1].Name != "b" || !out.Skills[1].Truncated || len(out.Skills[1].Files) != 0 {
		t.Errorf("skill b should be truncated with no files: %+v", out.Skills[1])
	}
	if out.Note == "" {
		t.Error("a truncated folder download should carry a note pointing at download_skill")
	}
}

func TestDownloadSkillFolder_UnknownFolder(t *testing.T) {
	svc := &Service{Skills: &fakeSkills{}}
	if _, err := svc.DownloadSkillFolder(context.Background(), tnt, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for unknown folder, got %v", err)
	}
}

// --- upload_skill ---------------------------------------------------------------

func TestUploadSkill_CreatesWhenAbsent(t *testing.T) {
	api := &fakeAPI{
		createSkillResp: apiclient.SkillInfo{ID: "sk-new", Slug: "writing"},
		uploadResp:      apiclient.UploadResult{VersionID: "v1", VersionNo: 1, Warnings: []string{"w"}},
	}
	skills := &fakeSkills{
		bySlug:  map[string]store.Skill{}, // absent → create
		folders: []store.SkillFolder{{ID: "f1", Slug: "product", Title: "Product"}},
	}
	svc := &Service{Skills: skills, API: api}

	in := uploadSkillIn{
		Name:    "writing",
		Title:   "Writing",
		Folders: []string{"product"},
		Files: []uploadSkillFileIn{
			{Path: "SKILL.md", Content: "# hi"},
			{Path: "assets/logo.png", Content: "AAEC", Encoding: "base64"}, // 0x00 0x01 0x02
		},
	}
	out, err := svc.UploadSkill(context.Background(), tnt, "tok-5", in)
	if err != nil {
		t.Fatalf("UploadSkill: %v", err)
	}
	// Create forwarded the token, slug, and title — but NO folders (the create is
	// what triggers seeding; folders are applied afterwards via SetSkillFolders).
	if api.createSkillToken != "tok-5" || api.createSkillSlug != "writing" || api.createSkillTitle != "Writing" {
		t.Errorf("create args wrong: %+v", api)
	}
	if len(api.createSkillFolders) != 0 {
		t.Errorf("create should be called with no folders, got: %v", api.createSkillFolders)
	}
	// Upload ran against the created skill under the same token.
	if api.uploadToken != "tok-5" || api.uploadSkillID != "sk-new" {
		t.Errorf("upload args wrong: token=%q skill=%q", api.uploadToken, api.uploadSkillID)
	}
	// Folders were resolved (post-seed) and applied to the created skill.
	if api.setFoldersToken != "tok-5" || api.setFoldersSkillID != "sk-new" {
		t.Errorf("SetSkillFolders args wrong: token=%q skill=%q", api.setFoldersToken, api.setFoldersSkillID)
	}
	if len(api.setFoldersIDs) != 1 || api.setFoldersIDs[0] != "f1" {
		t.Errorf("folder slug not resolved to id: %v", api.setFoldersIDs)
	}
	// Files decoded: text → raw bytes, base64 → decoded bytes.
	if len(api.uploadFiles) != 2 || string(api.uploadFiles[0].Content) != "# hi" {
		t.Fatalf("upload files wrong: %+v", api.uploadFiles)
	}
	if want := []byte{0x00, 0x01, 0x02}; !bytes.Equal(api.uploadFiles[1].Content, want) {
		t.Errorf("base64 file decoded wrong: %v, want %v", api.uploadFiles[1].Content, want)
	}
	if out.Name != "writing" || out.SkillID != "sk-new" || out.VersionNo != 1 || len(out.Warnings) != 1 {
		t.Errorf("result wrong: %+v", out)
	}
}

func TestUploadSkill_ReusesExistingSkill(t *testing.T) {
	api := &fakeAPI{uploadResp: apiclient.UploadResult{VersionID: "v2", VersionNo: 2}}
	skills := &fakeSkills{bySlug: map[string]store.Skill{
		"writing": {ID: "sk-old", Slug: "writing", CurrentVersionID: ptr("v1")},
	}}
	svc := &Service{Skills: skills, API: api}

	out, err := svc.UploadSkill(context.Background(), tnt, "tok", uploadSkillIn{
		Name:  "writing",
		Files: []uploadSkillFileIn{{Path: "SKILL.md", Content: "# v2"}},
	})
	if err != nil {
		t.Fatalf("UploadSkill: %v", err)
	}
	if api.createSkillSlug != "" {
		t.Error("create must not be called when the skill already exists")
	}
	if api.uploadSkillID != "sk-old" {
		t.Errorf("upload should target the existing skill: %q", api.uploadSkillID)
	}
	if api.setFoldersCalls != 0 {
		t.Error("a re-upload (reuse) must not re-file the skill's folders")
	}
	if out.SkillID != "sk-old" || out.VersionNo != 2 {
		t.Errorf("result wrong: %+v", out)
	}
}

func TestUploadSkill_RequiresRootSkillMD(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{Skills: &fakeSkills{bySlug: map[string]store.Skill{}}, API: api}
	_, err := svc.UploadSkill(context.Background(), tnt, "tok", uploadSkillIn{
		Name:  "writing",
		Files: []uploadSkillFileIn{{Path: "notes.md", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected an error without a root SKILL.md")
	}
	if api.createSkillSlug != "" || api.uploadSkillID != "" {
		t.Error("API must not be called when SKILL.md is missing")
	}
}

// An unknown folder slug is now caught AFTER the create+upload (the create is what
// seeds the org's folders, so resolution must run post-seed). The error still lists
// the available slugs — which are non-empty precisely because seeding has run — and
// SetSkillFolders is never called for the bad slug.
func TestUploadSkill_UnknownFolderListsAvailable(t *testing.T) {
	api := &fakeAPI{createSkillResp: apiclient.SkillInfo{ID: "sk-new", Slug: "writing"}}
	svc := &Service{
		Skills: &fakeSkills{
			bySlug:  map[string]store.Skill{},
			folders: []store.SkillFolder{{ID: "f1", Slug: "product"}},
		},
		API: api,
	}
	_, err := svc.UploadSkill(context.Background(), tnt, "tok", uploadSkillIn{
		Name:    "writing",
		Folders: []string{"ghost"},
		Files:   []uploadSkillFileIn{{Path: "SKILL.md", Content: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "product") {
		t.Fatalf("want an unknown-folder error listing available folders, got %v", err)
	}
	if api.setFoldersCalls != 0 {
		t.Error("SetSkillFolders must not be called for an unknown folder slug")
	}
}

func TestUploadSkill_UnsafePathRejected(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{Skills: &fakeSkills{bySlug: map[string]store.Skill{}}, API: api}
	_, err := svc.UploadSkill(context.Background(), tnt, "tok", uploadSkillIn{
		Name:  "writing",
		Files: []uploadSkillFileIn{{Path: "SKILL.md", Content: "x"}, {Path: "../escape.md", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected an error for a path containing '..'")
	}
	if api.uploadSkillID != "" {
		t.Error("API must not be called for an unsafe path")
	}
}

// --- share_chat -----------------------------------------------------------------

func TestShareChat_ResolvesSiteAndMaps(t *testing.T) {
	api := &fakeAPI{chatCreateResp: apiclient.ChatCreateResult{
		ChatLog:  apiclient.ChatLogInfo{ID: "chat-1", Title: "Build log"},
		Appended: 3, Pruned: 1, Window: 50, Dropped: 2,
	}}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{
			"docs": {ID: "site-1", Slug: "docs", Host: ptr("acme--docs.dropwaycontent.com")},
		}},
		API: api,
	}
	in := shareChatIn{
		Site:       "docs",
		Title:      "Build log",
		SourceTool: "claude_code",
		Transcript: "User: hi\nAssistant: hello",
		Format:     "text",
		Messages: []chatMessageIn{
			{Content: "done", Role: "assistant"},
			{Kind: "action", Content: "wired the nav", Meta: &chatActionMeta{Action: "file_edit", Paths: []string{"index.html"}}},
		},
	}
	out, err := svc.ShareChat(context.Background(), tnt, "tok-3", in)
	if err != nil {
		t.Fatalf("ShareChat: %v", err)
	}
	// Slug resolved to id; token + metadata forwarded.
	if api.chatCreateToken != "tok-3" || api.chatCreateSiteID != "site-1" ||
		api.chatCreateTitle != "Build log" || api.chatCreateSource != "claude_code" {
		t.Errorf("create args wrong: %+v", api)
	}
	// Import payload carried through, incl. the action meta conversion.
	if api.chatCreateImport.Transcript == "" || api.chatCreateImport.Format != "text" {
		t.Errorf("transcript not forwarded: %+v", api.chatCreateImport)
	}
	if len(api.chatCreateImport.Messages) != 2 {
		t.Fatalf("want 2 messages, got %d", len(api.chatCreateImport.Messages))
	}
	if m := api.chatCreateImport.Messages[1]; m.Kind != "action" || m.Meta == nil ||
		m.Meta.Action != "file_edit" || len(m.Meta.Paths) != 1 || m.Meta.Paths[0] != "index.html" {
		t.Errorf("action meta not converted: %+v", m)
	}
	// Result mapped, with a viewer hint pointing at the site.
	if out.ChatID != "chat-1" || out.Site != "docs" || out.Appended != 3 || out.Pruned != 1 || out.Window != 50 || out.Dropped != 2 {
		t.Errorf("result not mapped: %+v", out)
	}
	if !strings.Contains(out.ViewerHint, "How this was made") || !strings.Contains(out.ViewerHint, "acme--docs.dropwaycontent.com") {
		t.Errorf("viewer hint should name the panel and the site URL: %q", out.ViewerHint)
	}
}

func TestShareChat_UnattachedSkipsSiteLookup(t *testing.T) {
	api := &fakeAPI{chatCreateResp: apiclient.ChatCreateResult{ChatLog: apiclient.ChatLogInfo{ID: "chat-2"}}}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}, API: api}
	out, err := svc.ShareChat(context.Background(), tnt, "tok", shareChatIn{Transcript: "hi"})
	if err != nil {
		t.Fatalf("ShareChat: %v", err)
	}
	if api.chatCreateSiteID != "" {
		t.Errorf("unattached share must pass no site_id, got %q", api.chatCreateSiteID)
	}
	if out.ChatID != "chat-2" || out.Site != "" {
		t.Errorf("result wrong: %+v", out)
	}
	if !strings.Contains(out.ViewerHint, "attach") {
		t.Errorf("unattached hint should point at attaching: %q", out.ViewerHint)
	}
}

func TestShareChat_UnknownSiteDoesNotCallAPI(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}, API: api}
	if _, err := svc.ShareChat(context.Background(), tnt, "tok", shareChatIn{Site: "ghost"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for unknown site, got %v", err)
	}
	if api.chatCreateToken != "" {
		t.Error("API must not be called when the slug doesn't resolve")
	}
}

func TestShareChat_APIErrorPropagates(t *testing.T) {
	api := &fakeAPI{chatCreateErr: &apiclient.Error{Status: 402, Message: "chat log limit reached"}}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}, API: api}
	if _, err := svc.ShareChat(context.Background(), tnt, "tok", shareChatIn{Transcript: "hi"}); err == nil {
		t.Fatal("expected the API error to propagate")
	}
}

// share_chat's input schema must publish `messages` (and the nested `paths`) as
// plain arrays (see the deploy_site regression guard).
func TestShareChat_SchemaHasPlainTypes(t *testing.T) {
	s := inputSchema[shareChatIn]()
	msgs := s.Properties["messages"]
	if msgs == nil || msgs.Type != "array" || len(msgs.Types) != 0 {
		t.Fatalf("messages type = %q / %v, want plain \"array\"", msgs.Type, msgs.Types)
	}
	if msgs.Items == nil || msgs.Items.Type != "object" {
		t.Fatalf("messages.items should be an object schema, got %+v", msgs.Items)
	}
}

// --- append_chat ------------------------------------------------------------------

func TestAppendChat_BySiteResolvesSlug(t *testing.T) {
	api := &fakeAPI{siteChatResp: apiclient.ChatAppendResult{Appended: 2, Pruned: 1, Window: 50}}
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "site-1", Slug: "docs"}}},
		API:   api,
	}
	in := appendChatIn{
		Site: "docs",
		Messages: []chatMessageIn{
			{Content: "adding a pricing section", Role: "assistant"},
			{Kind: "action", Content: "kept the hero copy short on purpose", Meta: &chatActionMeta{Action: "tool_use", Tool: "deploy_site"}},
		},
	}
	out, err := svc.AppendChat(context.Background(), tnt, "tok-4", in)
	if err != nil {
		t.Fatalf("AppendChat: %v", err)
	}
	if api.siteChatToken != "tok-4" || api.siteChatSiteID != "site-1" {
		t.Errorf("site append args wrong: %+v", api)
	}
	if api.chatAppendID != "" {
		t.Error("the chat_id endpoint must not be called for a site append")
	}
	if m := api.siteChatImport.Messages[1]; m.Meta == nil || m.Meta.Action != "tool_use" || m.Meta.Tool != "deploy_site" {
		t.Errorf("action meta not converted: %+v", m)
	}
	if out.Site != "docs" || out.ChatID != "" || out.Appended != 2 || out.Pruned != 1 || out.Window != 50 {
		t.Errorf("result wrong: %+v", out)
	}
}

func TestAppendChat_ByChatID(t *testing.T) {
	api := &fakeAPI{chatAppendResp: apiclient.ChatAppendResult{Appended: 1}}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}, API: api}
	out, err := svc.AppendChat(context.Background(), tnt, "tok", appendChatIn{
		ChatID:   "chat-7",
		Messages: []chatMessageIn{{Content: "hi", Role: "user"}},
	})
	if err != nil {
		t.Fatalf("AppendChat: %v", err)
	}
	if api.chatAppendID != "chat-7" || api.chatAppendToken != "tok" {
		t.Errorf("chat append args wrong: %+v", api)
	}
	if api.siteChatSiteID != "" {
		t.Error("the site endpoint must not be called for a chat_id append")
	}
	if out.ChatID != "chat-7" || out.Appended != 1 {
		t.Errorf("result wrong: %+v", out)
	}
}

func TestAppendChat_ExactlyOneTarget(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs"}}}, API: api}
	msgs := []chatMessageIn{{Content: "x", Role: "user"}}
	if _, err := svc.AppendChat(context.Background(), tnt, "tok", appendChatIn{Messages: msgs}); err == nil {
		t.Fatal("expected an error when neither site nor chat_id is set")
	}
	if _, err := svc.AppendChat(context.Background(), tnt, "tok", appendChatIn{Site: "docs", ChatID: "c1", Messages: msgs}); err == nil {
		t.Fatal("expected an error when both site and chat_id are set")
	}
	if api.chatAppendID != "" || api.siteChatSiteID != "" {
		t.Error("API must not be called for an ambiguous target")
	}
}

func TestAppendChat_RequiresPayload(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "s1", Slug: "docs"}}}, API: api}
	if _, err := svc.AppendChat(context.Background(), tnt, "tok", appendChatIn{Site: "docs"}); err == nil {
		t.Fatal("expected an error for an append with no messages and no transcript")
	}
	if api.siteChatSiteID != "" {
		t.Error("API must not be called for an empty payload")
	}
}

func TestAppendChat_UnknownSiteDoesNotCallAPI(t *testing.T) {
	api := &fakeAPI{}
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}, API: api}
	_, err := svc.AppendChat(context.Background(), tnt, "tok", appendChatIn{
		Site: "ghost", Messages: []chatMessageIn{{Content: "x", Role: "user"}},
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for unknown site, got %v", err)
	}
	if api.siteChatSiteID != "" {
		t.Error("API must not be called when the slug doesn't resolve")
	}
}

// --- get_site_chat ----------------------------------------------------------------

func siteChatFixture() (*fakeStore, *fakeChats) {
	st := &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "site-1", Slug: "docs"}}}
	chats := &fakeChats{
		bySite: map[string]store.ChatLog{"site-1": {
			ID: "chat-1", SiteID: ptr("site-1"), Title: "Build log", SourceTool: "claude_code",
			PanelEnabled: true, MessageCount: 3, CreatedBy: "user-1",
			CreatedAt: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC),
		}},
		msgs: map[string][]store.ChatMessage{"chat-1": {
			{Seq: 1, Role: "user", Kind: "chat", Content: "build me a docs site",
				CreatedAt: time.Date(2026, 7, 14, 10, 1, 0, 0, time.UTC)},
			{Seq: 2, Role: "assistant", Kind: "action", Content: "scaffolded the layout",
				Meta:      []byte(`{"action":"file_edit","paths":["index.html","style.css"]}`),
				CreatedAt: time.Date(2026, 7, 14, 10, 2, 0, 0, time.UTC)},
			{Seq: 3, Role: "assistant", Kind: "chat", Content: "done!",
				CreatedAt: time.Date(2026, 7, 14, 10, 3, 0, 0, time.UTC)},
		}},
	}
	return st, chats
}

func TestGetSiteChat_MapsLogAndMessages(t *testing.T) {
	st, chats := siteChatFixture()
	svc := &Service{Store: st, Chats: chats}

	out, err := svc.GetSiteChat(context.Background(), tnt, "docs")
	if err != nil {
		t.Fatalf("GetSiteChat: %v", err)
	}
	if out.Site != "docs" || out.Truncated {
		t.Fatalf("out wrong: %+v", out)
	}
	l := out.ChatLog
	if l.ChatID != "chat-1" || l.Title != "Build log" || l.SourceTool != "claude_code" ||
		!l.PanelEnabled || l.MessageCount != 3 || l.CreatedAt != "2026-07-14T10:00:00Z" {
		t.Errorf("chat log wrong: %+v", l)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(out.Messages))
	}
	if m := out.Messages[0]; m.Seq != 1 || m.Role != "user" || m.Kind != "chat" || m.Meta != nil {
		t.Errorf("message[0] wrong: %+v", m)
	}
	// The action row's jsonb meta decodes into the structured shape.
	if m := out.Messages[1]; m.Kind != "action" || m.Meta == nil ||
		m.Meta.Action != "file_edit" || len(m.Meta.Paths) != 2 || m.Meta.Paths[1] != "style.css" {
		t.Errorf("action meta not decoded: %+v", m)
	}
}

func TestGetSiteChat_UnknownSite(t *testing.T) {
	svc := &Service{Store: &fakeStore{bySlug: map[string]store.Site{}}, Chats: &fakeChats{}}
	if _, err := svc.GetSiteChat(context.Background(), tnt, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for unknown site, got %v", err)
	}
}

func TestGetSiteChat_NoAttachedLog(t *testing.T) {
	svc := &Service{
		Store: &fakeStore{bySlug: map[string]store.Site{"docs": {ID: "site-1", Slug: "docs"}}},
		Chats: &fakeChats{bySite: map[string]store.ChatLog{}},
	}
	if _, err := svc.GetSiteChat(context.Background(), tnt, "docs"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for a site with no attached log, got %v", err)
	}
}

func TestGetSiteChat_TruncatedPastCap(t *testing.T) {
	orig := maxChatBytes
	maxChatBytes = 25 // fits the first message (20 bytes) but not also the second
	defer func() { maxChatBytes = orig }()

	st, chats := siteChatFixture()
	svc := &Service{Store: st, Chats: chats}
	out, err := svc.GetSiteChat(context.Background(), tnt, "docs")
	if err != nil {
		t.Fatalf("GetSiteChat: %v", err)
	}
	if !out.Truncated {
		t.Fatal("expected Truncated=true past the size cap")
	}
	if len(out.Messages) != 1 || out.Messages[0].Seq != 1 {
		t.Errorf("expected only the first message under the cap, got %+v", out.Messages)
	}
}

// upload_skill's input schema must publish `files`/`folders` as plain arrays
// (see the deploy_site regression guard).
func TestUploadSkill_SchemaHasPlainTypes(t *testing.T) {
	s := inputSchema[uploadSkillIn]()
	files := s.Properties["files"]
	if files == nil || files.Type != "array" || len(files.Types) != 0 {
		t.Fatalf("files type = %q / %v, want plain \"array\"", files.Type, files.Types)
	}
	folders := s.Properties["folders"]
	if folders == nil || folders.Type != "array" || len(folders.Types) != 0 {
		t.Fatalf("folders type = %q / %v, want plain \"array\"", folders.Type, folders.Types)
	}
}
