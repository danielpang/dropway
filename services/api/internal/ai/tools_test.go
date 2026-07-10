// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/internal/sandbox"
)

// fakeSandbox records writes and scripts exec/read results for dispatchTool tests.
type fakeSandbox struct {
	files    map[string][]byte
	execFn   func(sandbox.ExecRequest) sandbox.ExecResult
	listResp []sandbox.FileInfo
}

func newFakeSandbox() *fakeSandbox { return &fakeSandbox{files: map[string][]byte{}} }

func (f *fakeSandbox) ID() string { return "fake" }
func (f *fakeSandbox) Exec(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if f.execFn != nil {
		return f.execFn(req), nil
	}
	return sandbox.ExecResult{ExitCode: 0, Stdout: []byte("ok")}, nil
}
func (f *fakeSandbox) ImportTar(context.Context, string, io.Reader) error { return nil }
func (f *fakeSandbox) ExportTar(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (f *fakeSandbox) ReadFile(_ context.Context, path string) ([]byte, error) {
	return f.files[path], nil
}
func (f *fakeSandbox) WriteFile(_ context.Context, path string, data []byte) error {
	f.files[path] = data
	return nil
}
func (f *fakeSandbox) ListFiles(context.Context, string) ([]sandbox.FileInfo, error) {
	return f.listResp, nil
}

func TestDispatchToolWriteThenRead(t *testing.T) {
	sb := newFakeSandbox()
	ctx := context.Background()

	out := dispatchTool(ctx, sb, openrouter.ToolCall{
		Function: openrouter.FunctionCall{
			Name:      toolWriteFile,
			Arguments: `{"path":"index.html","content":"<h1>hi</h1>"}`,
		},
	})
	if !strings.Contains(out, "wrote index.html") {
		t.Errorf("write result = %q", out)
	}
	if string(sb.files["index.html"]) != "<h1>hi</h1>" {
		t.Errorf("file not written: %q", sb.files["index.html"])
	}

	read := dispatchTool(ctx, sb, openrouter.ToolCall{
		Function: openrouter.FunctionCall{Name: toolReadFile, Arguments: `{"path":"index.html"}`},
	})
	if read != "<h1>hi</h1>" {
		t.Errorf("read result = %q", read)
	}
}

func TestDispatchToolRunCommand(t *testing.T) {
	sb := newFakeSandbox()
	sb.execFn = func(req sandbox.ExecRequest) sandbox.ExecResult {
		return sandbox.ExecResult{ExitCode: 2, Stdout: []byte("building"), Stderr: []byte("warn")}
	}
	out := dispatchTool(context.Background(), sb, openrouter.ToolCall{
		Function: openrouter.FunctionCall{Name: toolRunCommand, Arguments: `{"command":"npm run build"}`},
	})
	if !strings.Contains(out, "exit_code: 2") || !strings.Contains(out, "building") || !strings.Contains(out, "warn") {
		t.Errorf("run result = %q", out)
	}
}

func TestDispatchToolInvalidArgs(t *testing.T) {
	out := dispatchTool(context.Background(), newFakeSandbox(), openrouter.ToolCall{
		Function: openrouter.FunctionCall{Name: toolWriteFile, Arguments: `not json`},
	})
	if !strings.Contains(out, "invalid arguments") {
		t.Errorf("expected invalid-args message, got %q", out)
	}
}

func TestDispatchToolUnknown(t *testing.T) {
	out := dispatchTool(context.Background(), newFakeSandbox(), openrouter.ToolCall{
		Function: openrouter.FunctionCall{Name: "publish_site", Arguments: `{}`},
	})
	if !strings.Contains(out, "unknown tool") {
		t.Errorf("expected unknown-tool message, got %q", out)
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"index.html":       "index.html",
		"./index.html":     "index.html",
		"/index.html":      "index.html",
		"../../etc/passwd": "etc/passwd", // traversal collapsed
		"assets/app.js":    "assets/app.js",
		"a\\b.txt":         "a/b.txt", // backslashes normalized
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContentType(t *testing.T) {
	if ct := contentType("index.html"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("html content type = %q", ct)
	}
	if ct := contentType("data.bin"); ct != "application/octet-stream" {
		t.Errorf("unknown ext content type = %q", ct)
	}
}
