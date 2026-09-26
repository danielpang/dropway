//go:build cloud

// plangate.go answers whether an org's live subscription tier includes a paid
// feature. It reads billing.subscriptions (no RLS) over the bare billing pool.
// app.org_meta is FORCE ROW LEVEL SECURITY, and this pool does not set
// app.current_org_id, so a SELECT from org_meta would return zero rows for
// every org and resolve everyone to Free.
package billing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// planTierQuery reads the entitlement tier from billing.subscriptions (no RLS),
// NOT app.org_meta. A regression test asserts this targets billing.subscriptions.
const planTierQuery = `SELECT plan_tier FROM billing.subscriptions WHERE org_id = $1`

// PlanGate is the paid-plan check for cloud-gated features such as org memory.
type PlanGate struct {
	pool *pgxpool.Pool
}

// NewPlanGate builds the gate over the billing pool.
func NewPlanGate(pool *pgxpool.Pool) *PlanGate {
	return &PlanGate{pool: pool}
}

// AllowMemoryForOrg reports whether the org may use org memory. Pro and above
// are allowed. Free orgs get a "plan_required" reason.
func (g *PlanGate) AllowMemoryForOrg(ctx context.Context, orgID string) (bool, string, error) {
	tier, err := g.planTier(ctx, orgID)
	if err != nil {
		return false, "", err
	}
	if !memoryPlanAllowed(tier) {
		return false, "plan_required", nil
	}
	return true, "", nil
}

// memoryPlanAllowed is the paid-plan bar for org memory: anything other than
// Free (including a missing tier) is denied.
func memoryPlanAllowed(tier PlanTier) bool {
	return tier != TierFree && tier != ""
}

func (g *PlanGate) planTier(ctx context.Context, orgID string) (PlanTier, error) {
	var tier string
	err := g.pool.QueryRow(ctx, planTierQuery, orgID).Scan(&tier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TierFree, nil
		}
		return "", err
	}
	return PlanTier(tier), nil
}
