// Command serve is the Shipped self-host content server — a plain Go HTTP server
// that is a full-parity alternative to the Cloudflare serving Worker
// (edge/serving-worker). It serves tenant static content on
// *.shippedusercontent.com + custom domains and enforces all four access modes
// (public/password/allowlist/org_only). The request lifecycle, every access-mode
// enforcement path, and the headers/CSP are a faithful Go port of the Worker.
//
// SECURITY: the DB connection is the non-BYPASSRLS shipped_app role (the same
// DATABASE_URL the API uses); cross-org host resolution is the narrow SECURITY
// DEFINER app.resolve_host. The server NEVER signs edge tokens (verify-only) and
// NEVER reads the operator Better Auth JWT.
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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/serve/internal/config"
	"github.com/danielpang/shipped/services/serve/internal/edgeverify"
	"github.com/danielpang/shipped/services/serve/internal/ratelimit"
	"github.com/danielpang/shipped/services/serve/internal/serve"
	"github.com/danielpang/shipped/services/serve/internal/storeadapter"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("serve exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()

	// ---- Data layer: pgx pool as the non-BYPASSRLS shipped_app role → Store. ----
	if cfg.DatabaseURL == "" {
		return errors.New("serve: DATABASE_URL is required (the shipped_app role) to resolve hosts")
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	resolver := storeadapter.NewRouteResolver(pool)
	slog.Info("route resolver wired (app.resolve_host, non-BYPASSRLS shipped_app)")

	// ---- Object storage: S3/R2 (MinIO locally), server-side reads only. ----
	if cfg.S3Bucket == "" {
		return errors.New("serve: S3_BUCKET is required to read manifests/blobs")
	}
	objStore, err := storage.NewS3Store(ctx, storage.S3Config{
		Bucket:          cfg.S3Bucket,
		Region:          cfg.S3Region,
		Endpoint:        cfg.S3Endpoint,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		UsePathStyle:    cfg.S3ForcePathStyle,
	})
	if err != nil {
		return err
	}
	slog.Info("object storage wired", "endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket)

	// ---- Hard-revocation denylist reader for the gated path. Cloudflare KV (prod)
	// or the local projection file (self-host). When NEITHER is configured, gated
	// serving FAILS CLOSED (a nil reader ⇒ every gated request 302s) — matching the
	// Worker's "no denylist binding ⇒ revoked". Public serving is unaffected. ----
	var revReader edgeverify.RevocationReader
	if denylist := newDenylistLookup(cfg); denylist != nil {
		revReader = storeadapter.NewRevocationReader(denylist)
	} else {
		slog.Warn("no revocation denylist reader (CF_* / PROJECTION_FILE unset) — GATED sites will 302 (fail closed); public serving works")
	}

	// ---- Edge-token verifier: remote JWKS client + the route bindings + revocation.
	verifier := edgeverify.New(cfg.EdgeJWKSURL, revReader)
	slog.Info("edge verifier wired", "jwks_url", cfg.EdgeJWKSURL)

	limiter := ratelimit.New(cfg.RateLimitMax, cfg.RateLimitWindow)

	// Org-status reader: self-host (OSS) has no billing/org_status source, so it is
	// left unwired (nil ⇒ the org-status gate is skipped / fails open), matching the
	// Worker's "no status KV configured" path. Link-expiry IS wired (from the route).
	handler := serve.New(resolver, objStore, verifier, limiter, nil, serve.Config{
		AppAuthzURL: cfg.AppAuthzURL,
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // streaming large blobs; no write deadline
		IdleTimeout:       120 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		slog.Info("serve listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

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

// newDenylistLookup selects the hard-revocation denylist reader, preferring
// Cloudflare KV (prod) then the local projection file (self-host). Returns nil
// when neither is configured (gated serving then fails closed).
func newDenylistLookup(cfg config.Config) storeadapter.DenylistLookup {
	if cfg.CFAccountID != "" && cfg.CFKVNamespaceID != "" && cfg.CFAPIToken != "" {
		slog.Info("revocation reader: cloudflare KV", "namespace", cfg.CFKVNamespaceID)
		return projection.NewCloudflareKV(cfg.CFAccountID, cfg.CFKVNamespaceID, cfg.CFAPIToken)
	}
	if cfg.ProjectionFilePath != "" {
		if l, err := projection.NewLocalFile(cfg.ProjectionFilePath); err == nil {
			slog.Info("revocation reader: local projection file", "path", cfg.ProjectionFilePath)
			return l
		}
		slog.Warn("projection file unreadable; no revocation reader", "path", cfg.ProjectionFilePath)
	}
	return nil
}
