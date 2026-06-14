package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielpang/shipped/cli/internal/api"
)

// errClient is a Client whose steps can be made to fail, so the deploy command's
// error-wrapping branches (create/prepare/finalize/publish) are exercised.
type errClient struct {
	createErr   error
	prepareErr  error
	finalizeErr error
	publishErr  error
	missing     []string
}

func (c *errClient) CreateSite(_ context.Context, req api.CreateSiteRequest) (*api.Site, error) {
	if c.createErr != nil {
		return nil, c.createErr
	}
	return &api.Site{ID: "site_" + req.Slug, Slug: req.Slug}, nil
}

func (c *errClient) PrepareDeployment(_ context.Context, _ string, _ api.PrepareRequest) (*api.PrepareResponse, error) {
	if c.prepareErr != nil {
		return nil, c.prepareErr
	}
	uploads := map[string]string{}
	for _, s := range c.missing {
		uploads[s] = "https://presign.local/" + s
	}
	return &api.PrepareResponse{Missing: c.missing, Uploads: uploads}, nil
}

func (c *errClient) UploadBlob(_ context.Context, _ string, _ []byte) error { return nil }

func (c *errClient) FinalizeDeployment(_ context.Context, _ string, _ api.FinalizeRequest) (*api.FinalizeResponse, error) {
	if c.finalizeErr != nil {
		return nil, c.finalizeErr
	}
	return &api.FinalizeResponse{VersionID: "ver_1", VersionNo: 1}, nil
}

func (c *errClient) Publish(_ context.Context, _ string, _ api.PublishRequest) (*api.PublishResponse, error) {
	if c.publishErr != nil {
		return nil, c.publishErr
	}
	return &api.PublishResponse{LiveURL: "https://x.shippedusercontent.com", VersionID: "ver_1"}, nil
}

// --- --new flag validation ---------------------------------------------------

func TestDeploy_New_RequiresSlug(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("SHIPPED_TOKEN", "shpd_test")
	factory := func(string, string) api.Client { return newFakeClient(nil) }
	// --new without --site <slug> must error.
	_, err := runDeploy(t, factory, dir, "--send", "--new")
	if err == nil || !strings.Contains(err.Error(), "--site") {
		t.Fatalf("err = %v, want a '--new requires --site' error", err)
	}
}

// --- per-step error wrapping -------------------------------------------------

func TestDeploy_CreateError_Wrapped(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("SHIPPED_TOKEN", "shpd_test")
	factory := func(string, string) api.Client { return &errClient{createErr: errors.New("slug taken")} }
	_, err := runDeploy(t, factory, dir, "--send", "--new", "--site", "dup")
	if err == nil || !strings.Contains(err.Error(), "create site") || !strings.Contains(err.Error(), "slug taken") {
		t.Fatalf("err = %v, want a wrapped create-site error", err)
	}
}

func TestDeploy_PrepareError_Wrapped(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("SHIPPED_TOKEN", "shpd_test")
	factory := func(string, string) api.Client { return &errClient{prepareErr: errors.New("boom")} }
	_, err := runDeploy(t, factory, dir, "--send", "--site-id", "s1")
	if err == nil || !strings.Contains(err.Error(), "prepare") {
		t.Fatalf("err = %v, want a wrapped prepare error", err)
	}
}

func TestDeploy_FinalizeError_Wrapped(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("SHIPPED_TOKEN", "shpd_test")
	factory := func(string, string) api.Client { return &errClient{finalizeErr: errors.New("verify failed")} }
	_, err := runDeploy(t, factory, dir, "--send", "--site-id", "s1")
	if err == nil || !strings.Contains(err.Error(), "finalize") {
		t.Fatalf("err = %v, want a wrapped finalize error", err)
	}
}

func TestDeploy_PublishError_Wrapped(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("SHIPPED_TOKEN", "shpd_test")
	factory := func(string, string) api.Client { return &errClient{publishErr: errors.New("pointer flip failed")} }
	_, err := runDeploy(t, factory, dir, "--send", "--site-id", "s1")
	if err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("err = %v, want a wrapped publish error", err)
	}
}

// --- uploadMissing error branches (no matching local file / missing URL) -----

func TestUploadMissing_NoLocalFileForSHA(t *testing.T) {
	// The server reports a sha missing that no local file in the manifest backs.
	prep := &api.PrepareResponse{
		Missing: []string{"deadbeef"},
		Uploads: map[string]string{"deadbeef": "https://presign.local/deadbeef"},
	}
	m := fakeManifest("index.html", "abc123") // the only local file hashes to abc123
	err := uploadMissing(context.Background(), newFakeClient(nil), t.TempDir(), m, prep)
	if err == nil || !strings.Contains(err.Error(), "no local file matches blob") {
		t.Fatalf("err = %v, want a 'no local file matches' error", err)
	}
}

func TestUploadMissing_MissingUploadURL(t *testing.T) {
	// The server lists a sha missing but gives no upload URL for it.
	prep := &api.PrepareResponse{Missing: []string{"abc123"}, Uploads: map[string]string{}}
	m := fakeManifest("index.html", "abc123")
	err := uploadMissing(context.Background(), newFakeClient(nil), t.TempDir(), m, prep)
	if err == nil || !strings.Contains(err.Error(), "gave no upload URL") {
		t.Fatalf("err = %v, want a 'no upload URL' error", err)
	}
}

func TestUploadMissing_ReadFileError(t *testing.T) {
	// The manifest references a file that doesn't exist on disk → a read error.
	dir := t.TempDir()
	prep := &api.PrepareResponse{
		Missing: []string{"abc123"},
		Uploads: map[string]string{"abc123": "https://presign.local/abc123"},
	}
	m := fakeManifest("ghost.html", "abc123") // not written to dir
	err := uploadMissing(context.Background(), newFakeClient(nil), dir, m, prep)
	if err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("err = %v, want a read error for the missing file", err)
	}
}

func TestUploadMissing_HappyPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	prep := &api.PrepareResponse{
		Missing: []string{"abc123"},
		Uploads: map[string]string{"abc123": "https://presign.local/abc123"},
	}
	m := fakeManifest("index.html", "abc123")
	fc := newFakeClient(nil)
	if err := uploadMissing(context.Background(), fc, dir, m, prep); err != nil {
		t.Fatalf("uploadMissing: %v", err)
	}
	if fc.uploaded["https://presign.local/abc123"] != len("hello") {
		t.Errorf("expected the file's bytes uploaded, got %v", fc.uploaded)
	}
}
