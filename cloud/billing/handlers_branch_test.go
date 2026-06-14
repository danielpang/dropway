//go:build cloud

package billing

// handlers_branch_test.go covers the requireOwnerAdmin gate's remaining branches
// (incomplete tenant claim, live-role-check error, member-table-unavailable
// strict-deny vs opt-in JWT fallback), the store-error propagation in
// Checkout/Portal/Current, and decodeJSON's empty-body / unknown-field rejection.
// The happy paths live in handlers_test.go; these pin the error/edge behavior the
// gate is responsible for. All requests run THROUGH the Auth middleware (claims can
// only be injected there) via fakeVerifier.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/shipped/internal/auth"
	"github.com/danielpang/shipped/internal/middleware"
)

// errStore wraps a fakeCPStore and injects errors on chosen methods so the
// handlers' store-error propagation paths are exercised without a live DB.
type errStore struct {
	getErr  error
	saveErr error
	readErr error
	inner   fakeCPStore
}

func (s *errStore) GetSubscription(ctx context.Context, org string) (Subscription, bool, error) {
	if s.getErr != nil {
		return Subscription{}, false, s.getErr
	}
	return s.inner.GetSubscription(ctx, org)
}
func (s *errStore) SaveCustomerID(ctx context.Context, org, cid string) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	return s.inner.SaveCustomerID(ctx, org, cid)
}
func (s *errStore) ReadPlanTier(ctx context.Context, org string) (PlanTier, error) {
	if s.readErr != nil {
		return TierFree, s.readErr
	}
	return s.inner.ReadPlanTier(ctx, org)
}

// mountUnderAuth wires the three billing routes behind the Auth middleware with the
// given canned claims, live roles, and JWT-fallback flag, so a test can drive any
// route with an injected tenant/role through the real claims plumbing.
func mountUnderAuth(store CheckoutPortalStore, sc StripeClient, c *auth.Claims, roles RoleChecker, allowFB bool) http.Handler {
	h := NewHandlers(store, sc, NewPriceMap("price_biz", "price_ent"), "https://app.shipped.app", roles, allowFB, nil)
	mux := http.NewServeMux()
	a := middleware.Auth(fakeVerifier{claims: c})
	mux.Handle("POST /v1/billing/checkout", a(http.HandlerFunc(h.Checkout)))
	mux.Handle("POST /v1/billing/portal", a(http.HandlerFunc(h.Portal)))
	mux.Handle("GET /v1/billing", a(http.HandlerFunc(h.Current)))
	return mux
}

// An incomplete tenant claim (empty org id) is 401, and no Stripe call is made.
func TestRequireOwnerAdmin_EmptyOrgClaim_401(t *testing.T) {
	sc := &fakeStripe{}
	c := &auth.Claims{OrgID: "", Role: "owner"}
	c.Subject = "user_1"
	h := mountUnderAuth(&fakeCPStore{}, sc, c, fakeRoles{role: "owner"}, false)

	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("empty org claim must be 401, got %d", rr.Code)
	}
	if sc.checkoutParams.PriceID != "" {
		t.Error("no checkout for an incomplete tenant")
	}
}

// A claim with an org but no user id (subject) is also an incomplete tenant → 401.
func TestRequireOwnerAdmin_EmptyUserClaim_401(t *testing.T) {
	c := &auth.Claims{OrgID: "org_1", Role: "owner"} // Subject left empty → UserID()==""
	h := mountUnderAuth(&fakeCPStore{}, &fakeStripe{}, c, fakeRoles{role: "owner"}, false)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("empty user id claim must be 401, got %d", rr.Code)
	}
}

// A live role-check error (the member table read failed transiently) is a 500 — we
// neither authorize nor silently deny.
func TestRequireOwnerAdmin_RoleCheckError_500(t *testing.T) {
	h := mountUnderAuth(&fakeCPStore{}, &fakeStripe{}, claimsWith("org_1", "owner"), fakeRoles{err: errors.New("db down")}, false)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("a role-check error must be 500, got %d", rr.Code)
	}
}

// Member table unavailable + fallback DISABLED (strict default) → deny with 403 even
// though the JWT claim says owner (the confused-deputy guard).
func TestRequireOwnerAdmin_Unavailable_StrictDeny_403(t *testing.T) {
	h := mountUnderAuth(&fakeCPStore{}, &fakeStripe{}, claimsWith("org_1", "owner"), fakeRoles{unavailable: true}, false)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("unavailable member table with fallback off must be 403, got %d", rr.Code)
	}
}

// Member table unavailable + fallback ENABLED + JWT claim is owner/admin → trust the
// verified claim (degraded) and proceed (200).
func TestRequireOwnerAdmin_Unavailable_FallbackAllows_200(t *testing.T) {
	sc := &fakeStripe{}
	h := mountUnderAuth(&fakeCPStore{}, sc, claimsWith("org_1", "admin"), fakeRoles{unavailable: true}, true)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("fallback-enabled admin claim must proceed (200), got %d body=%s", rr.Code, rr.Body.String())
	}
	if sc.checkoutParams.PriceID != "price_biz" {
		t.Error("checkout should have been created under the fallback authorization")
	}
}

