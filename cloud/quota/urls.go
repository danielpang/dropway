//go:build cloud

package quota

import "net/url"

// DashboardURLBuilder is the default URLBuilder: it deep-links the dashboard's
// billing routes. The base is the dashboard origin (e.g. https://app.shipped.app).
type DashboardURLBuilder struct {
	DashboardBaseURL string
}

// UpgradeURL points at the subscription modal pre-selected to `target`. The
// active org comes from the dashboard session, so no org id is embedded.
func (b DashboardURLBuilder) UpgradeURL(target PlanTier) string {
	q := url.Values{"tier": {string(target)}}
	return b.DashboardBaseURL + "/billing/upgrade?" + q.Encode()
}

// SalesURL points at the contact-sales CTA (no self-serve checkout above the
// Enterprise band).
func (b DashboardURLBuilder) SalesURL() string {
	return b.DashboardBaseURL + "/contact-sales"
}
