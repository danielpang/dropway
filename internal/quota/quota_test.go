package quota

import (
	"errors"
	"fmt"
	"testing"
)

// TestUnlimited_AllowsEverything proves the core/self-host provider never caps:
// any tier, any resource, any (even absurd) current count is permitted. This is
// the self-host "unlimited" guarantee (§14.3).
func TestUnlimited_AllowsEverything(t *testing.T) {
	u := Unlimited{}
	cases := []struct {
		tier    string
		res     Resource
		current int64
	}{
		{"free", ResourceSitePerUser, 0},
		{"free", ResourceSitePerUser, 10},
		{"free", ResourceSitePerUser, 1_000_000},
		{"free", ResourceMemberPerOrg, 5},
		{"free", ResourceMemberPerOrg, 9999},
		{"business", ResourceMemberPerOrg, 99},
		{"enterprise", ResourceMemberPerOrg, 1000},
		{"", "", 0}, // empty tier / unknown resource still allowed in OSS
	}
	for _, c := range cases {
		if err := u.Allow(c.tier, c.res, c.current); err != nil {
			t.Errorf("Unlimited.Allow(%q, %q, %d) = %v, want nil", c.tier, c.res, c.current, err)
		}
	}

	// AllowN (the +N / storage path) is also always permitted in OSS, for any delta.
	if err := u.AllowN("free", ResourceStorageBytesPerOrg, 1<<40, 1<<40); err != nil {
		t.Errorf("Unlimited.AllowN(storage, 1TiB, +1TiB) = %v, want nil (self-host unlimited)", err)
	}
}

// TestProvider_IsPure documents the seam contract: Provider.Allow takes only
// value inputs (plan tier, resource, count) and returns an error — no context, no
// store, no IO. A pure policy has no TOCTOU surface of its own; race-safety is the
// store's responsibility (the advisory lock around COUNT → Allow → INSERT). We
// assert it here against a tiny capping fake so the signature can't silently drift
// back to a DB-coupled shape.
func TestProvider_IsPure(t *testing.T) {
	var p Provider = capAtFive{}

	// 4 existing → 5th allowed.
	if err := p.Allow("free", ResourceMemberPerOrg, 4); err != nil {
		t.Fatalf("current=4 should be allowed: %v", err)
	}
	// 5 existing → 6th rejected with a rich, unwrappable ExceededError.
	err := p.Allow("free", ResourceMemberPerOrg, 5)
	wrapped := fmt.Errorf("creating member: %w", err) // proves errors.As unwrapping
	ex, ok := AsExceeded(wrapped)
	if !ok {
		t.Fatalf("want *ExceededError through the chain, got %v", err)
	}
	if ex.Current != 5 || ex.Max != 5 || ex.PlanTier != "free" {
		t.Errorf("exceeded payload = %+v", ex)
	}
	if !errors.Is(wrapped, err) {
		t.Error("wrapped error should still match the original")
	}
}

// capAtFive is a minimal pure policy for the purity test: cap of 5, no IO.
type capAtFive struct{}

func (c capAtFive) Allow(planTier string, res Resource, current int64) error {
	return c.AllowN(planTier, res, current, 1)
}

func (capAtFive) AllowN(planTier string, res Resource, current, n int64) error {
	const max = 5
	if current+n > max {
		return &ExceededError{Limit: res, Current: current, Max: max, PlanTier: planTier}
	}
	return nil
}
