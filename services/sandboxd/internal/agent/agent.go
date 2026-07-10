// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package agent implements the in-sandbox HTTP surface sandboxd serves. The
// endpoints mirror internal/sandbox's agentClient wire protocol exactly:
//
//	POST /exec    run a command                  (wireExecReq → wireExecResp)
//	POST /read    read a file                     (wireFileReq → wireFileResp)
//	POST /write   write a file                    (wireFileReq)
//	POST /list    list a directory                (wireListReq → wireListResp)
//	POST /import  unpack a tar into ?dir=         (raw tar body)
//	GET  /export  stream ?dir= back as a tar
//	GET  /healthz readiness
//
// Every endpoint except /healthz requires the bearer token. All paths are
// resolved under the configured workdir; traversal outside it is rejected.
package agent

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config configures the agent.
type Config struct {
	Token   string
	Workdir string
}

// Agent serves the sandbox HTTP surface.
type Agent struct {
	token   string
	workdir string

	mu           sync.Mutex
	lastActivity time.Time
}

// New builds an Agent.
func New(cfg Config) *Agent {
	return &Agent{token: cfg.Token, workdir: cfg.Workdir, lastActivity: time.Now()}
}

// LastActivity reports when the agent last handled a request (idle watchdog).
func (a *Agent) LastActivity() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastActivity
}

func (a *Agent) touch() {
	a.mu.Lock()
	a.lastActivity = time.Now()
	a.mu.Unlock()
}

// Handler returns the agent's HTTP mux.
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /exec", a.auth(a.handleExec))
	mux.HandleFunc("POST /read", a.auth(a.handleRead))
	mux.HandleFunc("POST /write", a.auth(a.handleWrite))
	mux.HandleFunc("POST /list", a.auth(a.handleList))
	mux.HandleFunc("POST /import", a.auth(a.handleImport))
	mux.HandleFunc("GET /export", a.auth(a.handleExport))
	return mux
}

// auth wraps a handler with constant-time bearer-token verification + activity
// tracking.
func (a *Agent) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		a.touch()
		next(w, r)
	}
}

// --- wire shapes (mirror internal/sandbox/agentclient.go) ---

type execReq struct {
	Cmd     []string `json:"cmd"`
	Cwd     string   `json:"cwd,omitempty"`
	Timeout int      `json:"timeout_seconds,omitempty"`
	Stdin   string   `json:"stdin_b64,omitempty"`
}

type execResp struct {
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout_b64"`
	Stderr    string `json:"stderr_b64"`
	Truncated bool   `json:"truncated"`
}

type fileReq struct {
	Path string `json:"path"`
	Data string `json:"data_b64,omitempty"`
}

type fileResp struct {
	Data string `json:"data_b64"`
}

type listReq struct {
	Dir string `json:"dir"`
}

type listResp struct {
	Files []fileInfo `json:"files"`
}

type fileInfo struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// maxOutput bounds captured stdout/stderr per stream so a chatty build can't
// blow up the API's agent-loop context.
const maxOutput = 64 << 10

func (a *Agent) handleExec(w http.ResponseWriter, r *http.Request) {
	var req execReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}
	cwd, err := a.resolve(req.Cwd)
	if err != nil {
		http.Error(w, "bad cwd", http.StatusBadRequest)
		return
	}

	cmd := exec.CommandContext(ctx, req.Cmd[0], req.Cmd[1:]...)
	cmd.Dir = cwd
	if req.Stdin != "" {
		if b, derr := base64.StdEncoding.DecodeString(req.Stdin); derr == nil {
			cmd.Stdin = strings.NewReader(string(b))
		}
	}
	var stdout, stderr cappedBuffer
	stdout.limit, stderr.limit = maxOutput, maxOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode := 0
	if runErr := cmd.Run(); runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
			stderr.Write([]byte(runErr.Error()))
		}
	}
	writeJSON(w, execResp{
		ExitCode:  exitCode,
		Stdout:    base64.StdEncoding.EncodeToString(stdout.Bytes()),
		Stderr:    base64.StdEncoding.EncodeToString(stderr.Bytes()),
		Truncated: stdout.truncated || stderr.truncated,
	})
}

func (a *Agent) handleRead(w http.ResponseWriter, r *http.Request) {
	var req fileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p, err := a.resolve(req.Path)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, fileResp{Data: base64.StdEncoding.EncodeToString(data)})
}

func (a *Agent) handleWrite(w http.ResponseWriter, r *http.Request) {
	var req fileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p, err := a.resolve(req.Path)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "bad data", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *Agent) handleList(w http.ResponseWriter, r *http.Request) {
	var req listReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	root, err := a.resolve(req.Dir)
	if err != nil {
		http.Error(w, "bad dir", http.StatusBadRequest)
		return
	}
	var out listResp
	err = filepath.Walk(root, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(a.workdir, path)
		out.Files = append(out.Files, fileInfo{Path: rel, Size: info.Size(), IsDir: info.IsDir()})
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// resolve joins p under the workdir and rejects any path that escapes it (a
// leading "/" is treated as workdir-relative).
func (a *Agent) resolve(p string) (string, error) {
	if p == "" {
		return a.workdir, nil
	}
	clean := filepath.Clean("/" + p) // force absolute, collapse ".."
	full := filepath.Join(a.workdir, clean)
	if full != a.workdir && !strings.HasPrefix(full, a.workdir+string(os.PathSeparator)) {
		return "", os.ErrPermission
	}
	return full, nil
}

// cappedBuffer accumulates up to limit bytes, dropping the rest and recording
// that it truncated.
type cappedBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.buf); room > 0 {
		if len(p) > room {
			b.buf = append(b.buf, p[:room]...)
			b.truncated = true
		} else {
			b.buf = append(b.buf, p...)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil // always report full write so the command never blocks
}

func (b *cappedBuffer) Bytes() []byte { return b.buf }
