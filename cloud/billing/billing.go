//go:build cloud

// Package billing (cloud) is the PROPRIETARY, hosted-only Stripe integration. It
// is compiled only under the `cloud` build tag and is NOT part of the FSL/
// self-host build (cloud/LICENSE).
//
// This file is the webhook handler skeleton. The architectural invariant it
// encodes: the paid entitlement (plan_tier) is written to the DB ONLY by a
// signature-verified webhook, never by the browser's success redirect. The
// handler therefore (1) verifies the Stripe-Signature, (2) hands the verified
// event to the store, which dedupes AND applies the entitlement change in a
// SINGLE transaction, and (3) acknowledges. All external dependencies (signature
// verify, event processing) are interfaces so this compiles and unit-tests
// without a live Stripe or database.
package billing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/danielpang/dropway/internal/httpx"
)

// PlanTier mirrors billing.subscriptions.plan_tier.
type PlanTier string

const (
	TierFree       PlanTier = "free"
	TierPro        PlanTier = "pro"
	TierBusiness   PlanTier = "business"
	TierEnterprise PlanTier = "enterprise"
)

// Event is the minimal, Stripe-shaped event this handler reasons about after the
// raw payload has been verified and parsed. The full cloud build unmarshals the
// real stripe.Event; this struct is the subset the persistence mapping needs.
type Event struct {
	ID   string    // Stripe event id (PK for dedupe: processed_stripe_events)
	Type string    // e.g. "checkout.session.completed"
	Data EventData // resolved subscription/customer fields
}

// EventData carries the resolved fields used to upsert a subscription row.
type EventData struct {
	OrgID                string // from client_reference_id / metadata / customer lookup
	StripeCustomerID     string
	StripeSubscriptionID string
	PlanTier             PlanTier // derived from the subscription's price
	Seats                int64
	Status               string // active | past_due | canceled | ...
	CurrentPeriodStart   int64  // unix seconds (subscription's current billing period start)
	CurrentPeriodEnd     int64  // unix seconds
	CancelAtPeriodEnd    bool
	// UnknownPrice is set when a subscription line item carried a non-empty price id
	// that matched NEITHER configured tier price (H6). applyEvent then refuses to
	// change entitlement (retryable) rather than silently downgrading to Free.
	UnknownPrice bool

	// Invoice fields, set only for invoice.paid. BillingReason is Stripe's reason
	// for the invoice: "subscription_cycle" marks a recurring renewal charge (as
	// opposed to "subscription_create" for the first one or
	// "subscription_update" for a proration). AmountPaid is in the currency's
	// minor units (cents), exactly as Stripe sends it.
	InvoiceID     string
	BillingReason string
	AmountPaid    int64
	Currency      string
}

// isEntitledStatus reports whether a Stripe subscription status grants the paid
// tier. `active`/`trialing` are entitled; `past_due` stays entitled through the
// dunning grace (it restricts NEW actions + shows a banner, but doesn't cut a
// paying customer's live sites). Every other status — `unpaid`, `incomplete`,
// `incomplete_expired`, `paused`, `canceled`, or anything unrecognized — is NOT
// entitled, so applyEvent downgrades to Free instead of granting the price's tier
// (M6: a non-paying subscription must never be recorded as active+paid).
func isEntitledStatus(status string) bool {
	switch status {
	case "active", "trialing", "past_due":
		return true
	default:
		return false
	}
}

// SignatureVerifier verifies the raw request body against the Stripe-Signature
// header using the endpoint's webhook secret, returning the parsed Event. The
// real implementation wraps stripe webhook.ConstructEvent; the stub below
// documents the contract and lets the handler compile/test.
type SignatureVerifier interface {
	Verify(payload []byte, sigHeader string) (Event, error)
}

