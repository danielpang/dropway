//go:build cloud

package billing

// analytics.go is the PROPRIETARY, cloud-only billing-analytics LOGIC: it decides
// WHAT to emit when a signature-verified Stripe webhook moves an org between tiers
// (free→pro upgrade, business→pro or →free downgrade, cancel→free) and maps it to a
// `plan_upgraded` / `plan_downgraded` product-analytics event. A paid renewal on
// the same plan is its own `subscription_renewed` event, never an upgrade or a
// downgrade.
//
// The TRANSPORT is the vendor-neutral, open-source internal/analytics.Emitter (a
// PostHog client by default) — so this file owns only the billing semantics, and
// the sink can be swapped (a different CDP, a no-op) without touching billing. It
// is BEST-EFFORT: CapturePlanChange never returns an error and never blocks the
// webhook (the entitlement write already committed).

import (
	"context"
	"strings"

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
	CaptureRenewal(ctx context.Context, r Renewal)
}

// Renewal is a paid recurring charge on the org's current subscription: a new
// billing period on the SAME plan. It is reported as its own event,
// subscription_renewed, never as a plan upgrade or downgrade.
type Renewal struct {
	OrgID          string
	SubscriptionID string
	InvoiceID      string
	PlanTier       PlanTier
	AmountPaid     int64  // minor units (cents), as Stripe sends it
	Currency       string // ISO code, lowercase as Stripe sends it
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

// tierMRRUSD is each self-serve tier's monthly price in USD, mirroring the
// dashboard's display pricing (apps/dashboard/lib/billing.ts) the same way the
// store mirrors cloud/quota's site caps. known=false for Enterprise (custom,
// negotiated pricing — a wrong number is worse than none) and for any
// unrecognized tier.
func tierMRRUSD(t PlanTier) (usd float64, known bool) {
	switch t {
	case TierFree:
		return 0, true
	case TierPro:
		return 25, true
	case TierBusiness:
		return 150, true
	default: // enterprise (custom pricing) + anything unknown
		return 0, false
	}
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
	props := map[string]any{
		"from_tier":               string(ev.FromTier),
		"to_tier":                 string(ev.ToTier),
		"direction":               ev.Direction,
		"reason":                  ev.Reason,
		"organization":            ev.OrgID,
		"$process_person_profile": false,
	}
	// Revenue tracking: stamp the monthly value of both tiers and the movement,
	// so PostHog can sum `mrr_delta_usd` into an MRR-movement trend and read new
	// MRR off upgrades directly. Omitted (not zeroed) when either side has custom
	// pricing (Enterprise) — those deals are tracked manually, and a fake $0
	// would corrupt the sums.
	if fromUSD, ok := tierMRRUSD(ev.FromTier); ok {
		if toUSD, ok := tierMRRUSD(ev.ToTier); ok {
			props["from_tier_mrr_usd"] = fromUSD
			props["to_tier_mrr_usd"] = toUSD
			props["mrr_delta_usd"] = toUSD - fromUSD
		}
	}
	a.em.Capture(ctx, analytics.Event{
		DistinctID: ev.OrgID,
		Event:      eventNameForDirection(ev.Direction),
		Properties: props,
		Groups:     map[string]string{"organization": ev.OrgID},
	})
}

// renewalEventName is the PostHog event for a paid subscription renewal.
const renewalEventName = "subscription_renewed"

// CaptureRenewal emits subscription_renewed for a paid renewal, tied to the org
// the same way as plan changes. amount_paid is in the currency's major units
// (Adaptive Pricing can bill in local currency, so currency is always included);
// mrr_usd is the plan's list price, for a USD trend that ignores currency, and is
// omitted for custom-priced tiers.
func (a emitterPlanAnalytics) CaptureRenewal(ctx context.Context, r Renewal) {
	if r.OrgID == "" {
		return
	}
	props := map[string]any{
		"plan_tier":               string(r.PlanTier),
		"subscription_id":         r.SubscriptionID,
		"invoice_id":              r.InvoiceID,
		"amount_paid":             majorUnits(r.AmountPaid, r.Currency),
		"currency":                strings.ToUpper(r.Currency),
		"organization":            r.OrgID,
		"$process_person_profile": false,
	}
	if usd, ok := tierMRRUSD(r.PlanTier); ok {
		props["mrr_usd"] = usd
	}
	a.em.Capture(ctx, analytics.Event{
		DistinctID: r.OrgID,
		Event:      renewalEventName,
		Properties: props,
		Groups:     map[string]string{"organization": r.OrgID},
	})
}

// zeroDecimalCurrencies are the currencies Stripe charges in whole units (no
// cents), from Stripe's currency documentation.
var zeroDecimalCurrencies = map[string]bool{
	"bif": true, "clp": true, "djf": true, "gnf": true, "jpy": true, "kmf": true,
	"krw": true, "mga": true, "pyg": true, "rwf": true, "ugx": true, "vnd": true,
	"vuv": true, "xaf": true, "xof": true, "xpf": true,
}

// majorUnits converts a Stripe minor-unit amount to the currency's major units.
func majorUnits(amount int64, currency string) float64 {
	if zeroDecimalCurrencies[strings.ToLower(currency)] {
		return float64(amount)
	}
	return float64(amount) / 100
}
