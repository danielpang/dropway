//go:build cloud

package quota

import "context"

// StaticCounts is a DB-free Counts used to wire the cloud build without a live
// database (and in tests). It reports a fixed plan tier and zero usage, so every
// check passes — the production deployment replaces it with a pgx-backed Counts
// that reads app.org_usage / the sites table and billing.subscriptions inside
// the caller's transaction.
//
// It exists so `go build -tags cloud ./...` links end to end; it is NOT the
// enforcement path that ships to the hosted environment.
type StaticCounts struct {
	Tier PlanTier // defaults to free via zero value handling below
}

func (s StaticCounts) tier() PlanTier {
	if s.Tier == "" {
		return TierFree
	}
	return s.Tier
}

func (s StaticCounts) PlanTier(context.Context, string) (PlanTier, error) {
	return s.tier(), nil
}

func (StaticCounts) MembersInOrg(context.Context, string) (int64, error) { return 0, nil }

func (StaticCounts) SitesForUser(context.Context, string, string) (int64, error) { return 0, nil }