// EventProcessor records the Stripe event id in the dedupe ledger AND applies the
// entitlement change for that event in ONE transaction (idempotency + the
// lost-update fix). It reports applied=false when the id was already present (a
// replay), in which case nothing was changed. Because the ledger INSERT and the
// entitlement write share a transaction, a failed apply rolls BOTH back: the
// handler 500s, Stripe retries, and the retry — seeing no ledger row — re-applies
// cleanly. An unhandled event type is still recorded (so it isn't reprocessed)
// but is otherwise a no-op (applied=true, no entitlement write).
//
// This single seam replaces the previous two-step dedupe→apply sequence that
// committed the ledger row in its own autocommit tx BEFORE the entitlement write:
// if the write then failed, the recorded id permanently short-circuited every
// retry to "duplicate_ignored" and the paying customer's plan_tier never flipped.
type EventProcessor interface {
	ProcessEvent(ctx context.Context, ev Event) (applied bool, err error)
}

// SubscriptionStore persists entitlement. UpsertSubscription is the ONLY place
// plan_tier is written; SetCanceled handles customer.subscription.deleted by
// downgrading to Free without destroying data (over-limit → read-only). The
// store's ProcessEvent dispatches to these inside the dedupe+apply transaction.
type SubscriptionStore interface {
	// CurrentSubscription returns the subscription the org's entitlement is
	// currently tracking (the zero value when the org has none). applyEvent uses
	// it to drop events from any OTHER subscription (one subscription per org).
	CurrentSubscription(ctx context.Context, orgID string) (CurrentSubscription, error)
	UpsertSubscription(ctx context.Context, d EventData) error
	SetCanceled(ctx context.Context, orgID string) error
	// OrgForSubscription returns the org whose row tracks the subscription ("" if
	// none). Used when an invoice carries no org_id metadata.
	OrgForSubscription(ctx context.Context, subscriptionID string) (string, error)
	// RecordRenewal notes a paid renewal for the post-commit analytics. It
	// changes no entitlement.
	RecordRenewal(ctx context.Context, r Renewal) error
}

// CurrentSubscription is the org's tracked Stripe subscription as recorded in
// billing.subscriptions: its id, tier and Stripe status.
type CurrentSubscription struct {
	ID       string
	PlanTier PlanTier
	Status   string
}

// live reports whether this subscription is still a paying entitlement — a paid
// tier on an entitled status. A row created by SaveCustomerID before the first
// Checkout (free / no subscription id) is NOT live.
func (c CurrentSubscription) live() bool {
	return c.ID != "" && c.PlanTier != "" && c.PlanTier != TierFree && isEntitledStatus(c.Status)
}

// ignoreForeignSubscription reports whether an event must be dropped because it
// belongs to a subscription other than the org's current one. An org has exactly
// ONE subscription; if a second one exists in Stripe (e.g. a Pro subscription left
// running after a separate Business Checkout), its renewals must not overwrite the
// org's tier — that is what flipped one org business→pro→business every month.
//
//   - Same subscription, or either id unknown → apply (the normal path).
//   - A cancel / non-paying event for another subscription → ignore: the org's
//     current subscription is unaffected by that one ending.
//   - A paying event for another subscription while the current one is no longer
//     live → apply (a re-subscribe after a cancel).
//   - A paying event for another subscription while the current one is live →
//     apply only if it is on a HIGHER tier, so an org that already pays for two
//     subscriptions settles on the higher one whichever event it tracked first;
//     ignore it otherwise. An unmapped price ranks as Free, so it never wins.
func ignoreForeignSubscription(cur CurrentSubscription, eventSubID string, eventTier PlanTier, cancel bool) bool {
	if eventSubID == "" || cur.ID == "" || eventSubID == cur.ID {
		return false
	}
	if cancel {
		return true
	}
	if !cur.live() {
		return false
	}
	return tierRank(eventTier) <= tierRank(cur.PlanTier)
}

// errIgnoredEvent marks an event that was deliberately not applied because it
// belongs to a subscription other than the org's current one. ProcessEvent still
// records it in the dedupe ledger and the handler acknowledges it with a 200, but
// reports it as ignored rather than applied.
var errIgnoredEvent = errors.New("billing: event ignored (not the org's current subscription)")

