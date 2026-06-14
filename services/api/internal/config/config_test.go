package config

import (
	"testing"
)

// allEnvKeys is every variable Load reads. Tests clear them so the host
// environment can't leak into (or out of) a case.
var allEnvKeys = []string{
	"PORT", "DATABASE_URL", "JWKS_URL", "JWT_ISSUER", "JWT_AUDIENCE",
	"SHIPPED_CLOUD", "ALLOW_JWT_ROLE_FALLBACK",
	"S3_ENDPOINT", "S3_REGION", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY",
	"S3_BUCKET", "S3_FORCE_PATH_STYLE",
	"CF_ACCOUNT_ID", "CF_KV_NAMESPACE_ID", "CF_API_TOKEN", "CF_ZONE_ID",
	"EDGE_SIGNING_KEY", "PROJECTION_FILE",
	"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET",
	"STRIPE_PRICE_BUSINESS", "STRIPE_PRICE_ENTERPRISE", "DASHBOARD_URL",
}

// clearEnv unsets every variable Load reads (t.Setenv restores afterward, but we
// must start from a known-empty baseline so the default path is exercised).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range allEnvKeys {
		t.Setenv(k, "")
	}
}

// TestLoad_Defaults asserts that with no env set, Load returns the documented
// defaults and no error (every optional value is allowed to be empty).
func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with empty env should not error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("default Port = %d, want 8080", cfg.Port)
	}
	if cfg.DashboardURL != "https://app.shipped.app" {
		t.Errorf("default DashboardURL = %q, want https://app.shipped.app", cfg.DashboardURL)
	}
	// Bools default false (self-host posture).
	if cfg.Cloud || cfg.AllowJWTRoleFallback || cfg.S3ForcePathStyle {
		t.Errorf("bool defaults should be false: %+v", cfg)
	}
	// Optional strings default empty.
	if cfg.DatabaseURL != "" || cfg.JWKSURL != "" || cfg.S3Bucket != "" || cfg.StripeSecretKey != "" {
		t.Errorf("optional strings should default empty: %+v", cfg)
	}
}

