//go:build cloud

package billing

// analytics.go is the PROPRIETARY, cloud-only billing-analytics LOGIC: it decides
// WHAT to emit when a signature-verified Stripe webhook moves an org between tiers
// (free→pro upgrade, business→pro or →free downgrade, cancel→free) and maps it to a
// `plan_upgraded` / `plan_downgraded` product-analytics event.
//
// The TRANSPORT is the vendor-neutral, open-source internal/analytics.Emitter (a
// PostHog client by default) — so this file owns only the billing semantics, and
// the sink can be swapped (a different CDP, a no-op) without touching billing. It
// is BEST-EFFORT: CapturePlanChange never returns an error and never blocks the
// webhook (the entitlement write already committed).

import (
	"context"

	"github.com/danielpang/dropway/internal/analytics"
)

// Plan-change direction labels (also the `direction` event property).
const (
	directionNone      = ""          // no tier movement (seat/status change) → no event
	DirectionUpgrade   = "upgrade"   // moved to a higher tier
	DirectionDowngrade = "downgrade" // moved to a lower tier (incl. cancel→free)
)

// PlanChange is the resolved, tier-level movement an applied webhook produced. It
// is analytics-only: the DB write is already committed by the time one is emitted.
type PlanChange struct {
	OrgID     string
	FromTier  PlanTier
	ToTier    PlanTier
	Direction string // DirectionUpgrade | DirectionDowngrade
	Reason    string // checkout | subscription_updated | subscription_canceled | ...
}

// PlanAnalytics receives plan-change events. It is an interface so the store stays
// testable (a fake records calls) and so a deployment with no analytics sink simply
// wires nothing (the store's emitter is nil → no-op). Implementations MUST be
// best-effort: CapturePlanChange never returns an error and must not panic.
type PlanAnalytics interface {
	CapturePlanChange(ctx context.Context, ev PlanChange)
}

// tierRank orders the tiers so a change can be classified as an upgrade or a
// downgrade. An unrecognized tier ranks as Free (0) — the conservative floor — so a
// misconfigured price can never be read as an "upgrade".
func tierRank(t PlanTier) int {
	switch t {
	case TierPro:
		return 1
	case TierBusiness:
		return 2
	case TierEnterprise:
		return 3
	default: // free + anything unknown
		return 0
	}
}

// planDirection classifies a from→to tier move. Equal ranks (a seat change, a
// status refresh, or a no-op re-apply) return directionNone so no event is emitted.
func planDirection(from, to PlanTier) string {
	switch {
	case tierRank(to) > tierRank(from):
		return DirectionUpgrade
	case tierRank(to) < tierRank(from):
		return DirectionDowngrade
	default:
		return directionNone
	}
}

// reasonForEvent maps the Stripe event type to a stable, queryable `reason`
// property so the dashboard can split (e.g.) a deliberate portal downgrade from a
// cancellation. Unknown types pass through verbatim.
func reasonForEvent(eventType string) string {
	switch eventType {
	case "checkout.session.completed":
		return "checkout"
	case "customer.subscription.created":
		return "subscription_created"
	case "customer.subscription.updated":
		return "subscription_updated"
	case "customer.subscription.deleted":
		return "subscription_canceled"
	default:
		return eventType
	}
}

// eventNameForDirection is the PostHog event name for a direction. Distinct names
// (rather than one event with a property) keep the dashboard's trends trivial.
func eventNameForDirection(direction string) string {
	if direction == DirectionDowngrade {
		return "plan_downgraded"
	}
	return "plan_upgraded"
}

// emitterPlanAnalytics adapts a vendor-neutral analytics.Emitter to PlanAnalytics:
// it shapes a PlanChange into the product-analytics Event (event name, properties,
// and the org group) and hands it to the emitter.
type emitterPlanAnalytics struct {
	em analytics.Emitter
}

var _ PlanAnalytics = emitterPlanAnalytics{}

// NewPlanAnalytics wraps a generic analytics emitter as the billing PlanAnalytics.
// Returns nil when em is nil so the store treats analytics as disabled.
func NewPlanAnalytics(em analytics.Emitter) PlanAnalytics {
	if em == nil {
		return nil
	}
	return emitterPlanAnalytics{em: em}
}

// CapturePlanChange shapes and emits the plan-change event. The event is tied to
// the org via PostHog GROUP analytics ($groups via Event.Groups) AND
// distinct_id=org so per-org billing rolls up cleanly. Person-profile processing is
// disabled ($process_person_profile:false): these are system events with no acting
// user, so they should not mint a "person" per org.
func (a emitterPlanAnalytics) CapturePlanChange(ctx context.Context, ev PlanChange) {
	if ev.OrgID == "" || ev.Direction == directionNone {
		return
	}
	a.em.Capture(ctx, analytics.Event{
		DistinctID: ev.OrgID,
		Event:      eventNameForDirection(ev.Direction),
		Properties: map[string]any{
			"from_tier":               string(ev.FromTier),
			"to_tier":                 string(ev.ToTier),
			"direction":               ev.Direction,
			"reason":                  ev.Reason,
			"organization":            ev.OrgID,
			"$process_person_profile": false,
		},
		Groups: map[string]string{"organization": ev.OrgID},
	})
}
