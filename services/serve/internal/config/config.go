// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package config loads the serving server's runtime configuration from the
// environment. It is self-contained (does not import the API's config) but mirrors
// its env-var conventions and the serving Worker's config.ts defaults.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved serving-server configuration.
type Config struct {
	// Port the HTTP server listens on (SERVE_PORT, else PORT). Defaults to 8090.
	Port int

	// InternalPort is a SEPARATE, internal-only listener (SERVE_INTERNAL_PORT,
	// default 8091) for the health check + the on-demand-TLS authorization endpoint
	// Caddy calls (GET /tls-check?domain=). It is kept off the Host-routed content
	// port so /tls-check + /healthz can never collide with a tenant path, and is NOT
	// published publicly (only the reverse proxy on the compose network reaches it).
	InternalPort int

	// DatabaseURL is the Postgres DSN (DATABASE_URL) — the SAME non-BYPASSRLS
	// shipped_app role the API uses. Required to resolve hosts.
	DatabaseURL string

	// S3 / R2 object storage for blobs + manifests (server-side reads only).
	S3Endpoint        string // S3_ENDPOINT (empty → real AWS S3)
	S3Region          string // S3_REGION (R2: "auto")
	S3AccessKeyID     string // S3_ACCESS_KEY_ID
	S3SecretAccessKey string // S3_SECRET_ACCESS_KEY
	S3Bucket          string // S3_BUCKET
	S3ForcePathStyle  bool   // S3_FORCE_PATH_STYLE (true for MinIO)

	// EdgeJWKSURL is the edge signer's JWKS endpoint (EDGE_JWKS_URL). For self-host
	// point it at the API's /.well-known/edge-jwks. Defaults to the production origin
	// (config.ts DEFAULT_EDGE_JWKS_URL).
	EdgeJWKSURL string

	// AppAuthzURL is the dashboard /authz exchange a gated request with no/invalid
	// edge token 302s to (APP_AUTHZ_URL). Defaults to config.ts DEFAULT_APP_AUTHZ_URL.
	AppAuthzURL string

	// Cloudflare KV credentials for the hard-revocation denylist reader (CF_ACCOUNT_ID
	// / CF_KV_NAMESPACE_ID / CF_API_TOKEN). When set, gated revocation reads the same
	// "revoked:" prefix the API writes. When unset, a local projection file
	// (PROJECTION_FILE) may back it; if neither is configured, gated serving FAILS
	// CLOSED (no denylist reader ⇒ every gated request 302s) — match the Worker.
	CFAccountID     string
	CFKVNamespaceID string
	CFAPIToken      string

	// ProjectionFilePath (PROJECTION_FILE) is the dev/self-host local projection
	// mirror that can back the revocation reader when CF_* is unset.
	ProjectionFilePath string

	// RateLimitMax / RateLimitWindow configure the in-process rate limiter
	// (RATE_LIMIT_MAX / RATE_LIMIT_WINDOW_SECONDS). Default 600 / 60s. Max <= 0
	// disables limiting (fail open / no-op).
	RateLimitMax    int
	RateLimitWindow time.Duration
}

// Load reads the environment into a validated Config.
func Load() (Config, error) {
	cfg := Config{
		Port:         8090,
		InternalPort: 8091,
		DatabaseURL:  os.Getenv("DATABASE_URL"),

		S3Endpoint:        os.Getenv("S3_ENDPOINT"),
		S3Region:          os.Getenv("S3_REGION"),
		S3AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3Bucket:          os.Getenv("S3_BUCKET"),
		S3ForcePathStyle:  parseBool(os.Getenv("S3_FORCE_PATH_STYLE")),

		EdgeJWKSURL: envOr("EDGE_JWKS_URL", "https://api.shipped.app/.well-known/edge-jwks"),
		AppAuthzURL: envOr("APP_AUTHZ_URL", "https://app.shipped.app/authz"),

		CFAccountID:     os.Getenv("CF_ACCOUNT_ID"),
		CFKVNamespaceID: os.Getenv("CF_KV_NAMESPACE_ID"),
		CFAPIToken:      os.Getenv("CF_API_TOKEN"),

		ProjectionFilePath: os.Getenv("PROJECTION_FILE"),

		RateLimitMax:    600,
		RateLimitWindow: 60 * time.Second,
	}

	// SERVE_PORT takes precedence over PORT.
	if p := envOr("SERVE_PORT", os.Getenv("PORT")); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Config{}, fmt.Errorf("config: invalid SERVE_PORT/PORT %q: %w", p, err)
		}
		if n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("config: port %d out of range", n)
		}
		cfg.Port = n
	}

	if v := os.Getenv("SERVE_INTERNAL_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("config: invalid SERVE_INTERNAL_PORT %q", v)
		}
		cfg.InternalPort = n
	}

	if v := os.Getenv("RATE_LIMIT_MAX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: invalid RATE_LIMIT_MAX %q: %w", v, err)
		}
		cfg.RateLimitMax = n
	}
	if v := os.Getenv("RATE_LIMIT_WINDOW_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("config: invalid RATE_LIMIT_WINDOW_SECONDS %q", v)
		}
		cfg.RateLimitWindow = time.Duration(n) * time.Second
	}

	return cfg, nil
}

// Addr returns the host:port for the content ListenAndServe.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.Port) }

// InternalAddr returns the host:port for the internal (health + tls-check) listener.
func (c Config) InternalAddr() string { return fmt.Sprintf(":%d", c.InternalPort) }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
