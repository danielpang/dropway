// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Command sandboxd is the tiny HTTP agent that runs INSIDE a builder sandbox
// (Fly Machine or Docker container). The Dropway API drives it over the
// sandbox.Provider seam to exec build commands and read/write/import/export the
// site working tree. It authenticates every request with a per-sandbox bearer
// token injected at boot (SANDBOXD_TOKEN) and binds SANDBOXD_PORT.
//
// It holds NO Dropway, R2, or OpenRouter credentials — only a copy of one site's
// files and its own auth token. This is the untrusted side of the boundary: the
// model's generated code runs here, isolated from the control plane.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielpang/dropway/services/sandboxd/internal/agent"
)

func main() {
	token := os.Getenv("SANDBOXD_TOKEN")
	if token == "" {
		slog.Error("SANDBOXD_TOKEN is required")
		os.Exit(1)
	}
	port := os.Getenv("SANDBOXD_PORT")
	if port == "" {
		port = "8090"
	}
	workdir := os.Getenv("SANDBOXD_WORKDIR")
	if workdir == "" {
		workdir = "/workspace"
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		slog.Error("create workdir", "err", err)
		os.Exit(1)
	}

	// Idle self-destruct: the agent exits (and, on Fly with auto_destroy, the
	// machine is reaped) after this long with no request, so a forgotten session
	// never leaks a running sandbox. The API also enforces limits and reaps.
	idle := 15 * time.Minute
	if v := os.Getenv("SANDBOXD_IDLE_SECONDS"); v != "" {
		if d, err := time.ParseDuration(v + "s"); err == nil && d > 0 {
			idle = d
		}
	}

	a := agent.New(agent.Config{Token: token, Workdir: workdir})
	srv := &http.Server{Addr: ":" + port, Handler: a.Handler()}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Idle watchdog: shut the server down when no request has arrived in `idle`.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Since(a.LastActivity()) > idle {
					slog.Info("sandbox idle, shutting down", "idle", idle)
					stop()
					return
				}
			}
		}
	}()

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	slog.Info("sandboxd listening", "port", port, "workdir", workdir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
