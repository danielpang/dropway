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
}

// Load reads the environment and returns a validated Config. It returns an error
// only for values that cannot be parsed (e.g. a non-numeric PORT); missing
// optional values fall back to documented defaults.
func Load() (Config, error) {
	cfg := Config{
		Port:        8080,
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWKSURL:     os.Getenv("JWKS_URL"),
		JWTIssuer:   os.Getenv("JWT_ISSUER"),
		JWTAudience: os.Getenv("JWT_AUDIENCE"),
		Cloud:       parseBool(os.Getenv("SHIPPED_CLOUD")),
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
