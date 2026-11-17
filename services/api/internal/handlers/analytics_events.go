// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"

	"github.com/danielpang/dropway/internal/analytics"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// captureOrgEvent sends a product-analytics event attributed to the acting user
// and rolled up to their organization: org_id is added to props and the event
// joins the `organization` group, which the dashboards break down by. Capture is
// best-effort and non-blocking; a nil emitter (tests, no POSTHOG_KEY) is a no-op.
func (a *API) captureOrgEvent(r *http.Request, t store.Tenant, event string, props map[string]any) {
	if a.Analytics == nil {
		return
	}
	if props == nil {
		props = map[string]any{}
	}
	props["org_id"] = t.OrgID
	a.Analytics.Capture(r.Context(), analytics.Event{
		DistinctID: t.UserID,
		Event:      event,
		Properties: props,
		Groups:     map[string]string{"organization": t.OrgID},
	})
}
