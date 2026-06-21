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

	// MCPAudience (MCP_PUBLIC_URL) is the Dropway MCP server's resource URL. When
	// set, the API ALSO accepts OAuth access tokens minted for the MCP resource
	// (aud=MCP_PUBLIC_URL), so the MCP server can forward a user's token to perform
	// control-plane writes (create site / change access) — reusing the API's quota,
	// admin re-check, edge-projection, revocation, and audit (the API stays the only
	// projection writer). The token's iss is the same as a dashboard JWT, and
	// admin-gating re-checks the live member table from org+user, so no extra claims
	// are needed. Empty → MCP-token writes are not accepted.
	MCPAudience string

	// Cloud selects the hosted build's quota/billing enforcement at runtime
	// (DROPWAY_CLOUD). The OSS binary ignores this — the cloud provider is only
	// linked in under the `cloud` build tag — but it's surfaced here so the
	// cloud build can read it and the OSS build can warn if it's set.
	Cloud bool

	// AllowJWTRoleFallback controls whether admin-gated actions may fall back to
	// the verified JWT role claim when the Better Auth identity.member table is
	// unavailable (ALLOW_JWT_ROLE_FALLBACK). Default FALSE (strict): if membership/
	// role can't be confirmed live, admin-gated actions are DENIED rather than
	// trusting the claim. A self-host that hasn't migrated Better Auth yet can opt
	// in by setting this true ([LOW]).
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

	// S3PublicEndpoint is the BROWSER-reachable object-store host that presigned
	// upload URLs are signed against (S3_PUBLIC_ENDPOINT). Server-side reads/writes
	// use S3Endpoint (the internal host, e.g. http://minio:9000), but a browser
	// folder drag-and-drop deploy PUTs blobs DIRECTLY to the presigned URL — so that
	// URL's host must be one the browser can resolve (e.g. http://localhost:9000
	// locally, or the public R2/custom-domain host in production). Empty → fall back
	// to S3Endpoint (correct when the store is already a public host, e.g. real R2).
	S3PublicEndpoint string // S3_PUBLIC_ENDPOINT

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

	// ---- Cloud-only billing (Stripe). These are read ONLY by the cloud build's
	// mountCloud (wire_cloud.go) to wire cloud/billing; the OSS build ignores them
	// and never mounts /webhooks/stripe or /v1/billing (self-host has no billing).
	// They're declared here (not under a build tag) so the
	// single Config type is shared; documented cloud-only in deploy/.env.example. ----

	// StripeSecretKey is the restricted Stripe API key (STRIPE_SECRET_KEY) used to
	// create Checkout + Billing-Portal sessions and Customers.
	StripeSecretKey string
	// StripeWebhookSecret (STRIPE_WEBHOOK_SECRET) verifies the Stripe-Signature on
	// the inbound /webhooks/stripe payload — the ONLY thing that may mutate the paid
	// entitlement.
	StripeWebhookSecret string
	// StripePricePro / StripePriceBusiness / StripePriceEnterprise are the Stripe
	// Price ids for the self-serve tiers (STRIPE_PRICE_PRO / STRIPE_PRICE_BUSINESS /
	// STRIPE_PRICE_ENTERPRISE). They map a checkout target_tier → price and a webhook
	// subscription price → plan_tier. STRIPE_PRICE_PRO is the $25 tier; STRIPE_PRICE_BUSINESS
	// is the $150 unlimited-sites tier.
	StripePricePro        string
	StripePriceBusiness   string
	StripePriceEnterprise string

	// DashboardURL is the dashboard origin (DASHBOARD_URL) used for Checkout
	// success/cancel + Billing-Portal return URLs. Defaults to https://app.dropway.dev.
	DashboardURL string

	// PostHogKey / PostHogHost configure server-side PostHog capture for cloud
	// billing analytics (plan upgrade/downgrade events emitted from the Stripe
	// webhook). PostHogKey is the project ingest key (POSTHOG_KEY, a `phc_…` value);
	// PostHogHost is the ingest host (POSTHOG_HOST, default PostHog US cloud). Read
	// ONLY by the cloud build; UNSET → billing analytics is disabled (no-op).
	PostHogKey  string
	PostHogHost string

	// Environment is the deploy label (ENVIRONMENT) stamped on every analytics event
	// so PostHog can segment production from staging/dev. Defaults to "development".
	Environment string

	// EnforceStorageQuota gates the cloud per-org STORAGE cap (ENFORCE_STORAGE_QUOTA).
	// Defaults to FALSE: storage is metered/tracked but a deploy is never rejected for
	// crossing a storage band — the only paid lever today is the per-org site count
	// The cap code stays in cloud/quota; flip this to true once
	// storage billing ships so an over-band org is held until it upgrades. No effect in
	// the OSS build (Unlimited ignores it).
	EnforceStorageQuota bool

	// ContentScheme / ContentPort configure how the API renders the DISPLAY URLs it
	// returns to clients (live_url / preview_url): scheme://host[:port]. They affect
	// ONLY the displayed URL — the stored host_routes.host (and the route:<host> KV
	// key) stays the bare host, since the serving server resolves by Host header and
	// strips the port. ContentScheme defaults to "https" (CONTENT_SCHEME); ContentPort
	// defaults to "" — no explicit port (CONTENT_PORT). A self-host/dev deployment can
	// set CONTENT_SCHEME=http and CONTENT_PORT=8443 to point clients at a local edge.
	ContentScheme string // CONTENT_SCHEME (default "https")
	ContentPort   string // CONTENT_PORT (default "" → no explicit port)
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
		MCPAudience:          os.Getenv("MCP_PUBLIC_URL"),
		Cloud:                parseBool(os.Getenv("DROPWAY_CLOUD")),
		AllowJWTRoleFallback: parseBool(os.Getenv("ALLOW_JWT_ROLE_FALLBACK")),

		S3Endpoint:        os.Getenv("S3_ENDPOINT"),
		S3Region:          os.Getenv("S3_REGION"),
		S3AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3Bucket:          os.Getenv("S3_BUCKET"),
		S3ForcePathStyle:  parseBool(os.Getenv("S3_FORCE_PATH_STYLE")),
		S3PublicEndpoint:  os.Getenv("S3_PUBLIC_ENDPOINT"),

		CFAccountID:     os.Getenv("CF_ACCOUNT_ID"),
		CFKVNamespaceID: os.Getenv("CF_KV_NAMESPACE_ID"),
		CFAPIToken:      os.Getenv("CF_API_TOKEN"),
		CFZoneID:        os.Getenv("CF_ZONE_ID"),

		EdgeSigningKey: os.Getenv("EDGE_SIGNING_KEY"),

		ProjectionFilePath: os.Getenv("PROJECTION_FILE"),

		StripeSecretKey:       os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret:   os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePricePro:        os.Getenv("STRIPE_PRICE_PRO"),
		StripePriceBusiness:   os.Getenv("STRIPE_PRICE_BUSINESS"),
		StripePriceEnterprise: os.Getenv("STRIPE_PRICE_ENTERPRISE"),
		DashboardURL:          envOr("DASHBOARD_URL", "https://app.dropway.dev"),
		PostHogKey:            os.Getenv("POSTHOG_KEY"),
		PostHogHost:           envOr("POSTHOG_HOST", "https://us.i.posthog.com"),
		Environment:           envOr("ENVIRONMENT", "development"),
		EnforceStorageQuota:   parseBool(os.Getenv("ENFORCE_STORAGE_QUOTA")),

		ContentScheme: envOr("CONTENT_SCHEME", "https"),
		ContentPort:   os.Getenv("CONTENT_PORT"),
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

	// SECURITY (audit HIGH): when a JWKS is configured (the API verifies real
	// Better Auth tokens), JWT_ISSUER and JWT_AUDIENCE MUST be set. golang-jwt v5
	// SKIPS the iss/aud check when the expected value is empty, so an empty
	// JWT_ISSUER/JWT_AUDIENCE would accept ANY EdDSA-signed, unexpired token
	// regardless of who minted it or for whom. Fail fast rather than run auth that
	// doesn't pin iss/aud.
	if cfg.JWKSURL != "" {
		if cfg.JWTIssuer == "" {
			return Config{}, fmt.Errorf("config: JWT_ISSUER must be set when JWKS_URL is configured (issuer is otherwise unenforced)")
		}
		if cfg.JWTAudience == "" {
			return Config{}, fmt.Errorf("config: JWT_AUDIENCE must be set when JWKS_URL is configured (audience is otherwise unenforced)")
		}
	}

	return cfg, nil
}

// Addr returns the host:port string for ListenAndServe.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.Port) }

// envOr returns the environment value for key, or def when it's unset/empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

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
