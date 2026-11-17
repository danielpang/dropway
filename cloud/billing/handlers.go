//go:build cloud

package billing

// handlers.go is the PROPRIETARY, cloud-only HTTP surface for the dashboard's
// billing flows: start a Stripe Checkout (first paid tier / upgrade), open the
// Billing Portal (self-serve seat/plan/payment-method/cancel), and read the
// current plan. These run BEHIND the verified Better Auth JWT (the authz boundary)
// and require OWNER/ADMIN — the success redirect grants nothing; only the
// signed webhook (billing.go) mutates entitlement.
//
// They live in cloud/ and may import internal/middleware + internal/httpx.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/middleware"
)

// CheckoutPortalStore is the persistence the handlers need: read the org's
// existing Stripe customer (if any) and record a newly created one. *BillingStore
// satisfies it.
type CheckoutPortalStore interface {
	GetSubscription(ctx context.Context, orgID string) (Subscription, bool, error)
	SaveCustomerID(ctx context.Context, orgID, customerID string) error
	ReadPlanTier(ctx context.Context, orgID string) (PlanTier, error)
	// WithCheckoutLock runs fn while holding a per-org lock, so two Checkout
	// requests for the same org (two tabs, a double click) run one after the other.
	// fn must do its reads and writes through the CheckoutStore it is given, which
	// shares the lock's database connection.
	WithCheckoutLock(ctx context.Context, orgID string, fn func(CheckoutStore) error) error
}

// CheckoutStore is what the Checkout path reads and writes while holding the
// per-org checkout lock.
type CheckoutStore interface {
	GetSubscription(ctx context.Context, orgID string) (Subscription, bool, error)
	SaveCustomerID(ctx context.Context, orgID, customerID string) error
}

var _ CheckoutPortalStore = (*BillingStore)(nil)

// RoleChecker re-reads the caller's CURRENT org role from the live Better Auth
// member table (NOT the JWT claim). Billing gates through this so a 5–15-minute
// JWT can't carry a stale admin role after a demotion.
//
// It is deliberately store-free (Go's internal-package rule forbids cloud/ from
// importing services/api/internal/store). The adapter that bridges to
// *store.Store lives in package main (services/api/cmd/api/wire_cloud.go), which
// IS allowed to import the internal store. `unavailable` reports that the member
// table couldn't be consulted (Better Auth not migrated) so the caller can apply
// the fallback policy; a missing membership returns role="" (treated as
// non-admin) with err=nil.
type RoleChecker interface {
	LiveRole(ctx context.Context, orgID, userID string) (role string, unavailable bool, err error)
}

// isOwnerAdmin reports whether role is admin-or-above (owner counts).
func isOwnerAdmin(role string) bool { return role == "owner" || role == "admin" }

// Handlers serves the authed /v1/billing/* routes.
type Handlers struct {
	store          CheckoutPortalStore
	stripe         StripeClient
	prices         PriceMap
	dashboardURL   string // e.g. https://app.dropway.dev
	roles          RoleChecker
	allowJWTRoleFB bool // ALLOW_JWT_ROLE_FALLBACK: trust the JWT role only when identity.member is unavailable
	log            *slog.Logger
}

// NewHandlers builds the billing HTTP handlers. dashboardURL is the dashboard
// origin used for Checkout success/cancel + Portal return URLs. roles is the live
// identity.member re-check; allowJWTRoleFB mirrors the core API's strict-by-default
// fallback policy.
func NewHandlers(s CheckoutPortalStore, sc StripeClient, prices PriceMap, dashboardURL string, roles RoleChecker, allowJWTRoleFB bool, log *slog.Logger) *Handlers {
	if log == nil {
		log = slog.Default()
	}
	return &Handlers{store: s, stripe: sc, prices: prices, dashboardURL: dashboardURL, roles: roles, allowJWTRoleFB: allowJWTRoleFB, log: log}
}

