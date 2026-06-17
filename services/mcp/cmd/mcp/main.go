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
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	coreauth "github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/storage"
	mcpauth "github.com/danielpang/dropway/services/mcp/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
	"github.com/danielpang/dropway/services/mcp/internal/tools"
)

func main() {
	ctx := context.Background()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	dbURL := mustEnv(log, "DATABASE_URL")
	jwksURL := mustEnv(log, "JWKS_URL")
	publicURL := strings.TrimRight(mustEnv(log, "MCP_PUBLIC_URL"), "/") // this server's external URL
	dashboardURL := mustEnv(log, "DASHBOARD_URL")                      // the OAuth authorization server
	issuer := os.Getenv("JWT_ISSUER")
	port := getenv("MCP_PORT", "8092")

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Error("db pool", "err", err)
		os.Exit(1)
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
		log.Error("object storage", "err", err)
		os.Exit(1)
	}

	// The bearer token is a Better-Auth-issued OAuth access token whose audience is
	// this MCP resource (publicURL); verify it against the platform JWKS.
	verifier := coreauth.NewVerifier(jwksURL, issuer, publicURL)
	st := store.New(pool)
	svc := &tools.Service{Store: st, Blobs: objStore}

	mux := newMux(verifier, st, svc, publicURL, dashboardURL)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("mcp listening", "addr", srv.Addr, "resource", publicURL, "authz", dashboardURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
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
		if err != nil || !enabled {
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

func mustEnv(log *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Error("missing required env", "key", key)
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
