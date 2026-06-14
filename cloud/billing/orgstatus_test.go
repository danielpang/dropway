//go:build cloud

package billing

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

// fakeOrgStatusWriter records the org_status values projected to the edge so the
// tests can assert that a billing org_status change is pushed to KV. It can also
// inject a failure to prove the projection is BEST-EFFORT (logged, not fatal).
type fakeOrgStatusWriter struct {
	calls []struct{ org, status string }
	fail  error
}

func (f *fakeOrgStatusWriter) SetOrgStatus(_ context.Context, orgID, status string) error {
	f.calls = append(f.calls, struct{ org, status string }{orgID, status})
	return f.fail
}

// TestProjectOrgStatus_Over_LimitAndActive asserts the billing store projects the
// edge org_status flag for the two real transitions (FIX 2): a cancel that pushes an
// org over the Free caps projects "over_limit" (edge blocks, read-only), and a
// healthy active subscription projects "active" (edge clears). This is the fast KV
// flag that makes a DB-side suspension actually block at the serving Worker.
func TestProjectOrgStatus_Over_LimitAndActive(t *testing.T) {
	w := &fakeOrgStatusWriter{}
	s := (&BillingStore{}).WithOrgStatusWriter(w)

	// subscription.deleted over the caps → over_limit (block at the edge).
	s.projectOrgStatus(context.Background(), "org_over", "over_limit")
	// healthy active subscription → active (clear the edge block).
	s.projectOrgStatus(context.Background(), "org_ok", "active")

	if len(w.calls) != 2 {
		t.Fatalf("got %d org_status projections, want 2: %+v", len(w.calls), w.calls)
	}
	if w.calls[0].org != "org_over" || w.calls[0].status != "over_limit" {
		t.Errorf("first projection = %+v, want {org_over over_limit}", w.calls[0])
	}
	if w.calls[1].org != "org_ok" || w.calls[1].status != "active" {
		t.Errorf("second projection = %+v, want {org_ok active}", w.calls[1])
	}
}

// TestProjectOrgStatus_BestEffort asserts a KV projection failure does NOT propagate
// (the DB is the source of truth; the projection is rebuildable, so the webhook must
// not 500 on a KV hiccup), and that nothing is projected when no writer is wired or
// the inputs are empty.
func TestProjectOrgStatus_BestEffort(t *testing.T) {
	// A failing writer must be swallowed (logged, not returned).
	w := &fakeOrgStatusWriter{fail: errors.New("kv down")}
	s := (&BillingStore{}).WithOrgStatusWriter(w)
	s.projectOrgStatus(context.Background(), "org_x", "over_limit") // must not panic / propagate
	if len(w.calls) != 1 {
		t.Fatalf("expected the projection to be attempted once, got %d", len(w.calls))
	}

	// No writer wired → no-op (the DB write still landed elsewhere).
	(&BillingStore{}).projectOrgStatus(context.Background(), "org_x", "over_limit")

	// Empty org/status → no projection (an unhandled event records nothing).
	w2 := &fakeOrgStatusWriter{}
	s2 := (&BillingStore{}).WithOrgStatusWriter(w2)
	s2.projectOrgStatus(context.Background(), "", "over_limit")
	s2.projectOrgStatus(context.Background(), "org_x", "")
	if len(w2.calls) != 0 {
		t.Fatalf("empty org or status must project nothing, got %+v", w2.calls)
	}
}

// TestTxSubsStore_CapturesStatusFromApply asserts the apply adapter records the
// org id + resulting org_status that ProcessEvent later projects to the edge. It
// drives applyEvent (the pure event→method dispatch) through a recorder that mirrors
// txSubsStore's capture, proving subscription.deleted → over_limit/active and an
// active subscription → active flow through to the projection input.
func TestTxSubsStore_CapturesStatusFromApply(t *testing.T) {
	cases := []struct {
		name       string
		ev         Event
		applied    string // status the (faked) DB apply resolves to
		wantOrg    string
		wantStatus string
	}{
		{
			name:       "subscription.deleted over caps → over_limit",
			ev:         Event{Type: "customer.subscription.deleted", Data: EventData{OrgID: "org_del"}},
			applied:    "over_limit",
			wantOrg:    "org_del",
			wantStatus: "over_limit",
		},
		{
			name:       "subscription.deleted under caps → active",
			ev:         Event{Type: "customer.subscription.deleted", Data: EventData{OrgID: "org_del2"}},
			applied:    "active",
			wantOrg:    "org_del2",
			wantStatus: "active",
		},
		{
			// fromCheckoutSession always stamps Status="active" (an entitled status),
			// so the event upserts (and projects "active"); mirror that here.
			name:       "active checkout → active",
			ev:         Event{Type: "checkout.session.completed", Data: EventData{OrgID: "org_pay", PlanTier: TierBusiness, Status: "active"}},
			wantOrg:    "org_pay",
			wantStatus: "active",
		},
		{
			// M6: a subscription.updated that has gone non-paying (e.g. unpaid) must
			// NOT keep the paid tier — it routes to the Free downgrade, projecting the
			// computed org_status (here: under caps → active), never staying active+paid.
			name:       "non-paying subscription.updated → downgrade",
			ev:         Event{Type: "customer.subscription.updated", Data: EventData{OrgID: "org_unpaid", PlanTier: TierBusiness, Status: "unpaid"}},
			applied:    "active",
			wantOrg:    "org_unpaid",
			wantStatus: "active",
		},
		{
			name:       "unhandled event → nothing projected",
			ev:         Event{Type: "invoice.created", Data: EventData{OrgID: "org_misc"}},
			wantOrg:    "",
			wantStatus: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &statusRecorder{canceledStatus: tc.applied}
			if err := applyEvent(context.Background(), rec, slog.Default(), tc.ev); err != nil {
				t.Fatalf("applyEvent: %v", err)
			}
			if rec.org != tc.wantOrg || rec.status != tc.wantStatus {
				t.Errorf("captured (org=%q status=%q), want (org=%q status=%q)",
					rec.org, rec.status, tc.wantOrg, tc.wantStatus)
			}
		})
	}
}

// H6: applyEvent must REFUSE (a retryable error) an event whose price didn't map
// to a tier — never silently downgrade. Neither UpsertSubscription nor SetCanceled
// is called, so the existing entitlement is untouched.
func TestApplyEvent_UnknownPrice_RefusesWithoutTouchingEntitlement(t *testing.T) {
	rec := &statusRecorder{}
	err := applyEvent(context.Background(), rec, slog.Default(), Event{
		Type: "customer.subscription.updated",
		Data: EventData{OrgID: "org_x", PlanTier: TierBusiness, Status: "active", UnknownPrice: true},
	})
	if err == nil {
		t.Fatal("applyEvent should error on an unknown price (H6)")
	}
	if rec.org != "" || rec.status != "" {
		t.Errorf("nothing should be applied; captured org=%q status=%q", rec.org, rec.status)
	}
}

// statusRecorder mirrors txSubsStore's status-capture contract WITHOUT a live tx, so
// the event→status mapping is unit-testable: UpsertSubscription captures "active";
// SetCanceled captures the (injected) computed status, exactly as the real adapter
// does after setCanceledTx.
type statusRecorder struct {
	canceledStatus string // status setCanceledTx would compute for this org
	org, status    string
}

func (r *statusRecorder) UpsertSubscription(_ context.Context, d EventData) error {
	r.org, r.status = d.OrgID, "active"
	return nil
}

func (r *statusRecorder) SetCanceled(_ context.Context, orgID string) error {
	r.org, r.status = orgID, r.canceledStatus
	return nil
}
