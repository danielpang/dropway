//go:build cloud

package quota

import "net/url"

// DashboardURLBuilder is the default URLBuilder: it deep-links the dashboard's
// billing routes. The base is the dashboard origin (e.g. https://app.shipped.app).
type DashboardURLBuilder struct {
	DashboardBaseURL string
}

// UpgradeURL points at the subscription modal pre-selected to `target`.
func (b DashboardURLBuilder) UpgradeURL(orgID string, target PlanTier) string {
	q := url.Values{"org": {orgID}, "tier": {string(target)}}
	return b.DashboardBaseURL + "/billing/upgrade?" + q.Encode()
}

// SalesURL points at the contact-sales CTA (no self-serve checkout above the
// Enterprise band).
func (b DashboardURLBuilder) SalesURL(orgID string) string {
	q := url.Values{"org": {orgID}}
	return b.DashboardBaseURL + "/contact-sales?" + q.Encode()
}