// requireOwnerAdmin enforces OWNER/ADMIN for an org-management (billing) action.
// It does NOT trust the JWT role claim: it re-reads the CURRENT role from the live
// Better Auth member table (the confused-deputy guard), exactly like the
// core API's requireAdmin. Strict by default: if membership can't be confirmed live
// it DENIES, unless ALLOW_JWT_ROLE_FALLBACK is on (then it trusts the verified claim
// with a logged degradation). Returns the org id + caller email on success.
func (h *Handlers) requireOwnerAdmin(w http.ResponseWriter, r *http.Request) (orgID, email string, ok bool) {
	claims, found := middleware.ClaimsFromContext(r.Context())
	if !found || claims.OrgID == "" || claims.UserID() == "" {
		httpx.WriteError(w, fmt.Errorf("%w: missing tenant", httpx.ErrUnauthorized))
		return "", "", false
	}

	role, unavailable, err := h.roles.LiveRole(r.Context(), claims.OrgID, claims.UserID())
	if err != nil {
		httpx.WriteError(w, fmt.Errorf("billing: role check: %w", err))
		return "", "", false
	}
	if unavailable {
		// Better Auth member table couldn't be consulted. Strict by default: deny.
		if !h.allowJWTRoleFB {
			h.log.Warn("member table unavailable and JWT role fallback disabled; denying billing action",
				"org_id", claims.OrgID, "user_id", claims.UserID())
			httpx.WriteError(w, fmt.Errorf("%w: billing requires owner or admin (membership could not be verified)", httpx.ErrForbidden))
			return "", "", false
		}
		// Opt-in fallback: trust the verified JWT role claim, logging the degradation.
		if isOwnerAdmin(claims.Role) {
			h.log.Warn("member table unavailable; authorizing billing from JWT claim (fallback enabled)",
				"org_id", claims.OrgID, "user_id", claims.UserID(), "role", claims.Role)
			return claims.OrgID, claims.Email, true
		}
		httpx.WriteError(w, fmt.Errorf("%w: billing requires owner or admin", httpx.ErrForbidden))
		return "", "", false
	}
	// Live role known (role=="" means no membership). Enforce owner/admin.
	if !isOwnerAdmin(role) {
		httpx.WriteError(w, fmt.Errorf("%w: billing requires owner or admin", httpx.ErrForbidden))
		return "", "", false
	}
	return claims.OrgID, claims.Email, true
}

// checkoutRequest is the POST /v1/billing/checkout body.
type checkoutRequest struct {
	TargetTier string `json:"target_tier"` // "pro" | "business" | "enterprise"
	Seats      int64  `json:"seats,omitempty"`
	// LocalCurrency opts into Adaptive Pricing (local-currency presentment). Default
	// false → billed in USD, the customer's bank handles FX.
	LocalCurrency bool `json:"local_currency,omitempty"`
}

// Checkout starts a Stripe Checkout Session for the org's target tier and returns
// {checkout_url}. It ensures a Stripe Customer exists for the org (persisting the
// id), then creates a subscription-mode session with client_reference_id=org_id +
// metadata{org_id,target_tier} so the signed webhook can resolve + entitle the org.
func (h *Handlers) Checkout(w http.ResponseWriter, r *http.Request) {
	orgID, email, ok := h.requireOwnerAdmin(w, r)
	if !ok {
		return
	}

	var req checkoutRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	target := PlanTier(req.TargetTier)
	priceID, hasPrice := h.prices.PriceFor(target)
	if !hasPrice {
		httpx.WriteError(w, fmt.Errorf("%w: unknown or unconfigured target_tier %q", httpx.ErrBadRequest, req.TargetTier))
		return
	}
	seats := req.Seats
	if seats < 1 {
		seats = 1
	}

	ctx := r.Context()

	// Every step below runs under a per-org lock: two Checkout requests for the
	// same org (two tabs, a double click) would otherwise both see "no
	// subscription yet" and both hand out a payable Checkout link.
	var url string
	err := h.store.WithCheckoutLock(ctx, orgID, func(cs CheckoutStore) error {
		var err error
		url, err = h.startCheckout(ctx, cs, orgID, email, CheckoutParams{
			PriceID:           priceID,
			Quantity:          seats,
			ClientReferenceID: orgID,
			Metadata:          map[string]string{"org_id": orgID, "target_tier": string(target)},
			SuccessURL:        h.dashboardURL + "/billing?status=processing",
			CancelURL:         h.dashboardURL + "/billing?status=canceled",
			LocalCurrency:     req.LocalCurrency,
		})
		return err
	})
	if errors.Is(err, errSubscriptionExists) {
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorBody{
			Error:   "subscription_exists",
			Message: "This organization already has an active subscription. Change plans from Manage billing. If you just cancelled it, try again in a minute.",
		})
		return
	}
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"checkout_url": url})
}