// Handler is the /webhooks/stripe HTTP handler.
type Handler struct {
	verifier  SignatureVerifier
	processor EventProcessor
	log       *slog.Logger
}

// NewHandler constructs the webhook handler. A nil logger uses slog.Default.
func NewHandler(v SignatureVerifier, p EventProcessor, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{verifier: v, processor: p, log: log}
}

// maxBody caps the webhook body to avoid a memory-amplification DoS; Stripe
// events are small (a few KB).
const maxBody = 1 << 20 // 1 MiB

// ServeHTTP implements the webhook flow:
//
//  1. read + size-limit the body
//  2. verify the Stripe-Signature (else 400 — never trust an unsigned body)
//  3. ProcessEvent: in ONE tx, INSERT event.id into processed_stripe_events
//     (ON CONFLICT → duplicate, no-op) AND apply the entitlement change. A failed
//     apply rolls back the ledger row too, so Stripe's retry re-applies cleanly.
//  4. 200 OK (whether applied, a duplicate, or an unfulfillable event we 400)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, httpx.ErrorBody{Error: "method_not_allowed"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorBody{Error: "bad_request", Message: "read body"})
		return
	}

	// (2) Signature verification. A failure here is a hard 400 — the success
	// redirect grants nothing; only a signed webhook may mutate entitlement.
	ev, err := h.verifier.Verify(body, r.Header.Get("Stripe-Signature"))
	if err != nil {
		h.log.Warn("stripe webhook signature verification failed", "err", err)
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorBody{Error: "invalid_signature"})
		return
	}

	// (3) Dedupe + apply ATOMICALLY. ProcessEvent records the event id and applies
	// the entitlement change in the SAME transaction, so a failed apply leaves NO
	// ledger row and Stripe's retry re-applies cleanly (the lost-update fix).
	applied, err := h.processor.ProcessEvent(r.Context(), ev)
	if errors.Is(err, errIgnoredEvent) {
		// Recorded in the ledger but deliberately not applied (another
		// subscription's event); details were logged when it was skipped.
		h.log.Info("stripe event ignored (not the org's current subscription)",
			"event_id", ev.ID, "type", ev.Type, "org_id", ev.Data.OrgID)
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	if err != nil {
		// An unfulfillable event (e.g. a missing Stripe customer id with no row to
		// COALESCE from) can never succeed on retry — acknowledge with a 400 so
		// Stripe stops retrying, but write NOTHING (the tx rolled back). Everything
		// else is a transient failure: 500 so Stripe retries.
		if errors.Is(err, errUnfulfillableEvent) {
			h.log.Error("stripe event unfulfillable; acknowledging to stop retries",
				"event_id", ev.ID, "type", ev.Type, "err", err)
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorBody{Error: "unprocessable_event", Message: "event cannot be applied"})
			return
		}
		// Returning 500 asks Stripe to retry; safe because ProcessEvent is atomic —
		// no ledger row was committed, so the retry re-applies from scratch.
		h.log.Error("processing stripe event failed", "event_id", ev.ID, "type", ev.Type, "err", err)
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorBody{Error: "apply_failed"})
		return
	}
	if !applied {
		h.log.Info("duplicate stripe event ignored", "event_id", ev.ID, "type", ev.Type)
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "duplicate_ignored"})
		return
	}

	// Log the committed entitlement change at INFO so billing outcomes are visible in
	// logs (not just a generic access-log line) — the audit trail for "what tier did
	// this event apply, to which org".
	h.log.Info("stripe event applied",
		"event_id", ev.ID, "type", ev.Type,
		"org_id", ev.Data.OrgID, "plan_tier", ev.Data.PlanTier, "status", ev.Data.Status)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// errUnfulfillableEvent wraps a permanent (non-retryable) apply failure: the event
