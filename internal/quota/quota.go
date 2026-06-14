// Package quota defines the open-core seam between the FSL core and the
// proprietary cloud quota/billing module (docs/ARCHITECTURE.md §9, §14).
//
// The CORE depends only on the Provider interface and ships the Unlimited
// implementation, so a self-hosted build has no caps and never imports cloud
// code. The hosted build wires in cloud/quota's real Provider (the
// 10-sites-per-user / 5-members-per-org → Business → Enterprise hard caps that
// return a 402 + open the Stripe subscription modal).
package quota

import (
	"context"
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

// Provider enforces (or doesn't) tenant resource caps. Implementations must be
// race-safe: the hosted impl serializes per-org via SELECT ... FOR UPDATE on the
// app.org_usage counter row inside the caller's transaction.
type Provider interface {
	// CheckAndReserve verifies that creating one more `res` for `orgID` is within
	// the org's plan cap and atomically reserves it. It returns an *ExceededError
	// when the cap would be crossed. `subjectID` is the user id for per-user caps.
	CheckAndReserve(ctx context.Context, orgID, subjectID string, res Resource) error
}

// Unlimited is the core/self-host Provider: every action is allowed. This is the
// default; cloud builds replace it via dependency injection.
type Unlimited struct{}

func (Unlimited) CheckAndReserve(context.Context, string, string, Resource) error { return nil }

// Ensure Unlimited satisfies Provider.
var _ Provider = Unlimited{}
