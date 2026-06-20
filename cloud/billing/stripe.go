//go:build cloud

package billing

// stripe.go is the PROPRIETARY, cloud-only bridge to stripe-go: the real
// signature verifier (wrapping webhook.ConstructEvent) that parses a verified
// stripe.Event into this package's Event/EventData, plus the StripeClient
// interface (Checkout + Billing-Portal sessions) with a real stripe-go impl and a
// fake for tests. The StubSignatureVerifier in stripe_stub.go stays the test
// double for the webhook handler; THIS file is what runs in production.

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	stripe "github.com/stripe/stripe-go/v84"
	billingportalsession "github.com/stripe/stripe-go/v84/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v84/checkout/session"
	"github.com/stripe/stripe-go/v84/customer"
	"github.com/stripe/stripe-go/v84/webhook"
)

// timeFromUnix converts unix seconds → time.Time (UTC). Used by the store to
// store current_period_end as timestamptz. Kept here next to the other Stripe
// time handling.
func timeFromUnix(unix int64) time.Time { return time.Unix(unix, 0).UTC() }

// PriceMap resolves a Stripe price id to a PlanTier. It is built from the
// STRIPE_PRICE_PRO / STRIPE_PRICE_BUSINESS / STRIPE_PRICE_ENTERPRISE env vars so we
// never hard-code price ids, and inverted so the webhook can map a subscription's
// price → tier.
type PriceMap struct {
	pro        string
	business   string
	enterprise string
}

// NewPriceMap builds the price→tier map from the configured price ids (any may be
// empty in dev; an unmatched price then resolves to Free).
func NewPriceMap(proPrice, businessPrice, enterprisePrice string) PriceMap {
	return PriceMap{pro: proPrice, business: businessPrice, enterprise: enterprisePrice}
}

// TierFor maps a Stripe price id to a PlanTier; unknown/empty → Free. Kept for
// callers/tests that want the lenient mapping; the ENTITLEMENT path uses
// TierForChecked so an unrecognized price can't silently downgrade a paying org.
func (m PriceMap) TierFor(priceID string) PlanTier {
	tier, _ := m.TierForChecked(priceID)
	return tier
}

// TierForChecked maps a Stripe price id to a PlanTier AND reports whether the price
// was RECOGNIZED. An empty price id maps to (Free, true) — a subscription with no
// priced item is legitimately Free. A NON-EMPTY price that matches neither the
// configured business nor enterprise price returns (Free, false): the caller must
// NOT write that Free tier, or an ops misconfiguration (a new/unset STRIPE_PRICE_*)
// would silently downgrade a paying org (H6). The entitlement path treats !ok as a
// retryable error instead of a downgrade.
func (m PriceMap) TierForChecked(priceID string) (PlanTier, bool) {
	switch {
	case priceID == "":
		return TierFree, true
	case priceID == m.enterprise && m.enterprise != "":
		return TierEnterprise, true
	case priceID == m.business && m.business != "":
		return TierBusiness, true
	case priceID == m.pro && m.pro != "":
		return TierPro, true
	default:
		return TierFree, false
	}
}

// PriceFor maps a target PlanTier to the configured Stripe price id (for Checkout).
func (m PriceMap) PriceFor(tier PlanTier) (string, bool) {
	switch tier {
	case TierPro:
		return m.pro, m.pro != ""
	case TierBusiness:
		return m.business, m.business != ""
	case TierEnterprise:
		return m.enterprise, m.enterprise != ""
	default:
		return "", false
	}
}

// ---------------------------------------------------------------------------
// RealSignatureVerifier: production webhook verification + parsing.
// ---------------------------------------------------------------------------

// RealSignatureVerifier verifies the Stripe-Signature header against the endpoint
// secret via stripe-go's webhook.ConstructEvent (timestamp tolerance, the v1
// signature list, replay protection), then maps the verified stripe.Event into
// this package's Event/EventData. It is the production SignatureVerifier; tests
// use StubSignatureVerifier.
type RealSignatureVerifier struct {
	secret string
	prices PriceMap
}

// NewRealSignatureVerifier builds the verifier from the webhook signing secret
// (STRIPE_WEBHOOK_SECRET) and the price→tier map.
func NewRealSignatureVerifier(webhookSecret string, prices PriceMap) RealSignatureVerifier {
	return RealSignatureVerifier{secret: webhookSecret, prices: prices}
}

