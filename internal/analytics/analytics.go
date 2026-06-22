// Package analytics is a small, vendor-neutral product-analytics seam shared by
// the Dropway services. It defines a transport-agnostic Emitter interface so
// callers enqueue product events without depending on any one analytics vendor,
// plus a PostHog implementation (the default sink, posthog.go).
//
// It lives in internal/ (open-source, Apache-2.0) — NOT behind the cloud build
// tag — so the self-host build can use it too, and a different backend (e.g. a
// RudderStack/Segment CDP, or a no-op) can be dropped in behind the same Emitter
// interface without touching call sites. The business logic that DECIDES what to
// emit (e.g. billing's plan upgrade/downgrade derivation) stays in its own package
// and depends only on this interface.
package analytics

import "context"

// Event is a single product-analytics event: who (DistinctID), what (Event), and
// free-form Properties, optionally associated with analytics Groups (e.g. an
// organization) for group-level rollups. Intentionally transport-agnostic.
type Event struct {
	// DistinctID is the event's actor id. For system/org-level events with no acting
	// user this is typically the org id (paired with a Groups["organization"] entry).
	DistinctID string
	// Event is the event name (e.g. "plan_upgraded").
	Event string
	// Properties is the free-form property bag recorded with the event.
	Properties map[string]any
	// Groups associates the event with analytics groups: group type → group key
	// (e.g. {"organization": "org_123"}) so PostHog can roll up per-org.
	Groups map[string]string
}

// Emitter sends product-analytics events to a sink. Implementations MUST be
// best-effort: Capture never returns an error and must neither panic nor block the
// caller's critical path.
//
// An Emitter is a pure consumer of its transport: it does NOT own the underlying
// client and has no Close. The composition root (each service's main) owns the
// shared client's lifecycle and flushes it on shutdown.
type Emitter interface {
	Capture(ctx context.Context, ev Event)
}
