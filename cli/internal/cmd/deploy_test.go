package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielpang/shipped/cli/internal/api"
)

// fakeClient records the request it received and returns a canned response.
type fakeClient struct {
	gotReq api.PrepareRequest
	resp   *api.PrepareResponse
	err    error
}

func (f *fakeClient) PrepareDeployment(_ context.Context, req api.PrepareRequest) (*api.PrepareResponse, error) {
	f.gotReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
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

func TestDeploy_DryRun_PrintsManifestJSON_NoNetwork(t *testing.T) {
	dir := tempSite(t)
	called := false
	factory := func(string, string) api.Client {
		called = true
		return &fakeClient{}
	}

	out, err := runDeploy(t, factory, dir)
	if err != nil {
		t.Fatalf("deploy dry run: %v", err)
	}
	if called {
		t.Error("dry run must NOT construct/use a network client")
	}
	if !strings.Contains(out, "/v1/deployments/prepare") {
		t.Error("output should show the prepare endpoint")
	}
	if !strings.Contains(out, "\"index.html\"") {
		t.Error("output should include the manifest JSON with index.html")
	}
	if !strings.Contains(out, "dry run") {
		t.Error("dry run hint should be printed")
	}
}

func TestDeploy_Send_CallsClient(t *testing.T) {
	dir := tempSite(t)
	t.Setenv("SHIPPED_TOKEN", "shpd_test")

	fc := &fakeClient{resp: &api.PrepareResponse{DeploymentID: "dpl_1", MissingSHA: []string{"abc"}}}
	factory := func(baseURL, token string) api.Client {
		if token != "shpd_test" {
			t.Errorf("token = %q, want shpd_test", token)
		}
		return fc
	}

	out, err := runDeploy(t, factory, dir, "--send")
	if err != nil {
		t.Fatalf("deploy --send: %v", err)
	}
	if fc.gotReq.Digest == "" || len(fc.gotReq.Files) != 1 {
		t.Errorf("client received bad request: %+v", fc.gotReq)
	}
	if !strings.Contains(out, "dpl_1") || !strings.Contains(out, "1/1 blob") {
		t.Errorf("output missing prepared-deployment summary: %s", out)
	}
}

func TestDeploy_Send_RequiresToken(t *testing.T) {
	dir := tempSite(t)
	os.Unsetenv("SHIPPED_TOKEN")

	factory := func(string, string) api.Client { return &fakeClient{} }
	_, err := runDeploy(t, factory, dir, "--send")
	if err == nil || !strings.Contains(err.Error(), "SHIPPED_TOKEN") {
		t.Fatalf("err = %v, want a SHIPPED_TOKEN requirement error", err)
	}
}

func TestDeploy_EmptyDir_Errors(t *testing.T) {
	dir := t.TempDir() // no files
	factory := func(string, string) api.Client { return &fakeClient{} }
	_, err := runDeploy(t, factory, dir)
	if err == nil || !strings.Contains(err.Error(), "no files") {
		t.Fatalf("err = %v, want 'no files' error", err)
	}
}

func TestDeploy_MissingDir_Errors(t *testing.T) {
	factory := func(string, string) api.Client { return &fakeClient{} }
	_, err := runDeploy(t, factory, filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("missing directory should error")
	}
}