// Member table unavailable + fallback ENABLED but the JWT claim is a plain member →
// still 403 (fallback trusts the claim, and the claim is not owner/admin).
func TestRequireOwnerAdmin_Unavailable_FallbackButMember_403(t *testing.T) {
	h := mountUnderAuth(&fakeCPStore{}, &fakeStripe{}, claimsWith("org_1", "member"), fakeRoles{unavailable: true}, true)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("fallback with a member claim must still be 403, got %d", rr.Code)
	}
}

// Checkout propagates a GetSubscription store error as a 500 (before any Stripe
// call).
func TestCheckout_GetSubscriptionError_500(t *testing.T) {
	sc := &fakeStripe{}
	store := &errStore{getErr: errors.New("get failed")}
	h := mountUnderAuth(store, sc, claimsWith("org_1", "owner"), fakeRoles{role: "owner"}, false)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("a GetSubscription error must be 500, got %d", rr.Code)
	}
	if sc.ensuredCustomer != "" {
		t.Error("must not call Stripe when the customer lookup failed")
	}
}

// Checkout propagates a SaveCustomerID store error (after EnsureCustomer, before
// session creation) as a 500.
func TestCheckout_SaveCustomerIDError_500(t *testing.T) {
	sc := &fakeStripe{}
	store := &errStore{saveErr: errors.New("save failed")} // hasSub=false → first-time customer
	h := mountUnderAuth(store, sc, claimsWith("org_1", "owner"), fakeRoles{role: "owner"}, false)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("a SaveCustomerID error must be 500, got %d", rr.Code)
	}
	if sc.checkoutParams.PriceID != "" {
		t.Error("must not create a checkout session when persisting the customer id failed")
	}
}

// A malformed checkout body (bad JSON) is a 400 before any Stripe work.
func TestCheckout_BadJSON_400(t *testing.T) {
	h := mountUnderAuth(&fakeCPStore{}, &fakeStripe{}, claimsWith("org_1", "owner"), fakeRoles{role: "owner"}, false)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{not json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed checkout body must be 400, got %d", rr.Code)
	}
}

// An unknown field in the checkout body is rejected (decodeJSON DisallowUnknownFields).
func TestCheckout_UnknownField_400(t *testing.T) {
	h := mountUnderAuth(&fakeCPStore{}, &fakeStripe{}, claimsWith("org_1", "owner"), fakeRoles{role: "owner"}, false)
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business","bogus":1}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field must be 400, got %d", rr.Code)
	}
}

// Portal propagates a GetSubscription store error as a 500.
func TestPortal_GetSubscriptionError_500(t *testing.T) {
	store := &errStore{getErr: errors.New("get failed")}
	h := mountUnderAuth(store, &fakeStripe{}, claimsWith("org_1", "owner"), fakeRoles{role: "owner"}, false)
	rr := doReq(h, http.MethodPost, "/v1/billing/portal", `{}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("a GetSubscription error in Portal must be 500, got %d", rr.Code)
	}
}

// Current with an incomplete tenant (empty org claim) is 401.
func TestCurrent_MissingTenant_401(t *testing.T) {
	c := &auth.Claims{OrgID: ""}
	c.Subject = "user_1"
	h := mountUnderAuth(&fakeCPStore{}, &fakeStripe{}, c, fakeRoles{}, false)
	rr := doReq(h, http.MethodGet, "/v1/billing", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Current with an empty org claim must be 401, got %d", rr.Code)
	}
}

// Current propagates a ReadPlanTier store error as a 500.
func TestCurrent_ReadPlanTierError_500(t *testing.T) {
	store := &errStore{readErr: errors.New("read failed")}
	h := mountUnderAuth(store, &fakeStripe{}, claimsWith("org_1", "member"), fakeRoles{}, false)
	rr := doReq(h, http.MethodGet, "/v1/billing", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("a ReadPlanTier error must be 500, got %d", rr.Code)
	}
}

// Current returns the authoritative plan (200) even with no subscription mirror row:
// org_meta drives plan_tier, the sub row only enriches status/seats.
func TestCurrent_NoSubscriptionRow_StillReturnsPlan(t *testing.T) {
	store := &fakeCPStore{tier: TierBusiness, hasSub: false}
	h := mountUnderAuth(store, &fakeStripe{}, claimsWith("org_1", "member"), fakeRoles{}, false)
	rr := doReq(h, http.MethodGet, "/v1/billing", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"plan_tier":"business"`) {
		t.Errorf("body should carry the authoritative plan_tier, got %s", rr.Body.String())
	}
}

// decodeJSON rejects an empty body with a clear error (an empty checkout POST).
func TestDecodeJSON_EmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(""))
	var v checkoutRequest
	if err := decodeJSON(req, &v); err == nil {
		t.Error("an empty body must produce an error")
	}
}
