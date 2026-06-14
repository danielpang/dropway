//go:build cloud

// This file is compiled ONLY into the proprietary `cloud` build. It wires the
// real hard-cap quota provider from cloud/quota (the Free 5u/10s → Business →
// Enterprise → Contact Sales bands). The OSS build never compiles this file, so
// the self-host binary never links any code under cloud/ (docs/ARCHITECTURE.md
// §14.3).
package main

import (
	cloudquota "github.com/danielpang/shipped/cloud/quota"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/services/api/internal/config"
)

// cloudBuild reports the build flavor for startup logging.
const cloudBuild = true

// newQuotaProvider builds the cloud hard-cap provider. It is a PURE policy
// (Allow(planTier, res, current)); the live counts + per-(org,user) advisory lock
// that make the check race-safe live in the Store, inside the request tx
// (internal/store). The dashboard fills the active org from the session, so the
// CTA URLs need no org id.
func newQuotaProvider(cfg config.Config) quota.Provider {
	return cloudquota.NewProvider(cloudquota.DashboardURLBuilder{DashboardBaseURL: "https://app.shipped.app"})
}

// quotaProviderName labels the wired provider for startup logging.
func quotaProviderName() string { return "cloud hard-cap (free/business/enterprise)" }
