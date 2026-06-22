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
	"fmt"
	"log/slog"
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
		// Surface delivery problems. posthog-go's Enqueue only QUEUES — the HTTP
		// send happens on a background goroutine, and without these hooks a rejected
		// batch (e.g. a wrong/invalid project key → 401, or an unreachable endpoint)
		// is silently dropped. Callback.Failure fires once a message is permanently
		// discarded; the Logger surfaces transport-level errors. Both go to slog at
		// WARN so "events aren't showing up in PostHog" is diagnosable from the logs
		// instead of being invisible.
		Callback: failureLogger{},
		Logger:   slogSDKLogger{},
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

// failureLogger implements posthog.Callback: it logs every message the SDK
// permanently fails to deliver, at WARN, so dropped events are visible. Success is
// intentionally a no-op (delivery is the expected case; logging it would be noise).
type failureLogger struct{}

func (failureLogger) Success(posthog.APIMessage) {}
func (failureLogger) Failure(_ posthog.APIMessage, err error) {
	slog.Warn("posthog: event delivery failed (event dropped)", "err", err)
}

// slogSDKLogger adapts posthog-go's Logger to slog. Transport errors/warnings
// surface at WARN (e.g. a 401 from a bad key, or a failing endpoint); the SDK's
// chatty debug/info output is dropped so it doesn't flood the logs.
type slogSDKLogger struct{}

func (slogSDKLogger) Debugf(string, ...any) {}
func (slogSDKLogger) Logf(string, ...any)   {}
func (slogSDKLogger) Warnf(format string, args ...any) {
	slog.Warn("posthog sdk: " + fmt.Sprintf(format, args...))
}
func (slogSDKLogger) Errorf(format string, args ...any) {
	slog.Warn("posthog sdk: " + fmt.Sprintf(format, args...))
}
