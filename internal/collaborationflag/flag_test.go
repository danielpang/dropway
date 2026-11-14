package collaborationflag

import (
	"context"
	"errors"
	"github.com/posthog/posthog-go"
	"testing"
)

type fakeClient func(posthog.FeatureFlagPayload) (interface{}, error)

func (f fakeClient) GetFeatureFlag(p posthog.FeatureFlagPayload) (interface{}, error) { return f(p) }
func TestFailClosedAndIdentity(t *testing.T) {
	if (Evaluator{}).Allowed(context.Background(), "u", "o") {
		t.Fatal("missing client enabled")
	}
	for _, value := range []interface{}{false, nil, "true", "enabled", 1, true} {
		e := Evaluator{Environment: "development", Client: fakeClient(func(p posthog.FeatureFlagPayload) (interface{}, error) {
			if p.Key != Key || p.DistinctId != "user" || p.PersonProperties["org_id"] != "org-a" || p.PersonProperties["environment"] != "development" {
				t.Fatalf("wrong identity: %+v", p)
			}
			return value, nil
		})}
		if got := e.Allowed(context.Background(), "user", "org-a"); got != (value == true) {
			t.Fatalf("value %v enabled=%v", value, got)
		}
	}
	e := Evaluator{Client: fakeClient(func(posthog.FeatureFlagPayload) (interface{}, error) { return true, errors.New("unavailable") })}
	if e.Allowed(context.Background(), "user", "org-a") {
		t.Fatal("error enabled")
	}
	e.Client = fakeClient(func(p posthog.FeatureFlagPayload) (interface{}, error) {
		return p.DistinctId == "user" && p.PersonProperties["org_id"] == "org-a", nil
	})
	if !e.Allowed(context.Background(), "user", "org-a") || e.Allowed(context.Background(), "user", "org-b") || e.Allowed(context.Background(), "other", "org-a") {
		t.Fatal("identity carried across users/orgs")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if e.Allowed(cancelled, "user", "org-a") {
		t.Fatal("cancelled request enabled")
	}
}