// Verify constructs and verifies the event, then resolves it to our Event. A
// signature/parse failure returns an error → the handler renders 400 and writes
// nothing (only a signed webhook may mutate entitlement).
//
// We keep the timestamp tolerance check ON (replay protection) but set
// IgnoreAPIVersionMismatch=true: the Stripe-Signature HMAC is the security
// boundary, and a Stripe account whose API version differs from the pinned
// stripe-go SDK must not hard-fail the entitlement write. We only read a small,
// stable subset of fields (org id, price, seats, status), so version drift is
// safe here; the alternative (rejecting every event until the SDK is bumped to
// match the account) would silently strand paying customers on the wrong tier.
func (v RealSignatureVerifier) Verify(payload []byte, sigHeader string) (Event, error) {
	ev, err := webhook.ConstructEventWithOptions(payload, sigHeader, v.secret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return Event{}, fmt.Errorf("billing: construct event: %w", err)
	}
	out := Event{ID: ev.ID, Type: string(ev.Type)}
	data, err := v.resolveData(ev)
	if err != nil {
		return Event{}, err
	}
	out.Data = data
	return out, nil
}

// resolveData extracts the EventData this package persists from the verified
// stripe.Event's raw object, per event type. We unmarshal ev.Data.Raw into a
// minimal, expand-agnostic shape (ids as strings) because Stripe's Customer /
// Subscription fields arrive either as a bare id string or an expanded object;
// the *idObject shapes parse both.
func (v RealSignatureVerifier) resolveData(ev stripe.Event) (EventData, error) {
	if ev.Data == nil || len(ev.Data.Raw) == 0 {
		return EventData{}, nil
	}
	switch ev.Type {
	case "checkout.session.completed":
		return v.fromCheckoutSession(ev.Data.Raw)
	case "customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted":
		return v.fromSubscription(ev.Data.Raw)
	default:
		// Other types (e.g. invoice.*) are acknowledged-and-ignored by the handler;
		// we still parse a best-effort org id so dedupe/logging have context.
		return EventData{}, nil
	}
}

// idObject parses a field that may be a bare id string OR an expanded object with
// an "id" key (Stripe expandable). UnmarshalJSON handles both.
type idObject struct{ ID string }

func (o *idObject) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		return json.Unmarshal(b, &o.ID)
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	o.ID = obj.ID
	return nil
}

// checkoutSessionRaw is the subset of a Checkout Session we read from the webhook.
type checkoutSessionRaw struct {
	ClientReferenceID string            `json:"client_reference_id"`
	Customer          idObject          `json:"customer"`
	Subscription      idObject          `json:"subscription"`
	Metadata          map[string]string `json:"metadata"`
}

func (v RealSignatureVerifier) fromCheckoutSession(raw json.RawMessage) (EventData, error) {
	var cs checkoutSessionRaw
	if err := json.Unmarshal(raw, &cs); err != nil {
		return EventData{}, fmt.Errorf("billing: parse checkout session: %w", err)
	}
	d := EventData{
		OrgID:                resolveOrgID(cs.ClientReferenceID, cs.Metadata),
		StripeCustomerID:     cs.Customer.ID,
		StripeSubscriptionID: cs.Subscription.ID,
		Status:               "active",
	}
	// Prefer the explicit target_tier from metadata (set at Checkout creation); it
	// is the tier the user is paying for. If absent, leave Free — the follow-up
	// customer.subscription.created/updated event carries the price → tier.
	if cs.Metadata != nil {
		if t := PlanTier(cs.Metadata["target_tier"]); t == TierBusiness || t == TierEnterprise {
			d.PlanTier = t
		}
	}
	if d.PlanTier == "" {
		d.PlanTier = TierFree
	}
	if d.OrgID == "" {
		return d, errors.New("billing: checkout.session.completed missing org id (client_reference_id/metadata.org_id)")
	}
	return d, nil
}

// subscriptionRaw is the subset of a Subscription object we read from the webhook.
type subscriptionRaw struct {
	ID                string            `json:"id"`
	Customer          idObject          `json:"customer"`
	Status            string            `json:"status"`
	CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	CurrentPeriodEnd  int64             `json:"current_period_end"`
	Metadata          map[string]string `json:"metadata"`
	Items             struct {
		Data []struct {
			Quantity int64 `json:"quantity"`
			Price    struct {
				ID string `json:"id"`
			} `json:"price"`
			// current_period_end can live on the item in newer API versions.
			CurrentPeriodEnd int64 `json:"current_period_end"`
		} `json:"data"`
	} `json:"items"`
}

func (v RealSignatureVerifier) fromSubscription(raw json.RawMessage) (EventData, error) {
	var sub subscriptionRaw
	if err := json.Unmarshal(raw, &sub); err != nil {
		return EventData{}, fmt.Errorf("billing: parse subscription: %w", err)
	}
	d := EventData{
		OrgID:                resolveOrgID("", sub.Metadata),
		StripeCustomerID:     sub.Customer.ID,
		StripeSubscriptionID: sub.ID,
		Status:               sub.Status,
		CancelAtPeriodEnd:    sub.CancelAtPeriodEnd,
		CurrentPeriodEnd:     sub.CurrentPeriodEnd,
		PlanTier:             TierFree,
	}
	// Derive seats + tier from the first (and, for us, only) line item's price.
	if len(sub.Items.Data) > 0 {
		item := sub.Items.Data[0]
		d.Seats = item.Quantity
		// H6: an UNRECOGNIZED non-empty price must NOT resolve to Free (that would
		// silently downgrade a paying org). Flag it so applyEvent refuses to change
		// entitlement (retryable) until the price is mapped, rather than downgrading.
		tier, ok := v.prices.TierForChecked(item.Price.ID)
		d.PlanTier = tier
		d.UnknownPrice = !ok
		if d.CurrentPeriodEnd == 0 {
			d.CurrentPeriodEnd = item.CurrentPeriodEnd
		}
	}
	if d.OrgID == "" {
		return d, errors.New("billing: subscription event missing org id (metadata.org_id)")
	}
	return d, nil
}

