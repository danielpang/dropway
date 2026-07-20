package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/api"
)

// fakeClient records the calls it received and returns canned responses,
// simulating the full prepare→upload→finalize→publish server flow in-memory.
type fakeClient struct {
	createdSlug string
	prepared    api.PrepareRequest
	uploaded    map[string]int // presigned URL → bytes uploaded
	finalized   api.FinalizeRequest
	published   api.PublishRequest

	missing []string // shas to report missing on prepare
}

func newFakeClient(missing []string) *fakeClient {
	return &fakeClient{uploaded: map[string]int{}, missing: missing}
}

func (f *fakeClient) CreateSite(_ context.Context, req api.CreateSiteRequest) (*api.Site, error) {
	f.createdSlug = req.Slug
	return &api.Site{ID: "site_" + req.Slug, Slug: req.Slug, LiveURL: "https://" + req.Slug + ".dropwaycontent.com"}, nil
}

func (f *fakeClient) PrepareDeployment(_ context.Context, siteID string, req api.PrepareRequest) (*api.PrepareResponse, error) {
	f.prepared = req
	uploads := map[string]string{}
	for _, sha := range f.missing {
		uploads[sha] = "https://fake-presign.local/blobs/org/" + sha
	}
	return &api.PrepareResponse{Missing: f.missing, Uploads: uploads}, nil
}

func (f *fakeClient) UploadBlob(_ context.Context, url string, data []byte) error {
	f.uploaded[url] = len(data)
	return nil
}

func (f *fakeClient) FinalizeDeployment(_ context.Context, siteID string, req api.FinalizeRequest) (*api.FinalizeResponse, error) {
	f.finalized = req
	return &api.FinalizeResponse{VersionID: "ver_1", VersionNo: 1, PreviewURL: "https://preview"}, nil
}

func (f *fakeClient) Publish(_ context.Context, siteID string, req api.PublishRequest) (*api.PublishResponse, error) {
	f.published = req
	return &api.PublishResponse{LiveURL: "https://" + strings.TrimPrefix(siteID, "site_") + ".dropwaycontent.com", VersionID: req.VersionID}, nil
}

func tempSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runDeploy(t *testing.T, factory func(string, string) api.Client, args ...string) (string, error) {
	t.Helper()
	cmd := newDeployCmd(factory)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestDeploy_DryRun_PrintsManifest_NoNetwork(t *testing.T) {
	dir := tempSite(t)
	called := false
	factory := func(string, string) api.Client {
		called = true
		return newFakeClient(nil)
	}

	out, err := runDeploy(t, factory, dir)
	if err != nil {
		t.Fatalf("deploy dry run: %v", err)
	}
	if called {
		t.Error("dry run must NOT construct/use a network client")
	}
	if !strings.Contains(out, "index.html") {
		t.Error("output should include the manifest with index.html")
	}
	if !strings.Contains(out, "dry run") {
		t.Error("dry run hint should be printed")
	}
}

// TestDeploy_FullFlow_NewSite drives the entire create→prepare→upload→finalize→
// publish flow against the fake client and asserts each step ran with the right
// data and the live URL is printed.
func TestDeploy_FullFlow_NewSite(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("DROPWAY_API_KEY", "shpd_test")

	// The single file's sha is reported missing so the upload step runs.
	idxSHA := sha256Hex(t, filepath.Join(dir, "index.html"))
	fc := newFakeClient([]string{idxSHA})
	factory := func(baseURL, token string) api.Client {
		if token != "shpd_test" {
			t.Errorf("token = %q", token)
		}
		return fc
	}

	out, err := runDeploy(t, factory, dir, "--send", "--new", "--site", "mysite")
	if err != nil {
		t.Fatalf("deploy --send: %v\n%s", err, out)
	}

	if fc.createdSlug != "mysite" {
		t.Errorf("created slug = %q", fc.createdSlug)
	}
	if len(fc.prepared.Manifest) != 1 || fc.prepared.Manifest[0].Path != "index.html" {
		t.Errorf("prepared manifest = %+v", fc.prepared.Manifest)
	}
	// The missing blob was uploaded with the file's bytes.
	if got := fc.uploaded["https://fake-presign.local/blobs/org/"+idxSHA]; got != len("<h1>hi</h1>") {
		t.Errorf("uploaded bytes = %d", got)
	}
	if fc.finalized.Digest == "" {
		t.Error("finalize missing digest")
	}
	if fc.published.VersionID != "ver_1" {
		t.Errorf("published version = %q", fc.published.VersionID)
	}
	if !strings.Contains(out, "Live at https://mysite.dropwaycontent.com") {
		t.Errorf("missing live URL in output:\n%s", out)
	}
}

// TestDeploy_NewSite_NormalizesSlug proves a loose --site value is slugified to
// the canonical grammar (mirroring the dashboard) before it hits the API, and the
// user is told what slug was used (H1: the API now rejects non-canonical slugs).
func TestDeploy_NewSite_NormalizesSlug(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("DROPWAY_API_KEY", "shpd_test")
	fc := newFakeClient([]string{sha256Hex(t, filepath.Join(dir, "index.html"))})
	factory := func(_, _ string) api.Client { return fc }

	out, err := runDeploy(t, factory, dir, "--send", "--new", "--site", "My Blog!")
	if err != nil {
		t.Fatalf("deploy --send: %v\n%s", err, out)
	}
	if fc.createdSlug != "my-blog" {
		t.Errorf("created slug = %q, want %q (normalized)", fc.createdSlug, "my-blog")
	}
	if !strings.Contains(out, `Using site slug "my-blog" (normalized from "My Blog!")`) {
		t.Errorf("expected a normalization notice in output:\n%s", out)
	}
}

// TestDeploy_NewSite_RejectsUnusableSlug proves a --site with no usable slug
// characters errors locally (no API call) instead of producing an empty slug.
func TestDeploy_NewSite_RejectsUnusableSlug(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("DROPWAY_API_KEY", "shpd_test")
	fc := newFakeClient(nil)
	factory := func(_, _ string) api.Client { return fc }

	_, err := runDeploy(t, factory, dir, "--send", "--new", "--site", "!!!")
	if err == nil {
		t.Fatal("expected an error for a slug with no usable characters")
	}
	if fc.createdSlug != "" {
		t.Errorf("CreateSite should not have been called; got slug %q", fc.createdSlug)
	}
}

// TestDeploy_SkipsAlreadyUploadedBlobs proves only-missing blobs are uploaded.
func TestDeploy_SkipsAlreadyUploadedBlobs(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("DROPWAY_API_KEY", "shpd_test")

	fc := newFakeClient(nil) // nothing missing → no uploads
	factory := func(string, string) api.Client { return fc }

	_, err := runDeploy(t, factory, dir, "--send", "--site-id", "site_existing")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if len(fc.uploaded) != 0 {
		t.Errorf("expected no uploads when nothing missing, got %v", fc.uploaded)
	}
	if fc.published.VersionID != "ver_1" {
		t.Error("publish should still run")
	}
}

func TestDeploy_Send_RequiresAuth(t *testing.T) {
	dir := tempSite(t)
	os.Unsetenv("DROPWAY_API_KEY")
	// Isolate credential storage to an empty dir so there's no stored login.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	factory := func(string, string) api.Client { return newFakeClient(nil) }
	_, err := runDeploy(t, factory, dir, "--send", "--site-id", "x")
	if err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("err = %v, want a sign-in requirement error", err)
	}
}

func TestDeploy_Send_RequiresTarget(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("DROPWAY_API_KEY", "shpd_test")
	factory := func(string, string) api.Client { return newFakeClient(nil) }
	_, err := runDeploy(t, factory, dir, "--send")
	if err == nil || !strings.Contains(err.Error(), "site-id") {
		t.Fatalf("err = %v, want a target-site requirement error", err)
	}
}

func TestDeploy_EmptyDir_Errors(t *testing.T) {
	dir := t.TempDir()
	factory := func(string, string) api.Client { return newFakeClient(nil) }
	_, err := runDeploy(t, factory, dir)
	if err == nil || !strings.Contains(err.Error(), "no files") {
		t.Fatalf("err = %v, want 'no files' error", err)
	}
}

func TestDeploy_MissingDir_Errors(t *testing.T) {
	factory := func(string, string) api.Client { return newFakeClient(nil) }
	_, err := runDeploy(t, factory, filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("missing directory should error")
	}
}

// sha256Hex computes the lowercase-hex SHA-256 of a file (mirrors manifest.Build).
func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
