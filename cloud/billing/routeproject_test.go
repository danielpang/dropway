//go:build cloud

package billing

import (
	"context"
	"errors"
	"testing"
)

// fakeOrgRouteProjector records the orgs whose edge routes were re-projected so the
// tests can assert a tier change triggers a reprojection. It can inject a failure to
// prove the reprojection is BEST-EFFORT (logged, not fatal).
type fakeOrgRouteProjector struct {
	calls []string
	fail  error
}

func (f *fakeOrgRouteProjector) ReprojectOrgRoutes(_ context.Context, orgID string) error {
	f.calls = append(f.calls, orgID)
	return f.fail
}

// TestReprojectOrgRoutes_OnlyOnTierChange asserts the route reprojection fires when
// (and only when) the plan tier actually moved: free→pro/business (banner clears) and
// a paid→free downgrade (banner reappears), but NOT a same-tier seat/status refresh
// (so we don't rewrite every host's KV on every billing event).
func TestReprojectOrgRoutes_OnlyOnTierChange(t *testing.T) {
	cases := []struct {
		name string
		res  applyResult
		want bool
	}{
		{"free→pro upgrade", applyResult{fromTier: TierFree, toTier: TierPro}, true},
		{"free→business upgrade", applyResult{fromTier: TierFree, toTier: TierBusiness}, true},
		{"pro→free downgrade", applyResult{fromTier: TierPro, toTier: TierFree}, true},
		{"pro→business (paid move)", applyResult{fromTier: TierPro, toTier: TierBusiness}, true},
		{"same tier (seat/status refresh)", applyResult{fromTier: TierPro, toTier: TierPro}, false},
		{"free→free (no-op apply)", applyResult{fromTier: TierFree, toTier: TierFree}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeOrgRouteProjector{}
			s := (&BillingStore{}).WithOrgRouteProjector(p)
			s.reprojectOrgRoutes(context.Background(), "org_1", tc.res)

			got := len(p.calls) == 1 && p.calls[0] == "org_1"
			if got != tc.want {
				t.Fatalf("reproject fired=%v (calls=%v), want %v", got, p.calls, tc.want)
			}
		})
	}
}

// TestReprojectOrgRoutes_BestEffortAndGuards asserts a reprojection failure does NOT
// propagate (the DB is the source of truth; the projection is rebuildable, so the
// webhook must not 500), and that nothing is projected without a writer or an org id.
func TestReprojectOrgRoutes_BestEffortAndGuards(t *testing.T) {
	changed := applyResult{fromTier: TierFree, toTier: TierPro}

	// A failing projector must be swallowed (logged, not returned/panicked).
	p := &fakeOrgRouteProjector{fail: errors.New("kv down")}
	s := (&BillingStore{}).WithOrgRouteProjector(p)
	s.reprojectOrgRoutes(context.Background(), "org_x", changed)
	if len(p.calls) != 1 {
		t.Fatalf("expected the reprojection to be attempted once, got %d", len(p.calls))
	}

	// No projector wired → no-op (the DB write still landed; next publish/rebuild heals).
	(&BillingStore{}).reprojectOrgRoutes(context.Background(), "org_x", changed)

	// Empty org → nothing projected even on a tier change.
	p2 := &fakeOrgRouteProjector{}
	s2 := (&BillingStore{}).WithOrgRouteProjector(p2)
	s2.reprojectOrgRoutes(context.Background(), "", changed)
	if len(p2.calls) != 0 {
		t.Fatalf("empty org must project nothing, got %v", p2.calls)
	}
}
