package analytics

import (
	"context"
	"testing"
)

type recordingEmitter struct{ events []Event }

func (r *recordingEmitter) Capture(_ context.Context, ev Event) { r.events = append(r.events, ev) }
func (r *recordingEmitter) Close() error                        { return nil }

// auth_rejected carries org_id when the org was resolved before the rejection
// (an authz failure), and leaves it out when no org is known.
func TestCaptureAuthRejected_OrgID(t *testing.T) {
	rec := &recordingEmitter{}
	CaptureAuthRejected(context.Background(), rec, AuthRejection{
		Surface: "mcp", Kind: "authz", Step: "mcp_disabled", OrgID: "org_1",
	})
	CaptureAuthRejected(context.Background(), rec, AuthRejection{
		Surface: "api", Kind: "authn", Step: "jwt_verify",
	})
	if len(rec.events) != 2 {
		t.Fatalf("captured %d events, want 2", len(rec.events))
	}
	if got := rec.events[0].Properties["org_id"]; got != "org_1" {
		t.Errorf("org_id = %v, want org_1", got)
	}
	if _, set := rec.events[1].Properties["org_id"]; set {
		t.Error("org_id must be left out when no org is known")
	}
	for _, ev := range rec.events {
		if _, set := ev.Properties["organization"]; set {
			t.Error("organization property must not be sent")
		}
	}
}

// A nil emitter is a no-op.
func TestCaptureAuthRejected_NilEmitter(t *testing.T) {
	CaptureAuthRejected(context.Background(), nil, AuthRejection{OrgID: "org_1"})
}
