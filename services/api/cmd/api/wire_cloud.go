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

// newQuotaProvider builds the cloud hard-cap provider. The Counts source here is
// the DB-free StaticCounts so the wiring compiles and runs without a database;
// the production deployment swaps in a pgx-backed Counts that reads
// app.org_usage + billing.subscriptions inside the request transaction.
func newQuotaProvider(cfg config.Config) quota.Provider {
	urls := cloudquota.DashboardURLBuilder{DashboardBaseURL: "https://app.shipped.app"}
	return cloudquota.NewProvider(cloudquota.StaticCounts{}, nil, urls)
}

// quotaProviderName labels the wired provider for startup logging.
func quotaProviderName() string { return "cloud hard-cap (free/business/enterprise)" }