// is well-formed and signed, but can never be persisted (e.g. it carries an empty
// Stripe customer id and there is no existing row to COALESCE from, which would
// violate billing.subscriptions.stripe_customer_id NOT NULL). The handler maps it
// to a 400 acknowledgment so Stripe stops retrying an event that will fail forever
// (FIX 3). It is NOT used for transient DB errors, which must 500 and retry.
var errUnfulfillableEvent = errors.New("billing: event cannot be applied (permanent)")

// errUnknownPrice is returned when a subscription event's price maps to no
// configured tier (H6). It is RETRYABLE (the handler 500s, Stripe retries): the
// entitlement is left UNCHANGED — never downgraded to Free — so an ops
// misconfiguration (an unmapped STRIPE_PRICE_*) can be fixed and the retry applies
// correctly, instead of silently dropping a paying org to Free.
var errUnknownPrice = errors.New("billing: subscription price is not mapped to a tier (configure STRIPE_PRICE_BUSINESS/ENTERPRISE)")

// applyEvent dispatches a verified, deduped event to the persistence layer. It is
// called by the store INSIDE the dedupe+apply transaction (so dispatch + the
// entitlement write + the ledger row commit or roll back together).
//
// Documented event → plan_tier mapping ("Lifecycle webhooks → DB state"):
//
//	checkout.session.completed        → UpsertSubscription (first paid tier set)
//	customer.subscription.created     → UpsertSubscription (tier from price)
//	customer.subscription.updated     → UpsertSubscription (seat/tier/cancel change)
//	customer.subscription.deleted     → SetCanceled (downgrade to Free; data kept)
//
//	invoice.paid (subscription_cycle)  → RecordRenewal (analytics only; no entitlement change)
//
// Unhandled types (and invoice.paid for anything but a renewal) are acknowledged
// with a 200 (no-op) so Stripe stops retrying them. The id is still recorded (in
// the caller's tx) so the event isn't reprocessed.
func applyEvent(ctx context.Context, subs SubscriptionStore, log *slog.Logger, ev Event) error {
	switch ev.Type {
	case "checkout.session.completed",
		"customer.subscription.created",
		"customer.subscription.updated":
		// M6: entitlement follows the live PAYING status. A non-entitled status
		// (unpaid / incomplete_expired / paused / canceled-via-update / …) must NOT
		// grant the paid tier — downgrade to Free (read-only over-limit), exactly as
		// a subscription.deleted does, never leave it active+paid.
		cancel := !isEntitledStatus(ev.Data.Status)
		// Events from another subscription are dropped first, so a stray
		// subscription can't trigger the retryable unknown-price error below.
		if err := checkForeignSubscription(ctx, subs, log, ev, cancel); err != nil {
			return err
		}
		// H6: an unrecognized price must not silently downgrade — refuse to change
		// entitlement (retryable) so the price can be mapped and the retry applies.
		if ev.Data.UnknownPrice {
			return fmt.Errorf("%w: org %s", errUnknownPrice, ev.Data.OrgID)
		}
		if cancel {
			return subs.SetCanceled(ctx, ev.Data.OrgID)
		}
		return subs.UpsertSubscription(ctx, ev.Data)

	case "customer.subscription.deleted":
		if err := checkForeignSubscription(ctx, subs, log, ev, true); err != nil {
			return err
		}
		return subs.SetCanceled(ctx, ev.Data.OrgID)

	case "invoice.paid":
		return recordRenewal(ctx, subs, log, ev)

	default:
		// Acknowledge-and-ignore: log at debug, return nil so we 200. The id is
		// still recorded by the caller's tx so it isn't reprocessed.
		// INFO (not DEBUG) so a no-op acknowledgement is visible at the prod log level
		// — an unsubscribed/unexpected event type otherwise leaves no trace.
		log.Info("ignoring unhandled stripe event (acknowledged, no-op)", "type", ev.Type)
		return nil
	}
}