// errSubscriptionExists means the org already pays: plan changes go through the
// Billing Portal on its existing subscription, never a second Checkout.
var errSubscriptionExists = errors.New("billing: org already has a live subscription")

// startCheckout resolves the org's Stripe customer and creates a Checkout session,
// refusing when the org already has a live subscription. One subscription per org:
// a second Checkout would create a second subscription that keeps billing (and
// whose renewals used to flip the org's tier back and forth). The caller holds the
// per-org checkout lock.
func (h *Handlers) startCheckout(ctx context.Context, cs CheckoutStore, orgID, email string, p CheckoutParams) (string, error) {
	var existingCustomer string
	sub, found, err := cs.GetSubscription(ctx, orgID)
	if err != nil {
		return "", err
	}
	if found {
		// A row still marked live after a cancellation whose webhook hasn't landed
		// yet also refuses here. That is deliberate: only signed webhooks change
		// plan_tier, and the 409 clears once Stripe delivers the cancellation.
		if sub.current().live() {
			return "", errSubscriptionExists
		}
		existingCustomer = sub.StripeCustomerID
	}

	customerID, err := h.stripe.EnsureCustomer(existingCustomer, orgID, email)
	if err != nil {
		h.log.Error("ensure stripe customer failed", "org_id", orgID, "err", err)
		return "", err
	}
	if existingCustomer == "" {
		// Persist the new customer id BEFORE Checkout so a later webhook can match it
		// even if the browser never returns. A brand-new customer has no
		// subscriptions or sessions yet, so the Stripe checks below are skipped.
		if err := cs.SaveCustomerID(ctx, orgID, customerID); err != nil {
			return "", err
		}
	} else {
		// Our row only changes when a webhook lands. A Checkout paid moments ago
		// already has a live subscription in Stripe, so ask Stripe too.
		live, err := h.stripe.HasLiveSubscription(customerID)
		if err != nil {
			h.log.Error("list stripe subscriptions failed", "org_id", orgID, "err", err)
			return "", err
		}
		if live {
			return "", errSubscriptionExists
		}
		// Only one Checkout link may be payable at a time: expire any the org
		// opened earlier (another tab, a double click) before creating a new one.
		n, err := h.stripe.ExpireOpenCheckoutSessions(customerID)
		if err != nil {
			h.log.Error("expire open checkout sessions failed", "org_id", orgID, "err", err)
			return "", err
		}
		if n > 0 {
			h.log.Info("expired earlier open checkout sessions", "org_id", orgID, "count", n)
		}
	}

	p.CustomerID = customerID
	url, err := h.stripe.CreateCheckoutSession(p)
	if err != nil {
		h.log.Error("create checkout session failed", "org_id", orgID, "err", err)
		return "", err
	}
	return url, nil
}

