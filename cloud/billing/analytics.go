//go:build cloud

package billing

// analytics.go is the PROPRIETARY, cloud-only PostHog emission for billing plan
// changes. When a signature-verified Stripe webhook moves an org between tiers
// (free→pro upgrade, business→pro or →free downgrade, cancel→free), we emit a
// `plan_upgraded` / `plan_downgraded` event to PostHog so the growth dashboard can
// track conversion and churn.
//
// DELIVERY MODEL — flush before returning. The Go API is long-lived, but we still
// SEND each event SYNCHRONOUSLY over HTTP at the moment it happens rather than
// handing it to a background batch queue. Billing events are rare and high-value,
// so a buffered client that could drop un-flushed events on a deploy/restart is the
// wrong trade: a direct, bounded POST guarantees the event is on the wire before
// the webhook handler returns, with nothing left buffered. It is BEST-EFFORT —
// every failure is logged, never returned — so analytics can never fail a webhook
// (the entitlement write already committed).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
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
// testable (a fake records calls) and so a deployment with no PostHog key simply
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

// defaultPostHogHost is PostHog US cloud's INGEST host (the `i.` subdomain is the
// event-capture endpoint, distinct from the app host).
const defaultPostHogHost = "https://us.i.posthog.com"

// PostHogAnalytics is the live PlanAnalytics: a synchronous HTTP capture against
// PostHog's /capture/ endpoint. Construct with NewPostHogAnalytics.
type PostHogAnalytics struct {
	apiKey      string
	endpoint    string // {host}/capture/
	environment string // stamped as the `environment` property (prod/staging/…)
	http        *http.Client
	log         *slog.Logger
}

var _ PlanAnalytics = (*PostHogAnalytics)(nil)

// NewPostHogAnalytics builds the emitter. host defaults to PostHog US cloud's
// ingest host; environment is the deploy label stamped on every event. A short
// client timeout bounds the synchronous send so a slow PostHog can't hold the
// webhook handler open. Returns nil when apiKey is empty so callers can wire
// "nothing" (the store treats a nil emitter as disabled).
func NewPostHogAnalytics(apiKey, host, environment string, log *slog.Logger) *PostHogAnalytics {
	if apiKey == "" {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	host = strings.TrimRight(host, "/")
	if host == "" {
		host = defaultPostHogHost
	}
	return &PostHogAnalytics{
		apiKey:      apiKey,
		endpoint:    host + "/capture/",
		environment: environment,
		http:        &http.Client{Timeout: 5 * time.Second},
		log:         log,
	}
}

// CapturePlanChange POSTs a single plan-change event to PostHog. It is best-effort:
// any failure is logged and swallowed (the entitlement change already committed; a
// missed analytics event must never surface to Stripe).
//
// The event is tied to the org via PostHog GROUP analytics ($groups.organization)
// AND distinct_id=org so per-org billing rolls up cleanly. Person-profile
// processing is disabled ($process_person_profile:false): these are system events
// with no acting user, so they should not mint a "person" per org.
func (p *PostHogAnalytics) CapturePlanChange(ctx context.Context, ev PlanChange) {
	if p == nil || ev.OrgID == "" || ev.Direction == directionNone {
		return
	}

	body, err := json.Marshal(map[string]any{
		"api_key":     p.apiKey,
		"event":       eventNameForDirection(ev.Direction),
		"distinct_id": ev.OrgID,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"properties": map[string]any{
			"from_tier":               string(ev.FromTier),
			"to_tier":                 string(ev.ToTier),
			"direction":               ev.Direction,
			"reason":                  ev.Reason,
			"environment":             p.environment,
			"organization":            ev.OrgID,
			"$groups":                 map[string]string{"organization": ev.OrgID},
			"$process_person_profile": false,
			"$lib":                    "dropway-cloud-api",
		},
	})
	if err != nil {
		p.log.Warn("posthog: marshal plan-change event failed", "org_id", ev.OrgID, "err", err)
		return
	}

	// Detach from the request's cancellation (a Stripe disconnect must not abort an
	// already-earned event) but keep a hard deadline so the send can't hang the
	// webhook response.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		p.log.Warn("posthog: build request failed", "org_id", ev.OrgID, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		p.log.Warn("posthog: plan-change capture failed", "org_id", ev.OrgID, "err", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain so the connection can be reused; PostHog returns a tiny {"status":1}.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		p.log.Warn("posthog: plan-change capture non-2xx",
			"org_id", ev.OrgID, "event", eventNameForDirection(ev.Direction), "status", resp.StatusCode)
		return
	}
	p.log.Debug("posthog: plan-change captured",
		"org_id", ev.OrgID, "event", eventNameForDirection(ev.Direction),
		"from", string(ev.FromTier), "to", string(ev.ToTier), "reason", ev.Reason)
}
