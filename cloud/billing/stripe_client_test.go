//go:build cloud

package billing

// stripe_client_test.go covers the network-FREE parts of the real StripeClient: the
// NewStripeClient constructor (which only wires per-resource clients to the backend,
// no API call) and EnsureCustomer's existing-id short-circuit (which returns the id
// WITHOUT touching Stripe). The methods that actually call Stripe (customer/checkout/
// portal creation) are exercised manually / by the integration suite, never here —
// we must not hit the real Stripe API in a unit test.

import "testing"

// NewStripeClient returns a usable StripeClient even from a blank key (the wiring
// guards key presence elsewhere); construction performs no network I/O.
func TestNewStripeClient_Constructs(t *testing.T) {
	if got := NewStripeClient(""); got == nil {
		t.Fatal("NewStripeClient(\"\") returned nil")
	}
	c := NewStripeClient("sk_test_dummy")
	if c == nil {
		t.Fatal("NewStripeClient returned nil")
	}
	if _, ok := c.(*realStripeClient); !ok {
		t.Errorf("NewStripeClient returned %T, want *realStripeClient", c)
	}
}

// EnsureCustomer returns an already-known customer id verbatim without creating a
// new one — the only branch of the real client that makes no Stripe call. (A blank
// existing id would call customers.New, which we must not do in a unit test.)
func TestEnsureCustomer_ExistingID_NoStripeCall(t *testing.T) {
	c := NewStripeClient("sk_test_dummy")
	got, err := c.EnsureCustomer("cus_existing", "org_1", "owner@example.com")
	if err != nil {
		t.Fatalf("EnsureCustomer with an existing id must not error: %v", err)
	}
	if got != "cus_existing" {
		t.Errorf("EnsureCustomer returned %q, want the existing cus_existing", got)
	}
}
