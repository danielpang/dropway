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
	"github.com/danielpang/shipped/internal/customdomains"
	"github.com/danielpang/shipped/internal/edgetoken"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/api/internal/config"
	"github.com/danielpang/shipped/services/api/internal/handlers"
	"github.com/danielpang/shipped/services/api/internal/router"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// cloudDeps carries the dependencies the build-tag-selected mountCloud needs to
// wire cloud-only routes. It is declared here (not under a build tag) so both
// wire_oss.go (no-op mountCloud) and wire_cloud.go (real mountCloud) share one
// signature; the OSS build simply ignores every field.
type cloudDeps struct {
	Cfg                  config.Config
	Pool                 *pgxpool.Pool                   // nil when no DATABASE_URL
	Store                *store.Store                    // nil when no DATABASE_URL; billing's live role re-check
	Verifier             middleware.Verifier             // the EdDSA JWT verifier (authz boundary)
	EnsureOrgProvisioned func(http.Handler) http.Handler // ensure-org-provisioned middleware
}

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

	// Build-tag-selected quota provider: Unlimited (OSS) or the cloud hard-cap
	// pure policy (cloud build). The Store owns the race-safe mechanics.
	qp := newQuotaProvider(cfg)
	slog.Info("quota provider wired", "cloud_build", cloudBuild, "provider", quotaProviderName())

	// ---- Data layer: pgx pool (non-BYPASSRLS shipped_app role) → Store. ----
	// The pool is hoisted to the run() scope so the cloud build's mountCloud can
	// build the BillingStore over the SAME non-BYPASSRLS pool (the per-event SET
	// LOCAL app.current_org_id is the isolation, §9). It stays nil when there's no
	// DATABASE_URL, and mountCloud then skips billing.
	var st *store.Store
	var pool *pgxpool.Pool
	if cfg.DatabaseURL != "" {
		p, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		pool = p
		defer pool.Close()
		st = store.New(pool, qp)
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

	// ---- Edge signer (Phase 2): mints the host-scoped edge token + serves the
	// edge JWKS. SEPARATE keypair from Better Auth. Generated-and-logged in dev. ----
	edgeSigner, seed, generated, err := edgetoken.LoadOrGenerateSigner(cfg.EdgeSigningKey)
	if err != nil {
		return err
	}
	if generated {
		slog.Warn("EDGE_SIGNING_KEY not set — generated an EPHEMERAL edge signer; set EDGE_SIGNING_KEY to persist",
			"kid", edgeSigner.Kid(), "seed_base64url", seed)
	} else {
		slog.Info("edge signer wired", "kid", edgeSigner.Kid())
	}

	// ---- Custom-domain provider (Phase 2): Cloudflare for SaaS, or a Fake. ----
	domains := newDomainProvider(cfg)

	// The EdDSA JWT verifier is the authz boundary. Prime the JWKS at startup so
	// the first request doesn't pay the fetch; a failure here is non-fatal (it
	// lazily refreshes on first use / unknown kid).
	verifier := auth.NewVerifier(cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
	primeCtx, cancelPrime := context.WithTimeout(context.Background(), 5*time.Second)
	if err := verifier.Prime(primeCtx); err != nil {
		slog.Warn("priming JWKS failed; will refresh lazily", "err", err)
	}
	cancelPrime()

	// nil-safe: NewFull stores nil deps and the DB-backed routes return 503.
	var siteStore handlers.SiteStore
	if st != nil {
		siteStore = st
	}
	api := handlers.NewFull(qp, siteStore, obj, proj)
	api.EdgeSigner = edgeSigner
	api.Domains = domains
	api.AllowJWTRoleFallback = cfg.AllowJWTRoleFallback
	if cfg.AllowJWTRoleFallback {
		slog.Warn("ALLOW_JWT_ROLE_FALLBACK=true — admin gating will trust the JWT role claim when auth.member is unavailable")
	}

	// Build the router (concrete *chi.Mux), then let the build-tag-selected
	// mountCloud add cloud-only routes onto it. In the OSS build mountCloud is a
	// no-op (wire_oss.go) → no /webhooks/stripe, no /v1/billing (self-host has no
	// billing). In the cloud build it mounts the signed Stripe webhook + the authed
	// billing routes (wire_cloud.go). The OSS route surface is therefore identical
	// whether or not this call is present.
	mux := router.New(verifier, api, baseLogger)
	mountCloud(mux, cloudDeps{
		Cfg:                  cfg,
		Pool:                 pool,
		Store:                st,
		Verifier:             verifier,
		EnsureOrgProvisioned: api.EnsureOrgProvisioned,
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           mux,
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

// newDomainProvider selects the custom-hostname provider. With a Cloudflare zone +
// API token it uses Cloudflare for SaaS (production); otherwise it falls back to
// the in-memory Fake (offline/self-host/dev) so the custom-domain endpoints work
// without Cloudflare.
func newDomainProvider(cfg config.Config) customdomains.Provider {
	if cfg.CFZoneID != "" && cfg.CFAPIToken != "" {
		slog.Info("custom-domain provider: cloudflare for saas", "zone", cfg.CFZoneID)
		return customdomains.NewCloudflareProvider(cfg.CFZoneID, cfg.CFAPIToken, projection.ContentDomain)
	}
	slog.Warn("custom-domain provider: in-memory fake (no CF_ZONE_ID/CF_API_TOKEN) — dev/self-host only")
	return customdomains.NewFake()
}
