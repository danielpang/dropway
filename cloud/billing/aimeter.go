//go:build cloud

// aimeter.go is the PROPRIETARY, cloud-only pass-through billing for the AI
// website builder. Users pay exactly what OpenRouter charges plus a flat 3% card
// processing fee (break-even; marketed as "no markup"). Each recorded generation
// is reported to a Stripe Billing Meter as cents on the org's subscription.
//
// The AI gate here also restricts the builder to paid plans in the hosted build:
// metered postpaid without a card on file is a collections problem, and a paid
// plan implies a card via the existing Checkout flow.
package billing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v84"
	meterevent "github.com/stripe/stripe-go/v84/billing/meterevent"
)

// aiFeePct is the flat card-processing fee added to the raw OpenRouter cost. It
// recovers Stripe's ~2.9% + fixed fee at break-even; the marketing claim ("no
// markup") is true because Dropway keeps none of it.
const aiFeePct = 0.03

// aiMeterEventName is the Stripe Billing Meter event_name the metered price is
// configured against. The metered price is $0.01 per unit, so one unit == one
// cent of billed AI cost.
const aiMeterEventName = "ai_cost_cents"

// AIMeter reports AI usage to Stripe and answers the paid-plan gate. It reads the
// org's Stripe customer id + plan tier from billing.subscriptions over the same
// non-BYPASSRLS pool the rest of billing uses.
type AIMeter struct {
	pool   *pgxpool.Pool
	secret string
	feePct float64
	sendFn func(customerID, identifier string, valueCents int64) error // overridable in tests
}

// NewAIMeter builds the meter over the billing pool + Stripe secret key.
func NewAIMeter(pool *pgxpool.Pool, stripeSecretKey string) *AIMeter {
	m := &AIMeter{pool: pool, secret: stripeSecretKey, feePct: aiFeePct}
	m.sendFn = m.sendMeterEvent
	return m
}

// ReportUsage pushes one generation's pass-through cost (+ fee) to Stripe as a
// meter event. It is idempotent on the generation id (Stripe enforces identifier
// uniqueness within a rolling window), so a retry never double-bills. A zero-cost
// generation (free model) reports nothing.
//
// The signature matches ai.Runner's UsageReporter seam (assigned in wire_cloud.go).
func (m *AIMeter) ReportUsage(ctx context.Context, orgID, generationID string, costUSD float64) error {
	if costUSD <= 0 {
		return nil
	}
	customerID, err := m.customerID(ctx, orgID)
	if err != nil {
		return err
	}
	if customerID == "" {
		return fmt.Errorf("billing: org %s has no Stripe customer for AI metering", orgID)
	}
	cents := ceilCents(costUSD, m.feePct)
	if cents <= 0 {
		return nil
	}
	return m.sendFn(customerID, generationID, cents)
}

// ceilCents computes the billed cents for a raw cost with the fee gross-up,
// rounding UP so a fractional cent never under-recovers the fee.
func ceilCents(costUSD, feePct float64) int64 {
	return int64(math.Ceil(costUSD * (1 + feePct) * 100))
}

// AllowAIForOrg reports whether the org may use the AI builder. A paid plan
// (pro/business/enterprise) is required — a paying org always has a card on file
// (checkout requires one), so the plan tier is the same gate the rest of the app
// uses for paid features (e.g. custom domains), kept consistent here rather than
// a bespoke billing query. Free orgs get a "plan_required" reason.
//
// This is the CLOUD billing gate and only ONE of the AI checks — the org-level
// ai_enabled admin toggle (settable during onboarding) gates the builder
// independently and is enforced before this.
func (m *AIMeter) AllowAIForOrg(ctx context.Context, orgID string) (bool, string, error) {
	tier, err := m.planTier(ctx, orgID)
	if err != nil {
		return false, "", err
	}
	if tier == TierFree || tier == "" {
		return false, "plan_required", nil
	}
	return true, "", nil
}

// AllowMemoryForOrg reports whether the org may use org memory (retrieval,
// extraction, content indexing, and the /v1/ai/memories surface). Same
// paid-plan bar as the AI builder: Pro and above. Kept as its OWN method (not
// an alias of AllowAIForOrg) so the two features can diverge in tier later
// without an API change.
func (m *AIMeter) AllowMemoryForOrg(ctx context.Context, orgID string) (bool, string, error) {
	tier, err := m.planTier(ctx, orgID)
	if err != nil {
		return false, "", err
	}
	if tier == TierFree || tier == "" {
		return false, "plan_required", nil
	}
	return true, "", nil
}