// Portal opens a Stripe Billing Portal session for the org's customer and returns
// {portal_url}. Owner/admin only. A 409 is returned if the org has no Stripe
// customer yet (it must run Checkout first).
func (h *Handlers) Portal(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := h.requireOwnerAdmin(w, r)
	if !ok {
		return
	}
	// The body is optional: {"target_tier": "business"} asks for the plan-change
	// screen when the org already pays on a lower tier.
	var req portalRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, errEmptyBody) {
		httpx.WriteError(w, fmt.Errorf("%w: %s", httpx.ErrBadRequest, err))
		return
	}
	target := PlanTier(req.TargetTier)
	if target != "" {
		if _, ok := h.prices.PriceFor(target); !ok {
			httpx.WriteError(w, fmt.Errorf("%w: unknown or unconfigured target_tier %q", httpx.ErrBadRequest, req.TargetTier))
			return
		}
	}
	ctx := r.Context()

	sub, found, err := h.store.GetSubscription(ctx, orgID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if !found || sub.StripeCustomerID == "" {
		httpx.WriteError(w, fmt.Errorf("%w: no billing customer for this org yet", httpx.ErrConflict))
		return
	}

	returnURL := h.dashboardURL + "/billing"
	if url, ok := h.planChangePortal(orgID, sub, target, returnURL); ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"portal_url": url})
		return
	}
	url, err := h.stripe.CreatePortalSession(sub.StripeCustomerID, returnURL)
	if err != nil {
		h.log.Error("create portal session failed", "org_id", orgID, "err", err)
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"portal_url": url})
}

// portalRequest is the optional POST /v1/billing/portal body.
type portalRequest struct {
	// TargetTier, when set to a tier above the org's live subscription, opens the
	// portal on the confirm screen for that upgrade instead of its home page.
	TargetTier string `json:"target_tier,omitempty"`
}

// planChangePortal opens the portal on the confirm screen for upgrading the org's
// live subscription to target. ok=false means "use the portal home page instead":
// no target, not an upgrade, no live subscription, or Stripe refused the deep link
// (e.g. the target price isn't offered in the portal's configuration).
func (h *Handlers) planChangePortal(orgID string, sub Subscription, target PlanTier, returnURL string) (string, bool) {
	if target == "" || !sub.current().live() || tierRank(target) <= tierRank(sub.PlanTier) {
		return "", false
	}
	priceID, ok := h.prices.PriceFor(target)
	if !ok {
		return "", false
	}
	url, err := h.stripe.CreatePlanChangePortalSession(sub.StripeCustomerID, sub.StripeSubscriptionID, priceID, returnURL, h.isPlanPrice)
	if err != nil {
		h.log.Warn("plan-change portal link failed; opening the portal home page instead",
			"org_id", orgID, "target_tier", target, "err", err)
		return "", false
	}
	return url, true
}

// isPlanPrice reports whether priceID is one of the configured plan prices.
func (h *Handlers) isPlanPrice(priceID string) bool {
	if priceID == "" {
		return false
	}
	_, known := h.prices.TierForChecked(priceID)
	return known
}

// currentPlanResponse is the GET /v1/billing body.
type currentPlanResponse struct {
	PlanTier  PlanTier `json:"plan_tier"`
	Status    string   `json:"status,omitempty"`
	OrgStatus string   `json:"org_status,omitempty"`
	Seats     int64    `json:"seats,omitempty"`
}

// Current returns the org's authoritative plan (read from app.org_meta) plus the
// subscription status/org_status if a row exists. Any authenticated member may
// read their org's plan (it drives the UI banner/CTA); writes still need
// owner/admin.
func (h *Handlers) Current(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFromContext(r.Context())
	if !ok || claims.OrgID == "" {
		httpx.WriteError(w, fmt.Errorf("%w: missing tenant", httpx.ErrUnauthorized))
		return
	}
	ctx := r.Context()

	tier, err := h.store.ReadPlanTier(ctx, claims.OrgID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	resp := currentPlanResponse{PlanTier: tier}
	if sub, found, err := h.store.GetSubscription(ctx, claims.OrgID); err == nil && found {
		resp.Status = sub.Status
		resp.OrgStatus = sub.OrgStatus
		resp.Seats = sub.Seats
		// org_meta is authoritative for plan_tier, but if it disagrees with the
		// subscription mirror, prefer org_meta (the value the quota gate reads).
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// decodeJSON reads a small JSON body (capped) into v, rejecting unknown fields.
// errEmptyBody is decodeJSON's error for a request with no body.
var errEmptyBody = errors.New("empty request body")

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errEmptyBody
		}
		return err
	}
	return nil
}
