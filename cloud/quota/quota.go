//go:build cloud

// Package quota (cloud) is the PROPRIETARY, hosted-only quota enforcer. It is
// compiled only under the `cloud` build tag and is NOT part of the FSL/self-host
// build (cloud/LICENSE).
//
// It implements the core quota.Provider PURE POLICY interface
// (Allow(planTier, res, current)) with the seat-free per-ORG site bands
// ("pay for sites, not seats"):
//
//	Free       : ≤ 10 sites/org   → 402 {next_tier: pro}
//	Pro        : ≤ 100 sites/org  → 402 {next_tier: business}
//	Business   : unlimited sites  (no site cap)
//	Enterprise : unlimited sites  (no site cap)
//
// SEATS ARE FREE: members are unlimited on every plan, so ResourceMemberPerOrg
// always passes. Storage (bytes/org) keeps its own bands but is GATED OFF by
// default — it's metered, not enforced, until storage billing ships (toggle with
// ENFORCE_STORAGE_QUOTA / NewProvider's enforceStorage). Business is the $150
// unlimited-sites tier between Pro and Enterprise; the internal tier keys now
// match the public labels (free / pro / business / enterprise).
//
// Race-safety lives in the STORE: it holds a per-(org,subject) advisory lock
// across COUNT → Allow → INSERT inside the request tx (internal/store). This
// package is a pure function of (planTier, resource, current) — no DB, trivially
// unit-testable, and the core never imports it (open-core boundary).
package quota

import (
	corequota "github.com/danielpang/dropway/internal/quota"
)

// PlanTier identifies the billing band an org is on. Mirrors
// billing.subscriptions.plan_tier / org_meta.plan_tier; "free" is the default.
type PlanTier string

const (
	TierFree       PlanTier = "free"
	TierPro        PlanTier = "pro"
	TierBusiness   PlanTier = "business"
	TierEnterprise PlanTier = "enterprise"
	tierSales      PlanTier = "contact_sales"
)

// Per-ORG site caps. These are the maximum EXISTING count;
// creating one more is rejected when current >= cap. Business and Enterprise are
// uncapped (handled in orgSiteCap), so they have no constant here. Seats are
// free, so there are no member caps.
const (
	freeSitesCap = 10
	proSitesCap  = 100
)

// Per-org storage caps in BYTES. gib is binary (1<<30) to
// match how the byte counter + infra tooling measure; the values are tunable.
const (
	gib = int64(1) << 30

	freeStorageCap       = 5 * gib
	proStorageCap        = 100 * gib
	businessStorageCap   = 250 * gib
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
	// enforceStorage gates the per-org STORAGE band. Default OFF (storage is metered
	// but never blocks a deploy) — the only paid lever today is the site count. When
	// false, AllowN(storage) always returns nil; the band code below is kept intact for
	// when storage billing ships (config: ENFORCE_STORAGE_QUOTA).
	enforceStorage bool
}

// NewProvider builds the cloud provider. urls may be nil (no CTA URLs in the 402).
// enforceStorage turns the per-org storage cap on; pass false to meter-without-gating
// (the current default — see Config.EnforceStorageQuota).
func NewProvider(urls URLBuilder, enforceStorage bool) *Provider {
	return &Provider{upgrade: urls, enforceStorage: enforceStorage}
}

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
	case corequota.ResourceSitePerOrg:
		max, nx, unlimited := orgSiteCap(tier)
		if unlimited {
			return nil // Enterprise: unlimited sites.
		}
		capMax, next = max, nx
	case corequota.ResourceMemberPerOrg:
		// Seats are free: unlimited members on every plan. The
		// seam stays so seat policy could be re-tightened here without a store change.
		return nil
	case corequota.ResourceStorageBytesPerOrg:
		// Storage is metered but only GATED when explicitly enabled (storage billing
		// is not live yet). Off → never blocks a deploy; the band logic is preserved.
		if !p.enforceStorage {
			return nil
		}
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

// orgSiteCap returns the per-ORG site cap, the tier to upgrade to, and whether the
// tier is uncapped. The bands are seat-free: Free 10 → Pro 100 → Business
// UNLIMITED → Enterprise UNLIMITED. Business and Enterprise have no site cap, so
// they return unlimited=true and the caller never builds a site-cap 402.
func orgSiteCap(tier PlanTier) (max int64, next PlanTier, unlimited bool) {
	switch tier {
	case TierPro:
		return proSitesCap, TierBusiness, false
	case TierBusiness, TierEnterprise:
		return 0, "", true
	default: // free
		return freeSitesCap, TierPro, false
	}
}

// storageCap returns the per-org storage cap (bytes) and the next tier for `tier`.
func storageCap(tier PlanTier) (max int64, next PlanTier) {
	switch tier {
	case TierPro:
		return proStorageCap, TierBusiness
	case TierBusiness:
		return businessStorageCap, TierEnterprise
	case TierEnterprise:
		return enterpriseStorageCap, tierSales
	default: // free
		return freeStorageCap, TierPro
	}
}
