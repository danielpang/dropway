//go:build cloud

// Package quota (cloud) is the PROPRIETARY, hosted-only quota enforcer. It is
// compiled only under the `cloud` build tag and is NOT part of the FSL/self-host
// build (docs/ARCHITECTURE.md §14, cloud/LICENSE).
//
// It implements the core quota.Provider PURE POLICY interface
// (Allow(planTier, res, current)) with the hard-cap member-count bands and
// per-user site caps from §9:
//
//	Free       : ≤ 5 members/org, ≤ 10 sites/user  → 402 {next_tier: business}
//	Business   : 6–99 members/org (cap 99)         → 402 {next_tier: enterprise}
//	Enterprise : 100–1,000 members/org (cap 1000)  → 402 {next_tier: contact_sales}
//	Contact Sales: > 1,000                          (no self-serve checkout)
//
// Race-safety lives in the STORE: it holds a per-(org,subject) advisory lock
// across COUNT → Allow → INSERT inside the request tx (internal/store). This
// package is a pure function of (planTier, resource, current) — no DB, trivially
// unit-testable, and the core never imports it (open-core boundary).
package quota

import (
	corequota "github.com/danielpang/shipped/internal/quota"
)

// PlanTier identifies the billing band an org is on. Mirrors
// billing.subscriptions.plan_tier / org_meta.plan_tier; "free" is the default.
type PlanTier string

const (
	TierFree       PlanTier = "free"
	TierBusiness   PlanTier = "business"
	TierEnterprise PlanTier = "enterprise"
	tierSales      PlanTier = "contact_sales"
)

// Hard caps per tier (§9). These are the maximum EXISTING count; creating one
// more is rejected when current >= cap.
const (
	freeMembersCap = 5
	freeSitesCap   = 10

	businessMembersCap = 99
	businessSitesCap   = 100

	enterpriseMembersCap = 1000
	enterpriseSitesCap   = 1000
)

// Per-org storage caps in BYTES (docs/pricing.md §3/§5). gib is binary (1<<30) to
// match how the byte counter + infra tooling measure; the values are tunable.
const (
	gib = int64(1) << 30

	freeStorageCap       = 5 * gib
	businessStorageCap   = 100 * gib
	enterpriseStorageCap = 500 * gib
)

// URLBuilder produces the upgrade / contact-sales URLs embedded in a 402 so the
// dashboard can deep-link the right CTA. The dashboard fills in the active org
// from the session, so these take no org id (keeping the policy pure).
type URLBuilder interface {
	UpgradeURL(target PlanTier) string
	SalesURL() string
}

// Provider is the cloud quota.Provider (pure policy). Construct with NewProvider.
type Provider struct {
	upgrade URLBuilder
}

// NewProvider builds the cloud provider. urls may be nil (no CTA URLs in the 402).
func NewProvider(urls URLBuilder) *Provider { return &Provider{upgrade: urls} }

// Ensure the cloud provider satisfies the core interface so DI is a drop-in.
var _ corequota.Provider = (*Provider)(nil)

// Allow enforces the hard cap for a discrete resource: creating ONE MORE
// (current+1) must stay within the tier cap. It is AllowN with n=1.
func (p *Provider) Allow(planTier string, res corequota.Resource, current int64) error {
	return p.AllowN(planTier, res, current, 1)
}

// AllowN enforces the hard cap for `res` given the org's live plan tier: ADDING n
// units to `current` must stay within the cap (current+n <= cap), else it returns a
// *corequota.ExceededError (→ HTTP 402). For discrete resources the store passes
// n=1; for storage, n is the deploy's new-blob byte delta. Pure: no IO, no side
// effects.
func (p *Provider) AllowN(planTier string, res corequota.Resource, current, n int64) error {
	tier := PlanTier(planTier)
	if tier == "" {
		tier = TierFree
	}

	var capMax int64
	var next PlanTier
	switch res {
	case corequota.ResourceSitePerUser:
		capMax, next = siteCap(tier)
	case corequota.ResourceMemberPerOrg:
		capMax, next = memberCap(tier)
	case corequota.ResourceStorageBytesPerOrg:
		capMax, next = storageCap(tier)
	default:
		// Unknown resources are not capped by the cloud policy (the store only
		// calls Allow for the resources it enforces).
		return nil
	}

	if current+n > capMax {
		return p.exceeded(res, current, capMax, tier, next)
	}
	return nil
}

// exceeded builds the rich 402 payload: the next tier + the matching CTA URL
// (upgrade for self-serve tiers, sales at the contact-sales boundary).
func (p *Provider) exceeded(res corequota.Resource, current, max int64, tier, next PlanTier) error {
	e := &corequota.ExceededError{
		Limit:    res,
		Current:  current,
		Max:      max,
		PlanTier: string(tier),
		NextTier: string(next),
	}
	if p.upgrade != nil {
		if next == tierSales {
			e.SalesURL = p.upgrade.SalesURL()
		} else {
			e.UpgradeURL = p.upgrade.UpgradeURL(next)
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
		return enterpriseSitesCap, tierSales
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
		return enterpriseMembersCap, tierSales
	default: // free
		return freeMembersCap, TierBusiness
	}
}

// storageCap returns the per-org storage cap (bytes) and the next tier for `tier`.
func storageCap(tier PlanTier) (max int64, next PlanTier) {
	switch tier {
	case TierBusiness:
		return businessStorageCap, TierEnterprise
	case TierEnterprise:
		return enterpriseStorageCap, tierSales
	default: // free
		return freeStorageCap, TierBusiness
	}
}
