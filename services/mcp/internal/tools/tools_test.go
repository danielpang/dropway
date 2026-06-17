// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

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
