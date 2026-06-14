// Package config loads the Go API's runtime configuration from the environment.
// Values map 1:1 to the variables documented in the infra agent's .env.example.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the resolved server configuration.
type Config struct {
	// Port the HTTP server listens on (PORT). Defaults to 8080.
	Port int

	// DatabaseURL is the Postgres DSN (DATABASE_URL). Optional in Phase 1 — the
	// publish/serve loop is stubbed — but logged when absent so it's obvious.
	DatabaseURL string

	// JWKSURL, JWTIssuer, JWTAudience configure the EdDSA JWT verifier. The Go
	// API is the authz boundary and verifies every token against this JWKS.
	JWKSURL     string
	JWTIssuer   string
	JWTAudience string

	// Cloud selects the hosted build's quota/billing enforcement at runtime
	// (SHIPPED_CLOUD). The OSS binary ignores this — the cloud provider is only
	// linked in under the `cloud` build tag — but it's surfaced here so the
	// cloud build can read it and the OSS build can warn if it's set.
	Cloud bool

	// AllowJWTRoleFallback controls whether admin-gated actions may fall back to
	// the verified JWT role claim when the Better Auth auth.member table is
	// unavailable (ALLOW_JWT_ROLE_FALLBACK). Default FALSE (strict): if membership/
	// role can't be confirmed live, admin-gated actions are DENIED rather than
	// trusting the claim. A self-host that hasn't migrated Better Auth yet can opt
	// in by setting this true (ARCHITECTURE.md §10 [LOW]).
	AllowJWTRoleFallback bool

	// S3 / R2 object storage for blobs + manifests. Optional in Phase 1 (the
	// publish/serve loop is unavailable without it). Works against MinIO locally
	// (path-style endpoint) and Cloudflare R2 in production.
	S3Endpoint        string // S3_ENDPOINT (empty → real AWS S3)
	S3Region          string // S3_REGION (R2: "auto")
	S3AccessKeyID     string // S3_ACCESS_KEY_ID
	S3SecretAccessKey string // S3_SECRET_ACCESS_KEY
	S3Bucket          string // S3_BUCKET
	S3ForcePathStyle  bool   // S3_FORCE_PATH_STYLE (true for MinIO)

	// Cloudflare KV projection (the edge routing projection writer). Optional in
	// Phase 1: when unset, a local/in-memory projection writer is used so the
	// publish path still works offline (the self-host serving path reads it).
	CFAccountID     string // CF_ACCOUNT_ID
	CFKVNamespaceID string // CF_KV_NAMESPACE_ID
	CFAPIToken      string // CF_API_TOKEN

	// CFZoneID is the Cloudflare zone for the custom-hostname (Cloudflare for SaaS)
	// API. When set together with CF_API_TOKEN, the real custom-domain provider is
	// wired; otherwise the in-memory Fake is used (offline/self-host/dev).
	CFZoneID string // CF_ZONE_ID

	// EdgeSigningKey is the Ed25519 key (32-byte seed or 64-byte key; base64/hex)
	// for the edge-token signer — a SEPARATE keypair from Better Auth's user JWT
	// (EDGE_SIGNING_KEY). When empty, the server GENERATES an ephemeral key at
	// startup and logs the seed (dev convenience; tokens won't survive a restart).
	EdgeSigningKey string

	// ProjectionFilePath, when set (PROJECTION_FILE), mirrors the local projection
	// writer to a JSON file (the offline/self-host serving shim reads it).
	ProjectionFilePath string
}

// Load reads the environment and returns a validated Config. It returns an error
// only for values that cannot be parsed (e.g. a non-numeric PORT); missing
// optional values fall back to documented defaults.
func Load() (Config, error) {
	cfg := Config{
		Port:                 8080,
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		JWKSURL:              os.Getenv("JWKS_URL"),
		JWTIssuer:            os.Getenv("JWT_ISSUER"),
		JWTAudience:          os.Getenv("JWT_AUDIENCE"),
		Cloud:                parseBool(os.Getenv("SHIPPED_CLOUD")),
		AllowJWTRoleFallback: parseBool(os.Getenv("ALLOW_JWT_ROLE_FALLBACK")),

		S3Endpoint:        os.Getenv("S3_ENDPOINT"),
		S3Region:          os.Getenv("S3_REGION"),
		S3AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3Bucket:          os.Getenv("S3_BUCKET"),
		S3ForcePathStyle:  parseBool(os.Getenv("S3_FORCE_PATH_STYLE")),

		CFAccountID:     os.Getenv("CF_ACCOUNT_ID"),
		CFKVNamespaceID: os.Getenv("CF_KV_NAMESPACE_ID"),
		CFAPIToken:      os.Getenv("CF_API_TOKEN"),
		CFZoneID:        os.Getenv("CF_ZONE_ID"),

		EdgeSigningKey: os.Getenv("EDGE_SIGNING_KEY"),

		ProjectionFilePath: os.Getenv("PROJECTION_FILE"),
	}

	if p := os.Getenv("PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Config{}, fmt.Errorf("config: invalid PORT %q: %w", p, err)
		}
		if n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("config: PORT %d out of range", n)
		}
		cfg.Port = n
	}

	return cfg, nil
}

// Addr returns the host:port string for ListenAndServe.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.Port) }

// parseBool treats the common truthy spellings as true; everything else
// (including empty) is false. self-host defaults to false.
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