// checkForeignSubscription looks up the org's current subscription and returns
// errIgnoredEvent when ev belongs to a different one that must not touch
// entitlement (see ignoreForeignSubscription). Either case where the org has two
// live subscriptions is logged at ERROR: the customer is being double-billed and
// the extra subscription needs canceling in Stripe.
func checkForeignSubscription(ctx context.Context, subs SubscriptionStore, log *slog.Logger, ev Event, cancel bool) error {
	if ev.Data.OrgID == "" || ev.Data.StripeSubscriptionID == "" {
		return nil
	}
	cur, err := subs.CurrentSubscription(ctx, ev.Data.OrgID)
	if err != nil {
		return err
	}
	foreign := cur.ID != "" && ev.Data.StripeSubscriptionID != cur.ID
	attrs := []any{
		"event_id", ev.ID, "type", ev.Type, "org_id", ev.Data.OrgID,
		"event_subscription_id", ev.Data.StripeSubscriptionID, "event_tier", ev.Data.PlanTier,
		"event_status", ev.Data.Status,
		"current_subscription_id", cur.ID, "current_tier", cur.PlanTier, "current_status", cur.Status,
	}
	if ignoreForeignSubscription(cur, ev.Data.StripeSubscriptionID, ev.Data.PlanTier, cancel) {
		if cur.live() && !cancel {
			log.Error("billing: ignoring event from a second live subscription on a lower or equal tier (org is double-billed; cancel the event's subscription in Stripe)", attrs...)
		} else {
			log.Warn("billing: ignoring event for a subscription that is not the org's current one", attrs...)
		}
		return fmt.Errorf("%w: event %s, subscription %s", errIgnoredEvent, ev.ID, ev.Data.StripeSubscriptionID)
	}
	if foreign && cur.live() {
		log.Error("billing: switching to a second live subscription on a higher tier (org is double-billed; cancel the previous subscription in Stripe)", attrs...)
	}
	return nil
}

// billingReasonRenewal is Stripe's invoice.billing_reason for a subscription's
// recurring charge at the start of a new billing period.
const billingReasonRenewal = "subscription_cycle"

// recordRenewal turns a paid recurring invoice into a Renewal for analytics.
// Renewals are their own event: the plan doesn't change, so they are never a
// plan_upgraded / plan_downgraded. Only the org's current subscription counts; a
// stray second subscription's renewal is ignored (and is double-billing ops must
// clean up). Every other invoice (the first charge, prorations, manual invoices)
// is acknowledged without an event.
func recordRenewal(ctx context.Context, subs SubscriptionStore, log *slog.Logger, ev Event) error {
	d := ev.Data
	if d.BillingReason != billingReasonRenewal || d.StripeSubscriptionID == "" {
		return nil
	}
	orgID := d.OrgID
	if orgID == "" {
		var err error
		if orgID, err = subs.OrgForSubscription(ctx, d.StripeSubscriptionID); err != nil {
			return err
		}
		if orgID == "" {
			log.Warn("billing: renewal invoice for a subscription no org tracks; no renewal recorded",
				"event_id", ev.ID, "invoice_id", d.InvoiceID, "subscription_id", d.StripeSubscriptionID)
			return nil
		}
	}
	cur, err := subs.CurrentSubscription(ctx, orgID)
	if err != nil {
		return err
	}
	if cur.ID != d.StripeSubscriptionID {
		log.Warn("billing: ignoring renewal invoice for a subscription that is not the org's current one",
			"event_id", ev.ID, "org_id", orgID, "invoice_id", d.InvoiceID,
			"invoice_subscription_id", d.StripeSubscriptionID, "current_subscription_id", cur.ID)
		return fmt.Errorf("%w: invoice %s, subscription %s", errIgnoredEvent, d.InvoiceID, d.StripeSubscriptionID)
	}
	return subs.RecordRenewal(ctx, Renewal{
		OrgID:          orgID,
		SubscriptionID: d.StripeSubscriptionID,
		InvoiceID:      d.InvoiceID,
		PlanTier:       cur.PlanTier,
		AmountPaid:     d.AmountPaid,
		Currency:       d.Currency,
	})
}
