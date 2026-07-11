//go:build cloud

// aisetup.go holds the one-off operator tasks that make AI pass-through billing
// land in a Stripe account:
//
//   - BootstrapAIMeter creates (idempotently) the ai_cost_cents Billing Meter and
//     the $0.01/unit metered Price it bills through, and returns the price id to
//     put in STRIPE_AI_METER_PRICE.
//   - BackfillAIMeteredItem attaches that metered price to EXISTING subscriptions,
//     so orgs that subscribed before AI metering shipped also bill on the same
//     invoice (new subscribers get it at checkout).
//
// Both are run by the operator once, via BILLING_TASK (see the cloud wiring),
// then normal request-path billing takes over.
package billing

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v84"
	meterclient "github.com/stripe/stripe-go/v84/billing/meter"
	priceclient "github.com/stripe/stripe-go/v84/price"
	productclient "github.com/stripe/stripe-go/v84/product"
	subitemclient "github.com/stripe/stripe-go/v84/subscriptionitem"
)

// BootstrapResult reports what BootstrapAIMeter created or reused.
type BootstrapResult struct {
	MeterID      string
	PriceID      string
	MeterExisted bool
	CreatedPrice bool
}

// BootstrapAIMeter ensures the ai_cost_cents Billing Meter exists and creates a
// metered Price ($0.01 per unit) that bills through it. It reuses an existing
// meter with the same event_name (Stripe allows one active meter per name in
// practice); the price is always created fresh, so run this ONCE and record the
// returned price id in STRIPE_AI_METER_PRICE.
func BootstrapAIMeter(secretKey string) (BootstrapResult, error) {
	var res BootstrapResult
	mc := meterclient.Client{B: stripe.GetBackend(stripe.APIBackend), Key: secretKey}

	// Reuse an existing active meter for the event name if there is one.
	it := mc.List(&stripe.BillingMeterListParams{Status: stripe.String("active")})
	for it.Next() {
		m := it.BillingMeter()
		if m.EventName == aiMeterEventName {
			res.MeterID = m.ID
			res.MeterExisted = true
			break
		}
	}
	if err := it.Err(); err != nil {
		return res, fmt.Errorf("billing: list meters: %w", err)
	}
	if res.MeterID == "" {
		m, err := mc.New(&stripe.BillingMeterParams{
			DisplayName: stripe.String("Dropway AI usage (cents)"),
			EventName:   stripe.String(aiMeterEventName),
			DefaultAggregation: &stripe.BillingMeterDefaultAggregationParams{
				Formula: stripe.String("sum"),
			},
			CustomerMapping: &stripe.BillingMeterCustomerMappingParams{
				Type:            stripe.String("by_id"),
				EventPayloadKey: stripe.String("stripe_customer_id"),
			},
			ValueSettings: &stripe.BillingMeterValueSettingsParams{
				EventPayloadKey: stripe.String("value"),
			},
		})
		if err != nil {
			return res, fmt.Errorf("billing: create meter: %w", err)
		}
		res.MeterID = m.ID
	}

	// A product to hang the metered price on.
	prod, err := productclient.New(&stripe.ProductParams{
		Name: stripe.String("Dropway AI usage"),
	})
	if err != nil {
		return res, fmt.Errorf("billing: create product: %w", err)
	}

	// $0.01 per unit (one unit == one cent of billed AI cost), monthly, metered
	// through the meter above.
	pr, err := priceclient.New(&stripe.PriceParams{
		Currency:   stripe.String(string(stripe.CurrencyUSD)),
		Product:    stripe.String(prod.ID),
		UnitAmount: stripe.Int64(1),
		Recurring: &stripe.PriceRecurringParams{
			Interval:  stripe.String("month"),
			UsageType: stripe.String("metered"),
			Meter:     stripe.String(res.MeterID),
		},
	})
	if err != nil {
		return res, fmt.Errorf("billing: create metered price: %w", err)
	}
	res.PriceID = pr.ID
	res.CreatedPrice = true
	return res, nil
}

// BackfillResult reports what BackfillAIMeteredItem did.
type BackfillResult struct {
	Scanned  int
	Attached int
	Skipped  int // already had the metered item
}

// BackfillAIMeteredItem attaches meteredPriceID to every existing subscription
// that doesn't already carry it, so pre-existing paid orgs bill AI usage on the
// same subscription. Idempotent: a subscription that already has the metered item
// is skipped, so it is safe to re-run.
func BackfillAIMeteredItem(ctx context.Context, pool *pgxpool.Pool, secretKey, meteredPriceID string) (BackfillResult, error) {
	var res BackfillResult
	if meteredPriceID == "" {
		return res, fmt.Errorf("billing: backfill needs a metered price id")
	}
	rows, err := pool.Query(ctx,
		`SELECT stripe_subscription_id FROM billing.subscriptions
		 WHERE stripe_subscription_id IS NOT NULL AND status IN ('active','trialing','past_due')`)
	if err != nil {
		return res, fmt.Errorf("billing: list subscriptions: %w", err)
	}
	var subIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		subIDs = append(subIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	be := stripe.GetBackend(stripe.APIBackend)
	sic := subitemclient.Client{B: be, Key: secretKey}
	for _, subID := range subIDs {
		res.Scanned++
		// Does this subscription already carry the metered price?
		has := false
		it := sic.List(&stripe.SubscriptionItemListParams{Subscription: stripe.String(subID)})
		for it.Next() {
			if item := it.SubscriptionItem(); item.Price != nil && item.Price.ID == meteredPriceID {
				has = true
			}
		}
		if err := it.Err(); err != nil {
			return res, fmt.Errorf("billing: list items for %s: %w", subID, err)
		}
		if has {
			res.Skipped++
			continue
		}
		if _, err := sic.New(&stripe.SubscriptionItemParams{
			Subscription: stripe.String(subID),
			Price:        stripe.String(meteredPriceID),
		}); err != nil {
			return res, fmt.Errorf("billing: attach metered item to %s: %w", subID, err)
		}
		res.Attached++
	}
	return res, nil
}
