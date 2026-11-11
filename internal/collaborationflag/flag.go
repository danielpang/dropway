// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// Package collaborationflag owns the single server-authoritative collaboration rollout.
package collaborationflag

import (
	"context"

	"github.com/posthog/posthog-go"
)

const Key = "project-collaboration"

type Client interface {
	GetFeatureFlag(posthog.FeatureFlagPayload) (interface{}, error)
}
type Evaluator struct {
	Client      Client
	Environment string
}

// Allowed deliberately does not cache positive decisions: workers recheck the
// initiator on every capability use, so rollout withdrawal stops active work.
// The shared SDK bounds remote evaluation with its feature-request timeout.
func (e Evaluator) Allowed(ctx context.Context, userID, orgID string) bool {
	if e.Client == nil || userID == "" || orgID == "" || ctx.Err() != nil {
		return false
	}
	sendEvents := false
	value, err := e.Client.GetFeatureFlag(posthog.FeatureFlagPayload{
		Key: Key, DistinctId: userID,
		PersonProperties:      posthog.NewProperties().Set("org_id", orgID).Set("environment", e.Environment),
		SendFeatureFlagEvents: &sendEvents,
	})
	enabled, ok := value.(bool)
	return err == nil && ctx.Err() == nil && ok && enabled
}
