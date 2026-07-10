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

	"github.com/jackc/pgx/v5"
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

// AllowAIForOrg reports whether the org may use the AI builder. In the hosted
// build a paid plan (pro/business/enterprise) is required; free orgs get a
// "plan_required" reason the dashboard turns into an upgrade prompt.
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

func (m *AIMeter) planTier(ctx context.Context, orgID string) (PlanTier, error) {
	var tier string
	err := m.pool.QueryRow(ctx,
		`SELECT plan_tier FROM app.org_meta WHERE id = $1`, orgID).Scan(&tier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TierFree, nil
		}
		return "", err
	}
	return PlanTier(tier), nil
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