// planTier reads the org's live entitlement tier from billing.subscriptions (the
// billing module's own mirror, written by the same webhook as app.org_meta.plan_tier
// in one tx). It reads billing.subscriptions rather than app.org_meta ON PURPOSE:
// app.org_meta is FORCE ROW LEVEL SECURITY, and this query runs over the bare
// billing pool WITHOUT the app.current_org_id tenant GUC, so a SELECT from
// app.org_meta would be RLS-filtered to zero rows for EVERY org and resolve to
// Free — blocking the AI builder for paying orgs too. billing.subscriptions has
// no RLS, so the tier reads correctly. Fail-soft to Free when the row is absent
// (a free org that never subscribed has none).
// planTierQuery reads the entitlement tier from billing.subscriptions (no RLS),
// NOT app.org_meta (FORCE RLS — would return zero rows over the bare pool). A
// regression test asserts this targets billing.subscriptions.
const planTierQuery = `SELECT plan_tier FROM billing.subscriptions WHERE org_id = $1`

func (m *AIMeter) planTier(ctx context.Context, orgID string) (PlanTier, error) {
	var tier string
	err := m.pool.QueryRow(ctx, planTierQuery, orgID).Scan(&tier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TierFree, nil
		}
		return "", err
	}
	return PlanTier(tier), nil
}

// BillingPeriodStart returns the start of the org's CURRENT Stripe billing
// period, so the AI spend window (cap + "usage this month") lines up with the
// exact day the subscription renews, and reconciles with the invoice. ok is
// false when the org has no subscription or no recorded period yet, in which case
// the caller falls back to the calendar month.
func (m *AIMeter) BillingPeriodStart(ctx context.Context, orgID string) (start time.Time, ok bool, err error) {
	var ts pgtype.Timestamptz
	qerr := m.pool.QueryRow(ctx,
		`SELECT current_period_start FROM billing.subscriptions WHERE org_id = $1`, orgID).
		Scan(&ts)
	if qerr != nil {
		if errors.Is(qerr, pgx.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, qerr
	}
	if !ts.Valid {
		return time.Time{}, false, nil
	}
	return ts.Time, true, nil
}

func (m *AIMeter) customerID(ctx context.Context, orgID string) (string, error) {
	var id string
	err := m.pool.QueryRow(ctx,
		`SELECT COALESCE(stripe_customer_id, '') FROM billing.subscriptions WHERE org_id = $1`, orgID).
		Scan(&id)
	if err != nil {
		// No subscription row → no customer yet (treated as "" by the caller).
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return id, nil
}


// sendMeterEvent posts a Billing Meter event to Stripe.
func (m *AIMeter) sendMeterEvent(customerID, identifier string, valueCents int64) error {
	client := meterevent.Client{B: stripe.GetBackend(stripe.APIBackend), Key: m.secret}
	_, err := client.New(&stripe.BillingMeterEventParams{
		EventName:  stripe.String(aiMeterEventName),
		Identifier: stripe.String(identifier),
		Payload: map[string]string{
			"stripe_customer_id": customerID,
			"value":              strconv.FormatInt(valueCents, 10),
		},
	})
	if err != nil {
		return fmt.Errorf("billing: send AI meter event: %w", err)
	}
	return nil
}

// SweepUnreported re-sends meter events for ledger rows whose report failed. The
// caller (ops) supplies the unreported rows (org, generation id, cost) and marks
// each reported on success. Returns the count re-sent.
func (m *AIMeter) SweepUnreported(ctx context.Context, rows []UnreportedUsage, markReported func(id string) error) (int, error) {
	sent := 0
	for _, row := range rows {
		if err := m.ReportUsage(ctx, row.OrgID, row.GenerationID, row.CostUSD); err != nil {
			continue // leave it unreported for the next sweep
		}
		if err := markReported(row.RowID); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// UnreportedUsage is one ledger row the meter retry sweep must re-send.
type UnreportedUsage struct {
	RowID        string
	OrgID        string
	GenerationID string
	CostUSD      float64
}
