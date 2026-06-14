// Package quota defines the open-core seam between the FSL core and the
// proprietary cloud quota/billing module (docs/ARCHITECTURE.md §9, §14).
//
// The seam is a PURE POLICY FUNCTION with no database access:
//
//	Provider.Allow(planTier string, res Resource, current int64) error
//
// It answers a single question — "given this plan tier and the current count of
// `res`, may we create one more?" — and returns an *ExceededError (→ HTTP 402)
// when current+1 would cross the tier's hard cap. The DB mechanics that make the
// check race-safe (the per-(org,user) advisory lock, the COUNT, reading
// org_meta.plan_tier, and the INSERT) all live in the STORE, inside the
// resource-creation transaction (internal/store). Keeping the provider pure means
// the policy is trivially unit-testable, has no TOCTOU surface of its own, and
// the core never imports any cloud/ code.
//
// The CORE ships the Unlimited implementation (every action allowed → a
// self-hosted build has no caps). The hosted build wires in cloud/quota's real
// Provider (the Free 5-members/10-sites → Business → Enterprise → Contact-Sales
// hard caps), selected via the `cloud` build tag in services/api/cmd/api.
package quota

import (
	"errors"
	"fmt"
)

// Resource is the thing being created that may be capped.
type Resource string

const (
	ResourceSitePerUser  Resource = "sites_per_user"
	ResourceMemberPerOrg Resource = "members_per_org"
)

// ExceededError is returned by an enforcing Provider when an action would cross
// a hard cap. The Go API surfaces this as HTTP 402 with this body so the
// dashboard can open the subscription modal (or the contact-sales CTA).
type ExceededError struct {
	Limit      Resource `json:"limit"`
	Current    int64    `json:"current"`
	Max        int64    `json:"max"`
	PlanTier   string   `json:"plan_tier"`
	NextTier   string   `json:"next_tier,omitempty"`
	UpgradeURL string   `json:"upgrade_url,omitempty"`
	SalesURL   string   `json:"sales_url,omitempty"`
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("quota exceeded: %s (%d/%d) on plan %q", e.Limit, e.Current, e.Max, e.PlanTier)
}

// AsExceeded extracts an *ExceededError from an error chain, if present.
func AsExceeded(err error) (*ExceededError, bool) {
	var e *ExceededError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Provider is the pure quota policy. Given the org's live plan tier and the
// CURRENT count of `res` (how many already exist), Allow reports whether creating
// ONE MORE is permitted: it returns nil when current+1 is within the cap and an
// *ExceededError when current+1 would cross it.
//
// Allow MUST be free of side effects and IO: race-safety is the store's job (it
// holds a per-(org,subject) advisory lock across the COUNT → Allow → INSERT so two
// concurrent same-subject creates can't both slip past the cap). A pure policy
// also means the `current` passed in must be the live count read under that lock.
type Provider interface {
	Allow(planTier string, res Resource, current int64) error
}

// Unlimited is the core/self-host Provider: every action is allowed regardless of
// tier or count (OSS self-host has no caps). This is the default; cloud builds
// replace it via dependency injection (wire_cloud.go).
type Unlimited struct{}

// Allow always permits the action.
func (Unlimited) Allow(string, Resource, int64) error { return nil }

// Ensure Unlimited satisfies Provider.
var _ Provider = Unlimited{}
