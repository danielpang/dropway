//go:build cloud

// This file is compiled ONLY into the proprietary `cloud` build. It wires the
// real hard-cap quota provider (cloud/quota) AND the cloud billing surface
// (cloud/billing): the signature-verified Stripe webhook plus the authed
// /v1/billing/* routes. The OSS build never compiles this file, so the self-host
// binary never links any code under cloud/ (docs/ARCHITECTURE.md §14.3) and has no
// billing routes at all.
package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/go-chi/chi/v5"

	cloudbilling "github.com/danielpang/dropway/cloud/billing"
	cloudquota "github.com/danielpang/dropway/cloud/quota"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/config"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// storeRoleChecker adapts the core *store.Store to cloud/billing's store-free
// RoleChecker (Go's internal-package rule forbids cloud/ from importing the
// internal store directly). It translates the store's sentinels: a missing
// member table → unavailable=true (caller applies the fallback policy); no
// membership → role="" (non-admin); a live role → that role.
type storeRoleChecker struct{ s *store.Store }

func (c storeRoleChecker) LiveRole(ctx context.Context, orgID, userID string) (string, bool, error) {
	role, err := c.s.MemberRole(ctx, orgID, userID)
	if err != nil {
		if errors.Is(err, store.ErrAuthSchemaUnavailable) {
			return "", true, nil
		}
		if errors.Is(err, store.ErrNoMembership) {
			return "", false, nil
		}
		return "", false, err
	}
	return role, false, nil
}

// cloudBuild reports the build flavor for startup logging.
const cloudBuild = true

// newQuotaProvider builds the cloud hard-cap provider. It is a PURE policy
// (Allow(planTier, res, current)); the live counts + per-(org,user) advisory lock
// that make the check race-safe live in the Store, inside the request tx
// (internal/store). The dashboard fills the active org from the session, so the
// CTA URLs need no org id.
func newQuotaProvider(cfg config.Config) quota.Provider {
	return cloudquota.NewProvider(cloudquota.DashboardURLBuilder{DashboardBaseURL: cfg.DashboardURL})
}

// quotaProviderName labels the wired provider for startup logging.
func quotaProviderName() string { return "cloud hard-cap (free/business/enterprise)" }

// mountCloud wires cloud/billing onto the shared chi mux. It builds:
//   - the Postgres BillingStore over the SAME non-BYPASSRLS dropway_app pool (the
//     per-event SET LOCAL app.current_org_id is the isolation, §9);
//   - the RealSignatureVerifier (Stripe-Signature → parsed Event, price→tier via
//     the configured price ids) + the webhook Handler;
//   - the real StripeClient (Checkout/Portal/Customer) + the billing Handlers.
//
// Routes mounted:
//   - POST /webhooks/stripe         JWT-FREE, signature-verified (the entitlement
//     writer; the browser redirect grants nothing).
//   - POST /v1/billing/checkout     authed (Auth + EnsureOrgProvisioned), owner/admin
//   - POST /v1/billing/portal       authed, owner/admin
//   - GET  /v1/billing              authed (current plan)
//
// Without a DB pool or the Stripe secrets, billing can't run; we log and skip so
// the rest of the API still serves (the webhook would 500 with no store; better to
// not register it).
func mountCloud(mux *chi.Mux, deps cloudDeps) {
	if deps.Pool == nil {
		slog.Warn("cloud billing NOT mounted: no DATABASE_URL (billing requires Postgres)")
		return
	}
	if deps.Cfg.StripeWebhookSecret == "" || deps.Cfg.StripeSecretKey == "" {
		slog.Warn("cloud billing NOT mounted: STRIPE_WEBHOOK_SECRET/STRIPE_SECRET_KEY unset")
		return
	}

	store := cloudbilling.NewStore(deps.Pool)
	// Attach the edge org-status projection writer (the SAME KV/local writer as the
	// route projection) so a billing org_status change (suspended / over_limit /
	// active) is pushed to org_status:<orgID> in KV AFTER the DB commit — making
	// suspension/over_limit actually BLOCK at the serving Worker (the DB column alone
	// never reaches the edge). Best-effort: a KV failure is logged, not fatal (the DB
	// is authoritative; the projection is rebuildable). FIX 2.
	if osw, ok := deps.Projection.(projection.OrgStatusWriter); ok && osw != nil {
		store = store.WithOrgStatusWriter(osw)
		slog.Info("cloud billing: edge org_status projection wired (suspension blocks at the edge)")
	} else {
		slog.Warn("cloud billing: no org_status projection writer — suspension will NOT block at the edge")
	}
	prices := cloudbilling.NewPriceMap(deps.Cfg.StripePriceBusiness, deps.Cfg.StripePriceEnterprise)

	// Webhook: verify → ProcessEvent (dedupe + persist ATOMICALLY in one tx, FIX 1).
	// The BillingStore is the EventProcessor.
	verifier := cloudbilling.NewRealSignatureVerifier(deps.Cfg.StripeWebhookSecret, prices)
	webhook := cloudbilling.NewHandler(verifier, store, slog.Default())

	// Authed billing handlers (Checkout/Portal/Current).
	stripeClient := cloudbilling.NewStripeClient(deps.Cfg.StripeSecretKey)
	// Billing's owner/admin gate re-checks the LIVE auth.member role (not the JWT
	// claim) via the core Store, strict-by-default with ALLOW_JWT_ROLE_FALLBACK —
	// the same confused-deputy guard the rest of the API uses (§5.4/§10, FIX 2).
	bh := cloudbilling.NewHandlers(store, stripeClient, prices, deps.Cfg.DashboardURL,
		storeRoleChecker{deps.Store}, deps.Cfg.AllowJWTRoleFallback, slog.Default())

	// JWT-FREE, signature-verified webhook. Mounted at the top level (NOT under
	// /v1) so no Auth middleware runs — Stripe carries no Better Auth JWT.
	mux.Post("/webhooks/stripe", webhook.ServeHTTP)

	// Authed billing group: same Auth boundary + EnsureOrgProvisioned as the rest
	// of /v1. Role (owner/admin) is enforced inside the Checkout/Portal handlers.
	mux.Group(func(r chi.Router) {
		r.Use(middleware.Auth(deps.Verifier))
		r.Use(deps.EnsureOrgProvisioned)

		r.Post("/v1/billing/checkout", bh.Checkout)
		r.Post("/v1/billing/portal", bh.Portal)
		r.Get("/v1/billing", bh.Current)
	})

	slog.Info("cloud billing mounted",
		"routes", []string{"POST /webhooks/stripe", "POST /v1/billing/checkout", "POST /v1/billing/portal", "GET /v1/billing"})
}
