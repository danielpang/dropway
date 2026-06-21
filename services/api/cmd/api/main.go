// Command api is the Dropway control-plane HTTP server (api.dropway.dev): the
// system of record and the authz boundary.
//
// It loads config from the environment, builds the chi router, wires an
// auth.Verifier (EdDSA JWT via JWKS) and a quota.Provider, then serves with
// graceful shutdown. The quota.Provider is selected at build time: the default
// (OSS) build links quota.Unlimited{} via wire_oss.go; the `cloud` build links
// the real hard-cap provider via wire_cloud.go.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/customdomains"
	"github.com/danielpang/dropway/internal/edgetoken"
	"github.com/danielpang/dropway/internal/errtrack"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/pgpool"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/config"
	"github.com/danielpang/dropway/services/api/internal/handlers"
	"github.com/danielpang/dropway/services/api/internal/router"
	"github.com/danielpang/dropway/services/api/internal/store"
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
	// Projection is the SAME edge-projection writer the route projection uses
	// (Cloudflare KV in prod, a local writer in dev). The cloud build threads it into
	// the BillingStore so a billing org_status change is pushed to the edge
	// (org_status:<orgID>) — making suspension/over_limit actually block at the
	// serving Worker. The OSS build ignores it.
	Projection projection.Writer
	// Analytics is the shared, vendor-neutral product-analytics emitter
	// (internal/analytics; a PostHog client when POSTHOG_KEY is set, else nil). The
	// cloud build hands it to the BillingStore for plan upgrade/downgrade events; the
	// OSS build ignores it. Its lifecycle (flush on shutdown) is owned by run().
	Analytics analytics.Emitter
}