// TestLoad_EveryEnvVarParsed sets every variable and asserts each maps onto the
// right Config field (a misrouted env var is the classic config bug).
func TestLoad_EveryEnvVarParsed(t *testing.T) {
	clearEnv(t)
	set := map[string]string{
		"PORT":                    "9999",
		"DATABASE_URL":            "postgres://db",
		"JWKS_URL":                "https://app/jwks",
		"JWT_ISSUER":              "https://app",
		"JWT_AUDIENCE":            "shipped-api",
		"SHIPPED_CLOUD":           "true",
		"ALLOW_JWT_ROLE_FALLBACK": "yes",
		"S3_ENDPOINT":             "http://minio:9000",
		"S3_REGION":               "auto",
		"S3_ACCESS_KEY_ID":        "akid",
		"S3_SECRET_ACCESS_KEY":    "secret",
		"S3_BUCKET":               "shipped-blobs",
		"S3_FORCE_PATH_STYLE":     "1",
		"CF_ACCOUNT_ID":           "acct",
		"CF_KV_NAMESPACE_ID":      "ns",
		"CF_API_TOKEN":            "cftok",
		"CF_ZONE_ID":              "zone",
		"EDGE_SIGNING_KEY":        "seedbytes",
		"PROJECTION_FILE":         "/tmp/projection.json",
		"STRIPE_SECRET_KEY":       "sk_test",
		"STRIPE_WEBHOOK_SECRET":   "whsec",
		"STRIPE_PRICE_BUSINESS":   "price_b",
		"STRIPE_PRICE_ENTERPRISE": "price_e",
		"DASHBOARD_URL":           "https://dash.example",
	}
	for k, v := range set {
		t.Setenv(k, v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, 9999},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://db"},
		{"JWKSURL", cfg.JWKSURL, "https://app/jwks"},
		{"JWTIssuer", cfg.JWTIssuer, "https://app"},
		{"JWTAudience", cfg.JWTAudience, "shipped-api"},
		{"Cloud", cfg.Cloud, true},
		{"AllowJWTRoleFallback", cfg.AllowJWTRoleFallback, true},
		{"S3Endpoint", cfg.S3Endpoint, "http://minio:9000"},
		{"S3Region", cfg.S3Region, "auto"},
		{"S3AccessKeyID", cfg.S3AccessKeyID, "akid"},
		{"S3SecretAccessKey", cfg.S3SecretAccessKey, "secret"},
		{"S3Bucket", cfg.S3Bucket, "shipped-blobs"},
		{"S3ForcePathStyle", cfg.S3ForcePathStyle, true},
		{"CFAccountID", cfg.CFAccountID, "acct"},
		{"CFKVNamespaceID", cfg.CFKVNamespaceID, "ns"},
		{"CFAPIToken", cfg.CFAPIToken, "cftok"},
		{"CFZoneID", cfg.CFZoneID, "zone"},
		{"EdgeSigningKey", cfg.EdgeSigningKey, "seedbytes"},
		{"ProjectionFilePath", cfg.ProjectionFilePath, "/tmp/projection.json"},
		{"StripeSecretKey", cfg.StripeSecretKey, "sk_test"},
		{"StripeWebhookSecret", cfg.StripeWebhookSecret, "whsec"},
		{"StripePriceBusiness", cfg.StripePriceBusiness, "price_b"},
		{"StripePriceEnterprise", cfg.StripePriceEnterprise, "price_e"},
		{"DashboardURL", cfg.DashboardURL, "https://dash.example"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestLoad_PortErrors asserts the only validated value (PORT) is rejected when it
// cannot be parsed or is out of the TCP range — the documented error contract.
func TestLoad_PortErrors(t *testing.T) {
	cases := []struct {
		name string
		port string
	}{
		{"non-numeric", "abc"},
		{"empty-but-set-space", " "}, // strconv.Atoi(" ") fails
		{"zero", "0"},
		{"negative", "-1"},
		{"too-large", "65536"},
		{"way-too-large", "99999999"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("PORT", c.port)
			cfg, err := Load()
			if err == nil {
				t.Fatalf("PORT=%q should error, got cfg=%+v", c.port, cfg)
			}
			// On error, the returned Config is the zero value (caller must not use it).
			if cfg != (Config{}) {
				t.Errorf("error path should return zero Config, got %+v", cfg)
			}
		})
	}
}

// TestLoad_PortBoundaries asserts the inclusive valid range [1,65535].
func TestLoad_PortBoundaries(t *testing.T) {
	for _, p := range []struct {
		val  string
		want int
	}{
		{"1", 1},
		{"65535", 65535},
		{"3000", 3000},
	} {
		clearEnv(t)
		t.Setenv("PORT", p.val)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("PORT=%q should be valid: %v", p.val, err)
		}
		if cfg.Port != p.want {
			t.Errorf("PORT=%q → Port=%d, want %d", p.val, cfg.Port, p.want)
		}
	}
}

// TestParseBool covers every truthy spelling and the false-on-anything-else rule.
// Note Load never ERRORS on a bad bool — an unrecognized value is simply false
// (the conservative self-host default), so a typo silently disables a flag rather
// than crashing the server.
func TestParseBool(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "True", "yes", "YES", "on", "ON", "  on  "}
	for _, s := range truthy {
		if !parseBool(s) {
			t.Errorf("parseBool(%q) = false, want true", s)
		}
	}
	falsy := []string{"", "0", "false", "no", "off", "nope", "2", "  ", "y", "enabled"}
	for _, s := range falsy {
		if parseBool(s) {
			t.Errorf("parseBool(%q) = true, want false", s)
		}
	}
}

// TestLoad_BadBoolIsFalseNotError confirms the documented "no error on bad bool"
// behavior end to end through Load.
func TestLoad_BadBoolIsFalseNotError(t *testing.T) {
	clearEnv(t)
	t.Setenv("SHIPPED_CLOUD", "definitely-not-a-bool")
	t.Setenv("S3_FORCE_PATH_STYLE", "maybe")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("a bad bool must not error: %v", err)
	}
	if cfg.Cloud || cfg.S3ForcePathStyle {
		t.Errorf("unrecognized bool should resolve to false: Cloud=%v PathStyle=%v", cfg.Cloud, cfg.S3ForcePathStyle)
	}
}

// TestEnvOr covers the default-fallback helper directly (set value wins; empty
// falls back to the default).
func TestEnvOr(t *testing.T) {
	t.Setenv("SOME_KEY", "value")
	if got := envOr("SOME_KEY", "def"); got != "value" {
		t.Errorf("envOr set = %q, want value", got)
	}
	t.Setenv("SOME_KEY", "")
	if got := envOr("SOME_KEY", "def"); got != "def" {
		t.Errorf("envOr empty = %q, want def (fallback)", got)
	}
}

// TestAddr asserts the host:port string handed to ListenAndServe.
func TestAddr(t *testing.T) {
	if got := (Config{Port: 8080}).Addr(); got != ":8080" {
		t.Errorf("Addr = %q, want :8080", got)
	}
	if got := (Config{Port: 443}).Addr(); got != ":443" {
		t.Errorf("Addr = %q, want :443", got)
	}
}
