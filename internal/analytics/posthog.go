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

// PostHog is the PostHog-backed Emitter. It is a pure consumer of the posthog-go
// client: construct with NewPostHogFromClient and let the composition root (main)
// own the client's lifecycle.
type PostHog struct {
	client posthog.Client
	log    *slog.Logger
}

var _ Emitter = (*PostHog)(nil)

// NewPostHogFromClient builds the emitter over the shared posthog-go client the
// composition root built and owns (the same client used for error tracking, so the
// process runs ONE client). The emitter BORROWS the client and never closes it.
// Returns nil when client is nil (PostHog disabled), so callers treat a nil emitter
// as disabled.
func NewPostHogFromClient(client posthog.Client, log *slog.Logger) *PostHog {
	if client == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &PostHog{client: client, log: log}
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