func main() {
	// Error tracking is wired first so the default logger captures EVERY
	// slog.Error from this point on (incl. startup failures). The provider is
	// runtime-selected (PostHog by default, Noop when unconfigured). WrapSlogHandler
	// mirrors Error-level records to the sink; Noop returns the JSON handler as-is.
	rep, label := errtrack.FromEnv("api")
	logger := slog.New(rep.WrapSlogHandler(slog.NewJSONHandler(os.Stdout, nil)))
	slog.SetDefault(logger)
	slog.Info("error tracking wired", "provider", label)

	err := run(logger)
	if err != nil {
		// Log before Close so this final fatal error is captured + flushed.
		slog.Error("server exited with error", "err", err)
	}
	rep.Close() // os.Exit skips defers; flush the sink explicitly.
	if err != nil {
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

	// ---- Data layer: pgx pool (non-BYPASSRLS dropway_app role) → Store. ----
	// The pool is hoisted to the run() scope so the cloud build's mountCloud can
	// build the BillingStore over the SAME non-BYPASSRLS pool (the per-event SET
	// LOCAL app.current_org_id is the isolation). It stays nil when there's no
	// DATABASE_URL, and mountCloud then skips billing.
	var st *store.Store
	var pool *pgxpool.Pool
	if cfg.DatabaseURL != "" {
		// Cap the pool (DB_MAX_CONNS overrides): the API + billing share this pool and
		// run concurrent request traffic, so it gets the largest of the Go services'
		// budgets while still leaving headroom under the shared pooler cap.
		p, err := pgpool.New(ctx, cfg.DatabaseURL, 8)
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
			PublicEndpoint:  cfg.S3PublicEndpoint,
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
	var vopts []auth.Option
	if cfg.MCPAudience != "" {
		// Also accept OAuth access tokens minted for the MCP resource, so the MCP
		// server can forward a user's token for control-plane writes. Both URL forms
		// (with/without a trailing slash) since clients canonicalize differently.
		base := strings.TrimRight(cfg.MCPAudience, "/")
		vopts = append(vopts, auth.WithExtraAudiences(base, base+"/"))
	}
	verifier := auth.NewVerifier(cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience, vopts...)
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
	api.CustomDomainsEnabled = customDomainsConfigured(cfg)
	// The edge revoker (hard-revocation denylist writer) is the SAME KV writer as
	// the route projection — both the Cloudflare KV and the local writer implement
	// projection.Revoker on the "revoked:" prefix. Wire it when the writer supports
	// it so member/site/org revocation is immediate.
	if rev, ok := proj.(handlers.EdgeRevoker); ok {
		api.Revoker = rev
	}
	// The same KV reader backs the /authz mint-time denylist check (H2): refuse to
	// re-mint for a viewer whose JWT predates a hard revocation.
	if rdr, ok := proj.(handlers.EdgeRevocationReader); ok {
		api.RevocationReader = rdr
	}
	api.AllowJWTRoleFallback = cfg.AllowJWTRoleFallback
	if cfg.AllowJWTRoleFallback {
		slog.Warn("ALLOW_JWT_ROLE_FALLBACK=true — admin gating will trust the JWT role claim when identity.member is unavailable")
	}
	// Display-URL scheme/port for the live_url / preview_url the API returns (the
	// stored host_routes.host stays the bare host). Defaults: https, no port.
	api.ContentScheme = cfg.ContentScheme
	api.ContentPort = cfg.ContentPort

	// Build the router (concrete *chi.Mux), then let the build-tag-selected
	// mountCloud add cloud-only routes onto it. In the OSS build mountCloud is a
	// no-op (wire_oss.go) → no /webhooks/stripe, no /v1/billing (self-host has no
	// billing). In the cloud build it mounts the signed Stripe webhook + the authed
	// billing routes (wire_cloud.go). The OSS route surface is therefore identical
	// whether or not this call is present.
	mux := router.New(verifier, api, baseLogger)

	// RFC 9728 protected-resource metadata: lets the CLI (`dropway login`) discover
	// the OAuth authorization server (the dashboard) and the audience to request, so
	// a browser sign-in mints a token this API accepts. `resource` is the API's own
	// audience; the dashboard registers it in the OAuth provider's validAudiences.
	mux.Get("/.well-known/oauth-protected-resource",
		oauthProtectedResource(cfg.JWTAudience, cfg.DashboardURL))

	// Product-analytics emitter (vendor-neutral seam; PostHog by default). Built once
	// here so its lifecycle is owned by run(): flushed on graceful shutdown so no
	// queued event is dropped. nil when POSTHOG_KEY is unset → analytics disabled.
	var analyticsEmitter analytics.Emitter
	if cfg.PostHogKey != "" {
		if ph, err := analytics.NewPostHog(cfg.PostHogKey, cfg.PostHogHost, cfg.Environment, baseLogger); err != nil {
			slog.Warn("analytics disabled: posthog emitter init failed", "err", err)
		} else if ph != nil {
			analyticsEmitter = ph
			defer func() { _ = ph.Close() }()
			slog.Info("product analytics emitter wired (posthog)", "environment", cfg.Environment)
		}
	}

	mountCloud(mux, cloudDeps{
		Cfg:                  cfg,
		Pool:                 pool,
		Store:                st,
		Verifier:             verifier,
		EnsureOrgProvisioned: api.EnsureOrgProvisioned,
		Projection:           proj,
		Analytics:            analyticsEmitter,
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

// customDomainsConfigured reports whether a REAL Cloudflare-for-SaaS provider is
// wired (a zone + API token). When false the custom-domain flow uses the in-memory
// fake, which can never reach "verified" — so the dashboard hides the feature
// (surfaced via /v1/me's custom_domains_enabled).
func customDomainsConfigured(cfg config.Config) bool {
	return cfg.CFZoneID != "" && cfg.CFAPIToken != ""
}

// newDomainProvider selects the custom-hostname provider. With a Cloudflare zone +
// API token it uses Cloudflare for SaaS (production); otherwise it falls back to
// the in-memory Fake (offline/self-host/dev) so the custom-domain endpoints work
// without Cloudflare.
func newDomainProvider(cfg config.Config) customdomains.Provider {
	if customDomainsConfigured(cfg) {
		slog.Info("custom-domain provider: cloudflare for saas", "zone", cfg.CFZoneID)
		return customdomains.NewCloudflareProvider(cfg.CFZoneID, cfg.CFAPIToken, projection.ContentDomain)
	}
	slog.Warn("custom-domain provider: in-memory fake (no CF_ZONE_ID/CF_API_TOKEN) — dev/self-host only")
	return customdomains.NewFake()
}

// oauthProtectedResource serves the RFC 9728 protected-resource metadata pointing
// the CLI's `dropway login` flow at the OAuth authorization server (the dashboard)
// and the audience to request. resource is this API's own audience; authServer is
// the dashboard origin. Unauthenticated + cacheable.
func oauthProtectedResource(resource, authServer string) http.HandlerFunc {
	body, _ := json.Marshal(map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{authServer},
		"bearer_methods_supported": []string{"header"},
	})
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(body)
	}
}
