// Command api is the Shipped control-plane HTTP server (api.shipped.app): the
// system of record and the authz boundary (docs/ARCHITECTURE.md §3).
//
// It loads config from the environment, builds the chi router, wires an
// auth.Verifier (EdDSA JWT via JWKS) and a quota.Provider, then serves with
// graceful shutdown. The quota.Provider is selected at build time: the default
// (OSS) build links quota.Unlimited{} via wire_oss.go; the `cloud` build links
// the real hard-cap provider via wire_cloud.go.
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

	"github.com/danielpang/shipped/internal/auth"
	"github.com/danielpang/shipped/services/api/internal/config"
	"github.com/danielpang/shipped/services/api/internal/handlers"
	"github.com/danielpang/shipped/services/api/internal/router"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cfg.DatabaseURL == "" {
		slog.Warn("DATABASE_URL not set — DB-backed routes will be unavailable (Phase 1 stubs only)")
	}
	if cfg.JWKSURL == "" {
		// The authenticated routes can't verify without a JWKS; surface it loudly.
		slog.Warn("JWKS_URL not set — authenticated routes will reject all tokens")
	}

	// The EdDSA JWT verifier is the authz boundary. Prime the JWKS at startup so
	// the first request doesn't pay the fetch; a failure here is non-fatal (it
	// lazily refreshes on first use / unknown kid).
	verifier := auth.NewVerifier(cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
	primeCtx, cancelPrime := context.WithTimeout(context.Background(), 5*time.Second)
	if err := verifier.Prime(primeCtx); err != nil {
		slog.Warn("priming JWKS failed; will refresh lazily", "err", err)
	}
	cancelPrime()

	// Build-tag-selected quota provider: Unlimited (OSS) or the cloud hard-cap
	// provider (cloud build). See wire_oss.go / wire_cloud.go.
	qp := newQuotaProvider(cfg)
	slog.Info("quota provider wired", "cloud_build", cloudBuild, "provider", quotaProviderName())

	api := handlers.New(qp)
	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router.New(verifier, api),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Run the listener; report a fatal listen error over a channel so we can
	// select against the shutdown signal.
	listenErr := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-listenErr:
		return err
	case sig := <-stop:
		slog.Info("shutdown signal received, draining", "signal", sig.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	slog.Info("server stopped cleanly")
	return nil
}
