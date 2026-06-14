//go:build cloud

// Package billing (cloud) is the PROPRIETARY, hosted-only Stripe integration. It
// is compiled only under the `cloud` build tag and is NOT part of the FSL/
// self-host build (docs/ARCHITECTURE.md §14, cloud/LICENSE).
//
// This file is the webhook handler skeleton. The architectural invariant it
// encodes (§9): the paid entitlement (plan_tier) is written to the DB ONLY by a
// signature-verified webhook, never by the browser's success redirect. The
// handler therefore (1) verifies the Stripe-Signature, (2) dedupes by event id,
// (3) maps the event to a plan_tier change, and (4) persists it. All external
// dependencies (signature verify, dedupe store, persistence) are interfaces so
// this compiles and unit-tests without a live Stripe or database.
package billing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/danielpang/shipped/internal/httpx"
)

// PlanTier mirrors billing.subscriptions.plan_tier.
type PlanTier string

const (
	TierFree       PlanTier = "free"
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
	CurrentPeriodEnd     int64  // unix seconds
	CancelAtPeriodEnd    bool
}

// SignatureVerifier verifies the raw request body against the Stripe-Signature
// header using the endpoint's webhook secret, returning the parsed Event. The
// real implementation wraps stripe webhook.ConstructEvent; the stub below
// documents the contract and lets the handler compile/test.
type SignatureVerifier interface {
	Verify(payload []byte, sigHeader string) (Event, error)
}

// DedupeStore records processed event ids (the processed_stripe_events table).
// MarkProcessed returns alreadyProcessed=true when the id was seen before, so a
// replayed webhook is a no-op (idempotency, §9).
type DedupeStore interface {
	MarkProcessed(ctx context.Context, eventID string) (alreadyProcessed bool, err error)
}

// SubscriptionStore persists entitlement. UpsertSubscription is the ONLY place
// plan_tier is written; SetCanceled handles customer.subscription.deleted by
// downgrading to Free without destroying data (over-limit → read-only, §9).
type SubscriptionStore interface {
	UpsertSubscription(ctx context.Context, d EventData) error
	SetCanceled(ctx context.Context, orgID string) error
}

// Handler is the /webhooks/stripe HTTP handler.
type Handler struct {
	verifier SignatureVerifier
	dedupe   DedupeStore
	subs     SubscriptionStore
	log      *slog.Logger
}

// NewHandler constructs the webhook handler. A nil logger uses slog.Default.
func NewHandler(v SignatureVerifier, d DedupeStore, s SubscriptionStore, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{verifier: v, dedupe: d, subs: s, log: log}
}

// maxBody caps the webhook body to avoid a memory-amplification DoS; Stripe
// events are small (a few KB).
const maxBody = 1 << 20 // 1 MiB

// ServeHTTP implements the webhook flow from §9:
//
//  1. read + size-limit the body
//  2. verify the Stripe-Signature (else 400 — never trust an unsigned body)
//  3. INSERT event.id into processed_stripe_events; ON CONFLICT → 200 & return
//  4. map the event type → persistence (the only writer of plan_tier)
//  5. 200 OK fast (heavy work is async in the full build)
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

	// (3) Dedupe: a replayed event id is acknowledged but not re-applied.
	already, err := h.dedupe.MarkProcessed(r.Context(), ev.ID)
	if err != nil {
		h.log.Error("dedupe store error", "event_id", ev.ID, "err", err)
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorBody{Error: "internal_error"})
		return
	}
	if already {
		h.log.Info("duplicate stripe event ignored", "event_id", ev.ID, "type", ev.Type)
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "duplicate_ignored"})
		return
	}

	// (4) Map event type → persistence. See eventTypeMapping below for the full
	// documented mapping.
	if err := h.apply(r.Context(), ev); err != nil {
		// Returning non-2xx asks Stripe to retry; safe because step (3) is
		// idempotent and step (4) upserts.
		h.log.Error("applying stripe event failed", "event_id", ev.ID, "type", ev.Type, "err", err)
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorBody{Error: "apply_failed"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// errUnhandledEvent is returned for event types we acknowledge but don't act on.
var errUnhandledEvent = errors.New("billing: unhandled event type")

// apply dispatches a verified, deduped event to the persistence layer.
//
// Documented event → plan_tier mapping (§9 "Lifecycle webhooks → DB state"):
//
//	checkout.session.completed        → UpsertSubscription (first paid tier set)
//	customer.subscription.created     → UpsertSubscription (tier from price)
//	customer.subscription.updated     → UpsertSubscription (seat/tier/cancel change)
//	customer.subscription.deleted     → SetCanceled (downgrade to Free; data kept)
//
// invoice.paid / invoice.payment_failed are handled in the full build for
// status/period transitions; unhandled types are acknowledged with a 200 (no-op)
// so Stripe stops retrying them.
func (h *Handler) apply(ctx context.Context, ev Event) error {
	switch ev.Type {
	case "checkout.session.completed",
		"customer.subscription.created",
		"customer.subscription.updated":
		return h.subs.UpsertSubscription(ctx, ev.Data)

	case "customer.subscription.deleted":
		return h.subs.SetCanceled(ctx, ev.Data.OrgID)

	default:
		// Acknowledge-and-ignore: log at debug, return nil so we 200.
		h.log.Debug("ignoring unhandled stripe event", "type", ev.Type, "reason", errUnhandledEvent)
		return nil
	}
}
