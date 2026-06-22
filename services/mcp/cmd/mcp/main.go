// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Command mcp is the Dropway MCP server: an OAuth-protected, remote (Streamable
// HTTP) Model Context Protocol endpoint that lets an LLM agent list and read a
// tenant's deployed documents — including gated content — scoped to one org by
// the same Postgres RLS as the rest of the platform.
//
// Auth is OAuth 2.1: the client discovers the authorization server from
// /.well-known/oauth-protected-resource, the user logs in + authorizes in the
// browser (Better Auth on the dashboard), and the resulting bearer token is
// verified here against the platform JWKS. The org-level mcp_enabled switch is
// re-checked per request.
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

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	coreauth "github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/errtrack"
	"github.com/danielpang/dropway/internal/pgpool"
	"github.com/danielpang/dropway/internal/phclient"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/mcp/internal/apiclient"
	mcpauth "github.com/danielpang/dropway/services/mcp/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
	"github.com/danielpang/dropway/services/mcp/internal/tools"
)

func main() {
	ctx := context.Background()
	// Build the single shared posthog client (main owns its lifecycle) and wire
	// error tracking over it first, so every log.Error mirrors to the sink. Provider
	// is runtime-selected; Noop when unconfigured.
	phClient, phErr := phclient.New(phclient.ConfigFromEnv())
	if phErr != nil {
		slog.Warn("posthog client init failed — error tracking disabled", "err", phErr)
	}
	rep, label := errtrack.FromEnv("mcp", phClient)
	log := slog.New(rep.WrapSlogHandler(slog.NewJSONHandler(os.Stderr, nil)))
	slog.SetDefault(log)
	log.Info("error tracking wired", "provider", label)
	// main owns the shared client (the reporter only borrows it). flush drains it
	// before an os.Exit, which would otherwise skip deferred cleanup.
	flush := func() {
		if phClient != nil {
			_ = phClient.Close()
		}
	}
	// fatal logs (→ captured), flushes the sink, then exits.
	fatal := func(msg string, err error) {
		log.Error(msg, "err", err)
		flush()
		os.Exit(1)
	}

	dbURL := mustEnv(flush, log, "DATABASE_URL")
	jwksURL := mustEnv(flush, log, "JWKS_URL")
	publicURL := strings.TrimRight(mustEnv(flush, log, "MCP_PUBLIC_URL"), "/") // this server's external URL
	dashboardURL := mustEnv(flush, log, "DASHBOARD_URL")                       // the OAuth authorization server
	issuer := os.Getenv("JWT_ISSUER")
	port := getenv("MCP_PORT", "8092")

	// Read-only, lighter traffic than the API: a small cap is plenty and leaves
	// shared-pooler headroom. DB_MAX_CONNS overrides.
	pool, err := pgpool.New(ctx, dbURL, 4)
	if err != nil {
		fatal("db pool", err)
	}
	defer pool.Close()

	objStore, err := storage.NewS3Store(ctx, storage.S3Config{
		Bucket:          os.Getenv("S3_BUCKET"),
		Region:          os.Getenv("S3_REGION"),
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		UsePathStyle:    os.Getenv("S3_FORCE_PATH_STYLE") == "true",
	})
	if err != nil {
		fatal("object storage", err)
	}

	// The bearer token is a Better-Auth-issued OAuth access token whose audience is
	// this MCP resource (publicURL); verify it against the platform JWKS. Accept
	// several canonical forms of the resource the client may have requested:
	//   - publicURL          the bare RFC 9728 resource we advertise
	//   - publicURL+"/"      clients that append a trailing slash (e.g. mcp-remote)
	//   - publicURL+"/mcp"   clients that use the connection URL (".../mcp") as the
	//   - publicURL+"/mcp/"  RFC 8707 resource (e.g. Claude's built-in connector)
	// The dashboard registers the same set in validAudiences so the issued token's aud
	// matches whatever form the client sent.
	verifier := coreauth.NewVerifier(jwksURL, issuer, publicURL,
		coreauth.WithExtraAudiences(publicURL+"/", publicURL+"/mcp", publicURL+"/mcp/"))
	st := store.New(pool)
	svc := &tools.Service{Store: st, Blobs: objStore}

	// Control-plane WRITE tools (create_site, set_site_access) call the Go API,
	// forwarding the user's token (the API accepts the MCP audience). Without an
	// API_URL the server stays read-only and those tools are not registered.
	if apiURL := os.Getenv("API_URL"); apiURL != "" {
		// Presigned blob URLs come back signed against the browser-facing public
		// endpoint (S3_PUBLIC_ENDPOINT, e.g. localhost:9000), which this server —
		// uploading server-side from inside the compose network — can't reach. Route
		// uploads to the internal object-store endpoint (S3_ENDPOINT, e.g. minio:9000)
		// while preserving the signed Host header so the signature still verifies.
		svc.API = apiclient.New(apiURL, apiclient.WithUploadEndpoint(os.Getenv("S3_ENDPOINT")))
		log.Info("control-plane write tools enabled", "api_url", apiURL)
	} else {
		log.Info("API_URL not set — MCP server is read-only (no create_site/set_site_access)")
	}

	mux := newMux(verifier, st, svc, publicURL, dashboardURL)

	srv := &http.Server{
		Addr: ":" + port,
		// errtrack.Recoverer recovers panics in MCP request handling, captures them,
		// and returns a clean 500.
		Handler:           errtrack.Recoverer(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("mcp listening", "addr", srv.Addr, "resource", publicURL, "authz", dashboardURL)

	// Run the listener on a goroutine so we can select against the shutdown signal.
	// A graceful drain on SIGINT/SIGTERM is what lets flush() actually drain the
	// shared client before the process exits — without it, errors captured since the
	// last flush interval would be lost on every deploy.
	listenErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-listenErr:
		fatal("serve", err)
	case sig := <-stop:
		log.Info("shutdown signal received, draining", "signal", sig.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	flush() // drain the shared client before exit.
}

// newMux wires the HTTP surface: /healthz, the RFC 9728 protected-resource
// metadata, and the OAuth-gated + mcp_enabled-gated /mcp Streamable-HTTP handler.
// Extracted from main so the integration test can stand the server up in-process
// against a test JWKS + a real Postgres-backed store.
func newMux(verifier *coreauth.Verifier, st *store.Store, svc *tools.Service, publicURL, dashboardURL string) http.Handler {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "dropway", Version: "v1"}, nil)
	tools.Register(server, svc)
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server }, nil)

	resourceMetaURL := publicURL + "/.well-known/oauth-protected-resource"

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		// Verify the DB is actually reachable, not just that the process is up. A
		// wrong/unreachable DATABASE_URL would otherwise pass health (the process
		// runs fine) while every DB-backed request 403s — exactly how a misconfigured
		// DSN hid in production. Failing health here makes a bad DSN fail the deploy
		// loudly instead. Short timeout so a slow DB can't wedge the check.
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := st.Ping(ctx); err != nil {
			slog.Warn("healthz: database ping failed", "err", err.Error())
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource",
		protectedResourceMetadata(publicURL, dashboardURL))

	// /mcp: validate the OAuth token (→ tenant) → enforce the org mcp_enabled
	// switch → hand to the MCP Streamable-HTTP handler.
	protected := mcpauth.Middleware(verifier, resourceMetaURL,
		requireMCPEnabled(st, mcpHandler))
	mux.Handle("/mcp", protected)
	mux.Handle("/mcp/", protected)
	return mux
}

