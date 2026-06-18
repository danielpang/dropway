package customdomains

import (
	"context"
	"testing"
)

func TestFake_StateMachine(t *testing.T) {
	ctx := context.Background()
	f := NewFake()

	created, err := f.CreateCustomHostname(ctx, "docs.acme.com")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.DCV.Name == "" || created.DCV.Value == "" {
		t.Fatalf("create result = %+v", created)
	}

	// Initially pending.
	st, err := f.Status(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StatePending || st.TLSIssued {
		t.Fatalf("initial state = %+v", st)
	}

	// Advance to verifying → no TLS yet.
	if err := f.AdvanceTo(created.ID, StateVerifying); err != nil {
		t.Fatal(err)
	}
	st, _ = f.Status(ctx, created.ID)
	if st.State != StateVerifying || st.TLSIssued {
		t.Fatalf("verifying state = %+v", st)
	}

	// Advance to active → TLS issued, DCV cleared.
	if err := f.AdvanceTo(created.ID, StateActive); err != nil {
		t.Fatal(err)
	}
	st, _ = f.Status(ctx, created.ID)
	if st.State != StateActive || !st.TLSIssued || st.DCV.Name != "" {
		t.Fatalf("active state = %+v", st)
	}
}

func TestFake_UnknownID(t *testing.T) {
	f := NewFake()
	if _, err := f.Status(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for unknown id")
	}
}

func TestDCVRecord_String(t *testing.T) {
	d := DCVRecord{Name: "_x.acme.com", Type: "TXT", Value: "abc"}
	if d.String() != "_x.acme.com TXT abc" {
		t.Fatalf("string = %q", d.String())
	}
	if (DCVRecord{}).String() != "" {
		t.Fatal("empty DCV should stringify to empty")
	}
}
