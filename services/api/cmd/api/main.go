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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/shipped/internal/auth"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/api/internal/config"
	"github.com/danielpang/shipped/services/api/internal/handlers"
	"github.com/danielpang/shipped/services/api/internal/router"
	"github.com/danielpang/shipped/services/api/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(baseLogger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()

	if cfg.JWKSURL == "" {
		// The authenticated routes can't verify without a JWKS; surface it loudly.
		slog.Warn("JWKS_URL not set — authenticated routes will reject all tokens")
	}

	// ---- Data layer: pgx pool (non-BYPASSRLS shipped_app role) → Store. ----
	var st *store.Store
	if cfg.DatabaseURL != "" {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		st = store.New(pool)
		slog.Info("store wired (RLS tenant context per request)")
	} else {
		slog.Warn("DATABASE_URL not set — DB-backed routes will return 503")
	}

	// ---- Object storage: S3/R2 (MinIO locally) for blobs + manifests. ----
	var obj storage.Store
	if cfg.S3Bucket != "" {
		s3, err := storage.NewS3Store(ctx, storage.S3Config{
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
		obj = s3
		slog.Info("object storage wired", "endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket)
	} else {
		slog.Warn("S3_BUCKET not set — deploy routes will return 503")
	}

	// ---- Projection writer: Cloudflare KV (prod) or a local writer (dev). ----
	proj := newProjectionWriter(cfg)

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

	// nil-safe: NewFull stores nil deps and the DB-backed routes return 503.
	var siteStore handlers.SiteStore
	if st != nil {
		siteStore = st
	}
	api := handlers.NewFull(qp, siteStore, obj, proj)
	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router.New(verifier, api, baseLogger),
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

// newProjectionWriter selects the edge-projection writer. With Cloudflare creds
// it writes to Workers KV (production); otherwise it falls back to a local writer
// (in-memory, optionally mirrored to PROJECTION_FILE) so the publish path works
// offline / in self-host without Cloudflare.
func newProjectionWriter(cfg config.Config) projection.Writer {
	if cfg.CFAccountID != "" && cfg.CFKVNamespaceID != "" && cfg.CFAPIToken != "" {
		slog.Info("projection writer: cloudflare KV", "namespace", cfg.CFKVNamespaceID)
		return projection.NewCloudflareKV(cfg.CFAccountID, cfg.CFKVNamespaceID, cfg.CFAPIToken)
	}
	if cfg.ProjectionFilePath != "" {
		if l, err := projection.NewLocalFile(cfg.ProjectionFilePath); err == nil {
			slog.Info("projection writer: local file", "path", cfg.ProjectionFilePath)
			return l
		}
		slog.Warn("projection file unreadable; using in-memory projection", "path", cfg.ProjectionFilePath)
	}
	slog.Warn("projection writer: in-memory (no CF_* creds) — dev/self-host only")
	return projection.NewLocal()
}