// requireMCPEnabled rejects requests for an org whose admin/owner has turned MCP
// off — re-checked per request so a disable takes effect immediately.
func requireMCPEnabled(st *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, ok := mcpauth.TenantFromContext(r.Context())
		if !ok {
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}
		enabled, err := st.MCPEnabled(r.Context(), t)
		switch {
		case errors.Is(err, store.ErrNotFound):
			// No app.org_meta row for this org yet. The row is created lazily by the
			// Go API's ensure-org-provisioned step (on the first API-backed tool call),
			// so a brand-new org — or one that has only ever used the dashboard — may
			// not have it when MCP is first connected. mcp_enabled DEFAULTS to true, so
			// a MISSING row means "not provisioned / not killed", NOT "disabled". Treat
			// it as enabled and let the request through; the first tool call provisions
			// it. (Previously this 403'd silently — the cause of "auth with the MCP
			// server failed" right after a successful OAuth.)
			slog.Info("mcp: no org_meta row yet, allowing (default-enabled)",
				"org_id", t.OrgID, "user_id", t.UserID, "path", r.URL.Path)
		case err != nil:
			// A genuine lookup error (RLS / connectivity / query) — distinct from the
			// missing-row case above. Log it so it's diagnosable rather than a silent 403.
			slog.Warn("mcp auth: org_meta check failed (returning 403)",
				"err", err.Error(), "org_id", t.OrgID, "user_id", t.UserID, "path", r.URL.Path)
			http.Error(w, "403 Forbidden: could not resolve organization access.",
				http.StatusForbidden)
			return
		case !enabled:
			// Explicit admin/owner kill-switch.
			slog.Warn("mcp auth: MCP disabled for org (returning 403)",
				"org_id", t.OrgID, "path", r.URL.Path)
			http.Error(w, "403 Forbidden: MCP access is disabled for this organization.",
				http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// protectedResourceMetadata serves the RFC 9728 document that points MCP clients
// at the Dropway authorization server (the dashboard's Better Auth).
func protectedResourceMetadata(resource, authServer string) http.HandlerFunc {
	body, _ := json.Marshal(map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{authServer},
		"scopes_supported":         []string{"mcp"},
		"bearer_methods_supported": []string{"header"},
	})
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(body)
	}
}

func mustEnv(flush func(), log *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Error("missing required env", "key", key)
		flush() // drain the shared client before os.Exit skips deferred cleanup.
		os.Exit(1)
	}
	return v
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