// resolveOrgID resolves the org id from a Checkout client_reference_id first, then
// metadata.org_id. (Resolving via the customer id is a DB lookup the webhook does
// not need here because we always stamp org_id on creation; left as the documented
// fallback order.)
func resolveOrgID(clientReferenceID string, metadata map[string]string) string {
	if clientReferenceID != "" {
		return clientReferenceID
	}
	if metadata != nil {
		if v := metadata["org_id"]; v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// StripeClient: Checkout + Billing-Portal session creation.
// ---------------------------------------------------------------------------

// CheckoutParams is the input to CreateCheckoutSession (mode=subscription).
type CheckoutParams struct {
	CustomerID        string // existing Stripe customer for the org
	PriceID           string // price for the target tier
	Quantity          int64  // seats
	ClientReferenceID string // org id (so the webhook can resolve it)
	Metadata          map[string]string
	SuccessURL        string
	CancelURL         string
}

// StripeClient is the narrow surface the Checkout/Portal handlers depend on, so
// they unit-test against a fake without hitting Stripe.
type StripeClient interface {
	// EnsureCustomer returns an existing customer id or creates one for the org.
	EnsureCustomer(existingID, orgID, email string) (string, error)
	// CreateCheckoutSession creates a subscription Checkout Session → its URL.
	CreateCheckoutSession(p CheckoutParams) (url string, err error)
	// CreatePortalSession creates a Billing Portal session → its URL.
	CreatePortalSession(customerID, returnURL string) (url string, err error)
}

// realStripeClient is the production StripeClient over stripe-go's per-resource
// clients, each bound to the secret key. We construct them with explicit Key so a
// single process can hold the cloud key without mutating the package-global
// stripe.Key.
type realStripeClient struct {
	customers customer.Client
	checkout  checkoutsession.Client
	portal    billingportalsession.Client
}

// NewStripeClient builds the production client from the secret key
// (STRIPE_SECRET_KEY). A blank key yields a client that will error on use, which
// the wiring guards against (cloud build requires the key).
func NewStripeClient(secretKey string) StripeClient {
	backend := stripe.GetBackend(stripe.APIBackend)
	return &realStripeClient{
		customers: customer.Client{B: backend, Key: secretKey},
		checkout:  checkoutsession.Client{B: backend, Key: secretKey},
		portal:    billingportalsession.Client{B: backend, Key: secretKey},
	}
}

func (c *realStripeClient) EnsureCustomer(existingID, orgID, email string) (string, error) {
	if existingID != "" {
		return existingID, nil
	}
	params := &stripe.CustomerParams{
		Metadata: map[string]string{"org_id": orgID},
	}
	if email != "" {
		params.Email = stripe.String(email)
	}
	cust, err := c.customers.New(params)
	if err != nil {
		return "", fmt.Errorf("billing: create customer for org %s: %w", orgID, err)
	}
	return cust.ID, nil
}

func (c *realStripeClient) CreateCheckoutSession(p CheckoutParams) (string, error) {
	qty := p.Quantity
	if qty < 1 {
		qty = 1
	}
	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Customer:          stripe.String(p.CustomerID),
		ClientReferenceID: stripe.String(p.ClientReferenceID),
		SuccessURL:        stripe.String(p.SuccessURL),
		CancelURL:         stripe.String(p.CancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			Price:    stripe.String(p.PriceID),
			Quantity: stripe.Int64(qty),
		}},
	}
	for k, val := range p.Metadata {
		params.AddMetadata(k, val)
	}
	// Propagate org metadata onto the created subscription too, so the
	// customer.subscription.* events can resolve the org id from metadata.org_id.
	params.SubscriptionData = &stripe.CheckoutSessionSubscriptionDataParams{
		Metadata: p.Metadata,
	}
	sess, err := c.checkout.New(params)
	if err != nil {
		return "", fmt.Errorf("billing: create checkout session: %w", err)
	}
	return sess.URL, nil
}

func (c *realStripeClient) CreatePortalSession(customerID, returnURL string) (string, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	}
	sess, err := c.portal.New(params)
	if err != nil {
		return "", fmt.Errorf("billing: create portal session: %w", err)
	}
	return sess.URL, nil
}
