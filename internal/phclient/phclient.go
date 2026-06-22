// Package phclient is the single place a posthog-go client is constructed for the
// Go services. It exists so error tracking (internal/errtrack) and product
// analytics (internal/analytics) can SHARE one client per process — one
// background flusher goroutine, one batch queue, one HTTP connection pool —
// instead of each calling posthog.NewWithConfig and spinning up its own.
//
// The builder is explicit (New takes a Config) so it stays testable: callers can
// point it at an httptest server. ConfigFromEnv reads the shared POSTHOG_KEY /
// POSTHOG_HOST / ENVIRONMENT vars for the common case.
package phclient

import (
	"os"
	"strings"
	"time"

	"github.com/posthog/posthog-go"
)

// Config is the resolved PostHog client configuration.
type Config struct {
	// Key is the project ingest key (phc_…). Empty ⇒ New returns a nil client
	// (PostHog disabled).
	Key string
	// Host is the ingest endpoint. Empty ⇒ posthog-go's default (US cloud ingest).
	Host string
	// Environment is stamped on every event/exception as the `environment` property
	// so PostHog can segment deploys. Empty ⇒ not stamped.
	Environment string
}

// ConfigFromEnv reads the shared PostHog vars from the environment. POSTHOG_KEY is
// the one secret used across error tracking, product analytics, and (separately)
// the edge worker + dashboard.
func ConfigFromEnv() Config {
	return Config{
		Key:         strings.TrimSpace(os.Getenv("POSTHOG_KEY")),
		Host:        strings.TrimSpace(os.Getenv("POSTHOG_HOST")),
		Environment: strings.TrimSpace(os.Getenv("ENVIRONMENT")),
	}
}

// New builds a posthog-go client from cfg. It returns (nil, nil) when cfg.Key is
// empty so callers can treat a nil client as "PostHog disabled" without special
// casing. The config unifies what the two seams previously set separately:
// BatchSize 1 (dispatch each event promptly rather than waiting for a full batch)
// and a bounded ShutdownTimeout (so a flush on Close can't wedge a deploy).
func New(cfg Config) (posthog.Client, error) {
	if cfg.Key == "" {
		return nil, nil
	}
	c := posthog.Config{
		BatchSize:       1,
		ShutdownTimeout: 3 * time.Second,
	}
	if cfg.Host != "" {
		c.Endpoint = cfg.Host
	}
	if cfg.Environment != "" {
		// DefaultEventProperties rides on every captured event, so callers don't have
		// to restamp the deploy label.
		c.DefaultEventProperties = posthog.NewProperties().Set("environment", cfg.Environment)
	}
	return posthog.NewWithConfig(cfg.Key, c)
}
