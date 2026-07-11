// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package agent_test

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/danielpang/dropway/internal/sandbox"
	"github.com/danielpang/dropway/services/sandboxd/internal/agent"
)

// TestAgentClientRoundTrip drives the real sandboxd HTTP handlers through the
// internal/sandbox agent client over an httptest server, so the wire protocol
// is verified end to end (the two halves can never silently drift).
func TestAgentClientRoundTrip(t *testing.T) {
	const token = "test-token"
	a := agent.New(agent.Config{Token: token, Workdir: t.TempDir()})
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	sb := sandbox.NewAgentClient("id-1", srv.URL, token, srv.Client())
	ctx := context.Background()

	if err := sandbox.WaitReady(ctx, sb); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	if err := sb.WriteFile(ctx, "index.html", []byte("<h1>hi</h1>")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := sb.ReadFile(ctx, "index.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "<h1>hi</h1>" {
		t.Errorf("ReadFile = %q", got)
	}

	res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", "echo out; echo err 1>&2; exit 3"}})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", res.ExitCode)
	}
	if !bytes.Contains(res.Stdout, []byte("out")) {
		t.Errorf("stdout = %q", res.Stdout)
	}
	if !bytes.Contains(res.Stderr, []byte("err")) {
		t.Errorf("stderr = %q", res.Stderr)
	}

	files, err := sb.ListFiles(ctx, "")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Path == "index.html" {
			found = true
		}
	}
	if !found {
		t.Errorf("ListFiles missing index.html: %+v", files)
	}

	rc, err := sb.ExportTar(ctx, "")
	if err != nil {
		t.Fatalf("ExportTar: %v", err)
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	exported := map[string]string{}
	for {
		hdr, terr := tr.Next()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			t.Fatalf("tar read: %v", terr)
		}
		b, _ := io.ReadAll(tr)
		exported[hdr.Name] = string(b)
	}
	if exported["index.html"] != "<h1>hi</h1>" {
		t.Errorf("exported tar = %+v", exported)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "seed.txt", Mode: 0o644, Size: 4})
	_, _ = tw.Write([]byte("seed"))
	_ = tw.Close()
	if err := sb.ImportTar(ctx, "", &buf); err != nil {
		t.Fatalf("ImportTar: %v", err)
	}
	seeded, err := sb.ReadFile(ctx, "seed.txt")
	if err != nil {
		t.Fatalf("ReadFile seeded: %v", err)
	}
	if string(seeded) != "seed" {
		t.Errorf("seeded = %q", seeded)
	}
}

// TestAgentClientAuth rejects a wrong token.
func TestAgentClientAuth(t *testing.T) {
	a := agent.New(agent.Config{Token: "right", Workdir: t.TempDir()})
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	sb := sandbox.NewAgentClient("id", srv.URL, "wrong", srv.Client())
	if _, err := sb.Exec(context.Background(), sandbox.ExecRequest{Cmd: []string{"true"}}); err == nil {
		t.Fatal("expected auth failure with wrong token")
	}
}

// TestPathTraversalContained ensures a "../" escape is collapsed to a
// workdir-relative path (contained), never writing outside the workdir.
func TestPathTraversalContained(t *testing.T) {
	workdir := t.TempDir()
	a := agent.New(agent.Config{Token: "t", Workdir: workdir})
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	sb := sandbox.NewAgentClient("id", srv.URL, "t", srv.Client())

	// The traversal is neutralized: "../escape.txt" collapses to
	// <workdir>/escape.txt, so nothing is written to the parent directory.
	if err := sb.WriteFile(context.Background(), "../escape.txt", []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(workdir), "escape.txt")); err == nil {
		t.Fatal("traversal escaped the workdir")
	}
	if _, err := os.Stat(filepath.Join(workdir, "escape.txt")); err != nil {
		t.Fatalf("contained file not found in workdir: %v", err)
	}
}
