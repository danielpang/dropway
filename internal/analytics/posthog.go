// posthog.go is the default Emitter: it forwards product-analytics events to
// PostHog using the official, open-source posthog-go client
// (github.com/posthog/posthog-go, MIT). Using the maintained library — rather than
// hand-rolling HTTP against the capture endpoint — gets us batching, retries, and
// the canonical capture payload for free.
//
// DELIVERY: posthog-go batches in a background goroutine. We set BatchSize:1 so an
// event is dispatched promptly instead of waiting for a full batch, and the API is
// long-lived so the flusher delivers it; Close() (wired into graceful shutdown)
// drains anything still queued so a deploy/restart doesn't drop in-flight events.

package analytics

import (
	"context"
	"log/slog"

	"github.com/posthog/posthog-go"
)

// PostHog is the PostHog-backed Emitter. Construct with NewPostHog.
type PostHog struct {
	client posthog.Client
	log    *slog.Logger
}

var _ Emitter = (*PostHog)(nil)

// NewPostHog builds the PostHog emitter. host is the ingest host (posthog-go
// defaults to PostHog US cloud when empty); environment, when set, is stamped on
// every event as the `environment` property so PostHog can segment deploys.
//
// Returns (nil, nil) when apiKey is empty so a deployment without analytics simply
// wires nothing — callers treat a nil emitter as disabled.
func NewPostHog(apiKey, host, environment string, log *slog.Logger) (*PostHog, error) {
	if apiKey == "" {
		return nil, nil
	}
	if log == nil {
		log = slog.Default()
	}
	cfg := posthog.Config{
		// BatchSize 1 → dispatch each event promptly rather than waiting for a full
		// batch; Close() drains anything pending on shutdown.
		BatchSize: 1,
	}
	if host != "" {
		cfg.Endpoint = host
	}
	if environment != "" {
		// DefaultEventProperties rides on every captured event, so callers never have
		// to restamp the deploy label.
		cfg.DefaultEventProperties = posthog.NewProperties().Set("environment", environment)
	}
	client, err := posthog.NewWithConfig(apiKey, cfg)
	if err != nil {
		return nil, err
	}
	return &PostHog{client: client, log: log}, nil
}

// Capture enqueues an event. Best-effort: a nil receiver, a missing event
// name/distinct id, or an enqueue error is silently ignored (debug-logged) so
// analytics can never break the caller.
func (p *PostHog) Capture(_ context.Context, ev Event) {
	if p == nil || p.client == nil || ev.Event == "" || ev.DistinctID == "" {
		return
	}

	props := posthog.NewProperties()
	for k, v := range ev.Properties {
		props.Set(k, v)
	}

	capture := posthog.Capture{
		DistinctId: ev.DistinctID,
		Event:      ev.Event,
		Properties: props,
	}
	if len(ev.Groups) > 0 {
		groups := posthog.NewGroups()
		for k, v := range ev.Groups {
			groups.Set(k, v)
		}
		capture.Groups = groups
	}

	if err := p.client.Enqueue(capture); err != nil {
		p.log.Debug("analytics: posthog enqueue failed", "event", ev.Event, "err", err)
	}
}

// Close flushes buffered events and shuts the client down. Safe on a nil receiver.
func (p *PostHog) Close() error {
	if p == nil || p.client == nil {
		return nil
	}
	return p.client.Close()
}
