//go:build !cloud

// This file is compiled into the DEFAULT (open-source, self-host) build. It
// wires the no-op quota provider so the OSS binary has no caps and never imports
// any code under cloud/ (docs/ARCHITECTURE.md §14.3). CI asserts the core has
// zero references into cloud/.
package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/services/api/internal/config"
)

// cloudBuild reports whether this binary was built with the `cloud` tag. It's
// false here so logs/metrics can show the build flavor.
const cloudBuild = false

// newQuotaProvider returns the OSS provider: unlimited. The config is accepted
// for signature parity with the cloud variant but unused here.
func newQuotaProvider(_ config.Config) quota.Provider {
	return quota.Unlimited{}
}

// quotaProviderName is a human-readable label for startup logging.
func quotaProviderName() string { return "unlimited (oss)" }

// mountCloud is a NO-OP in the OSS/self-host build: there is no billing. So
// /webhooks/stripe and /v1/billing/* simply do not exist in the self-host surface
// (ARCHITECTURE.md §14). The cloud build replaces this (wire_cloud.go) to mount the
// signature-verified Stripe webhook + the authed billing routes. The deps argument
// is accepted for signature parity with the cloud variant and is unused here.
func mountCloud(_ *chi.Mux, _ cloudDeps) {}
