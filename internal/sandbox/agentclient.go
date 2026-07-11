package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// agentClient talks to the in-container sandboxd HTTP agent over the provider's
// reachable base URL (Fly private networking / a mapped Docker port), bearing a
// per-sandbox token. Both providers embed one; it is the single place the
// sandboxd wire protocol lives, so hosted and self-host can never drift.
//
// The wire shapes here MUST match services/sandboxd's handlers.
type agentClient struct {
	id      string
	baseURL string
	token   string
	http    *http.Client
}

// NewAgentClient builds a Sandbox handle backed by the in-container sandboxd
// agent at baseURL, authenticating with token. Providers call this after they
// have booted a container/machine and resolved its reachable agent address.
func NewAgentClient(id, baseURL, token string, hc *http.Client) Sandbox {
	return newAgentClient(id, baseURL, token, hc)
}

// WaitReady blocks until s's in-container agent answers /healthz or ctx expires.
// It is a no-op (nil) for a Sandbox that is not agent-backed.
func WaitReady(ctx context.Context, s Sandbox) error {
	if ac, ok := s.(*agentClient); ok {
		return ac.WaitReady(ctx)
	}
	return nil
}

func newAgentClient(id, baseURL, token string, hc *http.Client) *agentClient {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Minute}
	}
	return &agentClient{id: id, baseURL: baseURL, token: token, http: hc}
}

func (a *agentClient) ID() string { return a.id }

func (a *agentClient) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return a.http.Do(req)
}

func (a *agentClient) doJSON(ctx context.Context, path string, reqBody, respBody any) error {
	var buf bytes.Buffer
	if reqBody != nil {
		if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
			return err
		}
	}
	resp, err := a.do(ctx, http.MethodPost, path, &buf, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("sandbox agent %s: %s: %s", path, resp.Status, string(msg))
	}
	if respBody != nil {
		return json.NewDecoder(resp.Body).Decode(respBody)
	}
	return nil
}

// --- wire shapes (mirror services/sandboxd) ---

type wireExecReq struct {
	Cmd     []string `json:"cmd"`
	Cwd     string   `json:"cwd,omitempty"`
	Timeout int      `json:"timeout_seconds,omitempty"`
	Stdin   string   `json:"stdin_b64,omitempty"`
}

type wireExecResp struct {
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout_b64"`
	Stderr    string `json:"stderr_b64"`
	Truncated bool   `json:"truncated"`
}

type wireFileReq struct {
	Path string `json:"path"`
	Data string `json:"data_b64,omitempty"`
}

type wireFileResp struct {
	Data string `json:"data_b64"`
}

type wireListReq struct {
	Dir string `json:"dir"`
}

type wireListResp struct {
	Files []wireFileInfo `json:"files"`
}

type wireFileInfo struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

func (a *agentClient) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	wreq := wireExecReq{Cmd: req.Cmd, Cwd: req.Cwd}
	if req.Timeout > 0 {
		wreq.Timeout = int(req.Timeout / time.Second)
	}
	if len(req.Stdin) > 0 {
		wreq.Stdin = base64.StdEncoding.EncodeToString(req.Stdin)
	}
	var wresp wireExecResp
	if err := a.doJSON(ctx, "/exec", wreq, &wresp); err != nil {
		return ExecResult{}, err
	}
	stdout, _ := base64.StdEncoding.DecodeString(wresp.Stdout)
	stderr, _ := base64.StdEncoding.DecodeString(wresp.Stderr)
	return ExecResult{ExitCode: wresp.ExitCode, Stdout: stdout, Stderr: stderr, Truncated: wresp.Truncated}, nil
}

func (a *agentClient) ReadFile(ctx context.Context, path string) ([]byte, error) {
	var wresp wireFileResp
	if err := a.doJSON(ctx, "/read", wireFileReq{Path: path}, &wresp); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(wresp.Data)
}

func (a *agentClient) WriteFile(ctx context.Context, path string, data []byte) error {
	return a.doJSON(ctx, "/write", wireFileReq{
		Path: path,
		Data: base64.StdEncoding.EncodeToString(data),
	}, nil)
}

func (a *agentClient) ListFiles(ctx context.Context, dir string) ([]FileInfo, error) {
	var wresp wireListResp
	if err := a.doJSON(ctx, "/list", wireListReq{Dir: dir}, &wresp); err != nil {
		return nil, err
	}
	out := make([]FileInfo, len(wresp.Files))
	for i, f := range wresp.Files {
		out[i] = FileInfo{Path: f.Path, Size: f.Size, IsDir: f.IsDir}
	}
	return out, nil
}

// dirQuery builds the escaped "dir=<dir>" query string for the tar endpoints, so
// a workdir containing reserved characters (#, &, +, space) can't corrupt the
// request path or be misparsed by the agent.
func dirQuery(dir string) string {
	return url.Values{"dir": {dir}}.Encode()
}

// ImportTar streams a tar into dir via the agent's raw /import endpoint (the tar
// bytes are the request body, not base64 JSON, to avoid buffering a whole site).
func (a *agentClient) ImportTar(ctx context.Context, dir string, r io.Reader) error {
	resp, err := a.do(ctx, http.MethodPost, "/import?"+dirQuery(dir), r, "application/x-tar")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("sandbox agent /import: %s: %s", resp.Status, string(msg))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// ExportTar streams dir back as a tar. The caller must Close the returned reader.
func (a *agentClient) ExportTar(ctx context.Context, dir string) (io.ReadCloser, error) {
	resp, err := a.do(ctx, http.MethodGet, "/export?"+dirQuery(dir), nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("sandbox agent /export: %s: %s", resp.Status, string(msg))
	}
	return resp.Body, nil
}

// WaitReady polls the agent's /healthz until it responds or ctx expires — a
// freshly booted sandbox's agent takes a moment to bind.
func (a *agentClient) WaitReady(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("sandbox agent %s not ready: %w", a.id, ctx.Err())
		default:
		}
		resp, err := a.do(ctx, http.MethodGet, "/healthz", nil, "")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("sandbox agent %s not ready: %w", a.id, ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Ensure agentClient satisfies the file/exec half of Sandbox (providers embed it
// and add ID()/lifecycle).
var _ interface {
	Exec(context.Context, ExecRequest) (ExecResult, error)
	ReadFile(context.Context, string) ([]byte, error)
	WriteFile(context.Context, string, []byte) error
	ListFiles(context.Context, string) ([]FileInfo, error)
	ImportTar(context.Context, string, io.Reader) error
	ExportTar(context.Context, string) (io.ReadCloser, error)
} = (*agentClient)(nil)
