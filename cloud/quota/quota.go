//go:build cloud

// Package quota (cloud) is the PROPRIETARY, hosted-only quota enforcer. It is
// compiled only under the `cloud` build tag and is NOT part of the FSL/self-host
// build (docs/ARCHITECTURE.md §14, cloud/LICENSE).
//
// It implements the core quota.Provider interface with the hard-cap member-count
// bands and per-user site caps from §9:
//
//	Free       : ≤ 5 members/org, ≤ 10 sites/user  → 402 {next_tier: business}
//	Business   : 6–99 members/org (cap 99)         → 402 {next_tier: enterprise}
//	Enterprise : 100–1,000 members/org (cap 1000)  → 402 {next_tier: contact_sales}
//	Contact Sales: > 1,000                          (no self-serve checkout)
//
// Enforcement is synchronous at the cost-creating action. Real race-safety lives
// in the persistence layer (SELECT ... FOR UPDATE on app.org_usage inside the
// caller's tx); this package depends only on an injected Counts interface so it
// compiles and unit-tests without a live database.
package quota

import (
	"context"
	"fmt"

	corequota "github.com/danielpang/shipped/internal/quota"
)

// PlanTier identifies the billing band an org is on. It mirrors
// billing.subscriptions.plan_tier; "free" is the default when no paid row exists.
type PlanTier string

const (
	TierFree       PlanTier = "free"
	TierBusiness   PlanTier = "business"
	TierEnterprise PlanTier = "enterprise"
)

// Hard caps per tier (§9). These are inclusive maxima for the EXISTING count;
// creating one more is rejected when current >= cap.
const (
	freeMembersCap = 5
	freeSitesCap   = 10

	businessMembersCap = 99
	businessSitesCap   = 100

	enterpriseMembersCap = 1000
	enterpriseSitesCap   = 1000
)

// Counts is the injected read side: live counts + the org's current plan tier.
// In the hosted deployment this is backed by app.org_usage / the sites table and
// billing.subscriptions; tests and the compile-without-DB path inject a fake.
//
// The reservation itself (incrementing the counter under FOR UPDATE) is the
// caller's transaction's job — this provider only decides allow/deny.
type Counts interface {
	// PlanTier returns the org's live plan tier (defaulting to free).
	PlanTier(ctx context.Context, orgID string) (PlanTier, error)
	// MembersInOrg returns the current member count for the org.
	MembersInOrg(ctx context.Context, orgID string) (int64, error)
	// SitesForUser returns the current site count owned by the user.
	SitesForUser(ctx context.Context, orgID, userID string) (int64, error)
}

// Reserver atomically increments the relevant counter once the cap check passes.
// It's optional: a nil Reserver means "check only" (the counter increment is
// handled by the caller's create transaction). Kept separate so the policy
// (this package) is decoupled from the storage engine.
type Reserver interface {
	Reserve(ctx context.Context, orgID, userID string, res corequota.Resource) error
}

// Provider is the cloud quota.Provider. Construct it with NewProvider.
type Provider struct {
	counts  Counts
	reserve Reserver
	upgrade URLBuilder
}

// URLBuilder produces the upgrade / contact-sales URLs embedded in a 402 so the
// dashboard can deep-link the right CTA. Injected so the hosted config (base
// URL, org id) stays out of this package.
type URLBuilder interface {
	UpgradeURL(orgID string, target PlanTier) string
	SalesURL(orgID string) string
}

// NewProvider builds the cloud provider. reserve may be nil (check-only).
func NewProvider(counts Counts, reserve Reserver, urls URLBuilder) *Provider {
	return &Provider{counts: counts, reserve: reserve, upgrade: urls}
}

// Ensure the cloud provider satisfies the core interface so DI is a drop-in.
var _ corequota.Provider = (*Provider)(nil)

// CheckAndReserve enforces the hard cap for `res`, returning a
// *corequota.ExceededError (→ HTTP 402) when creating one more would cross it.
func (p *Provider) CheckAndReserve(ctx context.Context, orgID, subjectID string, res corequota.Resource) error {
	tier, err := p.counts.PlanTier(ctx, orgID)
	if err != nil {
		return fmt.Errorf("cloud/quota: read plan tier: %w", err)
	}

	switch res {
	case corequota.ResourceSitePerUser:
		current, err := p.counts.SitesForUser(ctx, orgID, subjectID)
		if err != nil {
			return fmt.Errorf("cloud/quota: count sites: %w", err)
		}
		cap, next := siteCap(tier)
		if current >= cap {
			return p.exceeded(orgID, res, current, cap, tier, next)
		}

	case corequota.ResourceMemberPerOrg:
		current, err := p.counts.MembersInOrg(ctx, orgID)
		if err != nil {
			return fmt.Errorf("cloud/quota: count members: %w", err)
		}
		cap, next := memberCap(tier)
		if current >= cap {
			return p.exceeded(orgID, res, current, cap, tier, next)
		}

	default:
		return fmt.Errorf("cloud/quota: unknown resource %q", res)
	}

	// Within cap — reserve if a Reserver was provided.
	if p.reserve != nil {
		if err := p.reserve.Reserve(ctx, orgID, subjectID, res); err != nil {
			return fmt.Errorf("cloud/quota: reserve: %w", err)
		}
	}
	return nil
}

// exceeded builds the rich 402 payload, including the next tier and the matching
// CTA URL (upgrade for self-serve tiers, sales for the contact-sales boundary).
func (p *Provider) exceeded(orgID string, res corequota.Resource, current, max int64, tier, next PlanTier) error {
	e := &corequota.ExceededError{
		Limit:    res,
		Current:  current,
		Max:      max,
		PlanTier: string(tier),
		NextTier: string(next),
	}
	if p.upgrade != nil {
		if next == "contact_sales" {
			e.SalesURL = p.upgrade.SalesURL(orgID)
		} else {
			e.UpgradeURL = p.upgrade.UpgradeURL(orgID, next)
		}
	}
	return e
}

// siteCap returns the per-user site cap and the tier to upgrade to for `tier`.
func siteCap(tier PlanTier) (max int64, next PlanTier) {
	switch tier {
	case TierBusiness:
		return businessSitesCap, TierEnterprise
	case TierEnterprise:
		return enterpriseSitesCap, "contact_sales"
	default: // free
		return freeSitesCap, TierBusiness
	}
}

// memberCap returns the per-org member cap and the next tier for `tier`.
func memberCap(tier PlanTier) (max int64, next PlanTier) {
	switch tier {
	case TierBusiness:
		return businessMembersCap, TierEnterprise
	case TierEnterprise:
		return enterpriseMembersCap, "contact_sales"
	default: // free
		return freeMembersCap, TierBusiness
	}
}
