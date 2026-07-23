//go:build cloud

// This file is compiled ONLY into the proprietary `cloud` build. It wires the
// real hard-cap quota provider (cloud/quota) AND the cloud billing surface
// (cloud/billing): the signature-verified Stripe webhook plus the authed
// /v1/billing/* routes. The OSS build never compiles this file, so the self-host
// binary never links any code under cloud/ and has no
// billing routes at all.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	cloudbilling "github.com/danielpang/dropway/cloud/billing"
	cloudquota "github.com/danielpang/dropway/cloud/quota"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/pgpool"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/config"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// runCloudBillingTask runs a one-off operator task selected by BILLING_TASK and
// exits, instead of starting the server. It lands AI pass-through billing in a
// Stripe account:
//
//	BILLING_TASK=bootstrap-ai  create the ai_cost_cents meter + $0.01 metered
//	                           price; prints the price id for STRIPE_AI_METER_PRICE.
//	BILLING_TASK=backfill-ai   attach STRIPE_AI_METER_PRICE to existing
//	                           subscriptions (idempotent; safe to re-run).
//
// Returns handled=false (server starts normally) when BILLING_TASK is unset.
func runCloudBillingTask(ctx context.Context, cfg config.Config) (bool, error) {
	task := os.Getenv("BILLING_TASK")
	if task == "" {
		return false, nil
	}
	if cfg.StripeSecretKey == "" {
		return true, errors.New("BILLING_TASK requires STRIPE_SECRET_KEY")
	}
	switch task {
	case "bootstrap-ai":
		res, err := cloudbilling.BootstrapAIMeter(cfg.StripeSecretKey)
		if err != nil {
			return true, err
		}
		slog.Info("AI meter bootstrap complete",
			"meter_id", res.MeterID, "meter_existed", res.MeterExisted,
			"price_id", res.PriceID)
		slog.Info("set STRIPE_AI_METER_PRICE to the price id above, then run BILLING_TASK=backfill-ai for existing subscribers")
		return true, nil
	case "backfill-ai":
		if cfg.DatabaseURL == "" {
			return true, errors.New("backfill-ai requires DATABASE_URL")
		}
		pool, err := pgpool.New(ctx, cfg.DatabaseURL, 4)
		if err != nil {
			return true, err
		}
		defer pool.Close()
		res, err := cloudbilling.BackfillAIMeteredItem(ctx, pool, cfg.StripeSecretKey, cfg.StripeAIMeterPrice)
		if err != nil {
			return true, err
		}
		slog.Info("AI metered-price backfill complete",
			"scanned", res.Scanned, "attached", res.Attached, "skipped", res.Skipped)
		return true, nil
	default:
		return true, fmt.Errorf("unknown BILLING_TASK %q (want bootstrap-ai or backfill-ai)", task)
	}
}

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

// storeRouteProjector adapts the core *store.Store + the edge projection.Writer to
// cloud/billing's store-free OrgRouteProjector (Go's internal-package rule forbids
// cloud/ from importing the internal store directly, same as storeRoleChecker). On a
// billing tier change the BillingStore calls this to re-project the org's route:<host>
// values so RouteValue.plan_tier updates at the serving Worker (the free-tier
// attribution banner clears on upgrade / reappears on downgrade) without a republish.
//
// It delegates to store.ReprojectOrgRoutes, which collects the org's live routes
// under the org's own RLS tenant context and upserts each host (PutRoute, continue-
// on-error) — NOT RebuildFromDB, which would wipe the entire cross-org projection.
type storeRouteProjector struct {
	s *store.Store
	w projection.Writer
}

func (p storeRouteProjector) ReprojectOrgRoutes(ctx context.Context, orgID string) error {
	return p.s.ReprojectOrgRoutes(ctx, p.w, orgID)
}

// cloudBuild reports the build flavor for startup logging.
const cloudBuild = true

// newQuotaProvider builds the cloud hard-cap provider. It is a PURE policy
// (Allow(planTier, res, current)); the live counts + per-(org,user) advisory lock
// that make the check race-safe live in the Store, inside the request tx
// (internal/store). The dashboard fills the active org from the session, so the
// CTA URLs need no org id.
func newQuotaProvider(cfg config.Config) quota.Provider {
	// Storage gating is off by default (ENFORCE_STORAGE_QUOTA): storage is metered but
	// only the per-org site count blocks a deploy today.
	return cloudquota.NewProvider(
		cloudquota.DashboardURLBuilder{DashboardBaseURL: cfg.DashboardURL},
		cfg.EnforceStorageQuota,
	)
}

// quotaProviderName labels the wired provider for startup logging.
func quotaProviderName() string { return "cloud hard-cap (free/pro/business/enterprise)" }

// mountCloud wires cloud/billing onto the shared chi mux. It builds:
//   - the Postgres BillingStore over the SAME non-BYPASSRLS dropway_app pool (the
//     per-event SET LOCAL app.current_org_id is the isolation);
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
// cloudAIGate adapts cloud/billing's AIMeter (which answers AllowAIForOrg by
// org id) to the handlers.AIGate interface (which takes a store.Tenant). Go's
// internal-package rule forbids cloud/ from naming store.Tenant, so this
// adapter lives here (inside services/api) exactly like storeRoleChecker.
type cloudAIGate struct{ meter *cloudbilling.AIMeter }

func (g cloudAIGate) AllowAI(ctx context.Context, t store.Tenant) (bool, string, error) {
	return g.meter.AllowAIForOrg(ctx, t.OrgID)
}

// AllowMemory makes the same adapter satisfy handlers.MemoryGate and
// ai.MemoryGate (org memory is Pro+ on the hosted build).
func (g cloudAIGate) AllowMemory(ctx context.Context, t store.Tenant) (bool, string, error) {
	return g.meter.AllowMemoryForOrg(ctx, t.OrgID)
}

func mountCloud(mux *chi.Mux, deps cloudDeps) {
	if deps.Pool == nil {
		slog.Warn("cloud billing NOT mounted: no DATABASE_URL (billing requires Postgres)")
		return
	}
	if deps.Cfg.StripeWebhookSecret == "" || deps.Cfg.StripeSecretKey == "" {
		slog.Warn("cloud billing NOT mounted: STRIPE_WEBHOOK_SECRET/STRIPE_SECRET_KEY unset")
		return
	}

	// AI pass-through billing: attach the Stripe meter to the AI runner (each
	// generation's cost + 3% fee is metered) and the paid-plan gate to the API
	// (the hosted build requires a paid plan with a card on file). Wired here,
	// BEFORE the local `store` var shadows the store package, so the PeriodStart
	// closure can name store.Tenant. No-op when the AI builder is disabled.
	if deps.API != nil {
		meter := cloudbilling.NewAIMeter(deps.Pool, deps.Cfg.StripeSecretKey)
		deps.API.AIGate = cloudAIGate{meter: meter}
		// Org memory is Pro+ on the hosted build: gate the /v1/ai/memories +
		// /v1/orgs/memory surface AND the runner's in-loop/async paths
		// (extraction, content indexing) with the same tier check.
		deps.API.MemoryGate = cloudAIGate{meter: meter}
		// Align the AI spend window (cap enforcement AND the dashboard usage
		// display) to the org's exact Stripe billing period, so the number the user
		// sees reconciles with the invoice period and can't disagree with a 402.
		// Falls back to the calendar month when the org has no subscription/period
		// yet. The SAME closure feeds the runner (cap) and the API (display).
		billingPeriodStart := func(ctx context.Context, t store.Tenant) time.Time {
			if start, ok, err := meter.BillingPeriodStart(ctx, t.OrgID); err == nil && ok {
				return start
			}
			now := time.Now().UTC()
			return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
		deps.API.AISpendPeriodStart = billingPeriodStart
		if deps.AIRunner != nil {
			deps.AIRunner.UsageReporter = meter
			deps.AIRunner.PeriodStart = billingPeriodStart
			deps.AIRunner.MemoryGate = cloudAIGate{meter: meter}
			slog.Info("cloud AI metering wired (pass-through OpenRouter cost + 3% fee, billing-period-aligned)")
		}
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

	// Plan upgrade/downgrade analytics (emitted post-commit from the webhook) over
	// the shared, vendor-neutral analytics emitter (internal/analytics, a PostHog
	// client by default). The emitter is nil when POSTHOG_KEY is unset, so a deploy
	// without it simply records nothing — NewPlanAnalytics(nil) → WithPlanAnalytics(nil)
	// is a no-op.
	if deps.Analytics != nil {
		store = store.WithPlanAnalytics(cloudbilling.NewPlanAnalytics(deps.Analytics))
		slog.Info("cloud billing: plan-change analytics wired")
	} else {
		slog.Info("cloud billing: analytics disabled (POSTHOG_KEY unset)")
	}

	// Edge route reprojection: on a tier change, re-project the org's route:<host>
	// values so RouteValue.plan_tier updates at the serving Worker — the free-tier
	// "Deployed with Dropway" attribution banner then clears on upgrade (or reappears
	// on downgrade) without the org republishing. Best-effort, post-commit, logged.
	if deps.Store != nil && deps.Projection != nil {
		store = store.WithOrgRouteProjector(storeRouteProjector{s: deps.Store, w: deps.Projection})
		slog.Info("cloud billing: edge route reprojection wired (plan change updates the attribution banner)")
	} else {
		slog.Warn("cloud billing: no route reprojector — the attribution banner will not update until the org republishes")
	}
	prices := cloudbilling.NewPriceMap(deps.Cfg.StripePricePro, deps.Cfg.StripePriceBusiness, deps.Cfg.StripePriceEnterprise)

	// Webhook: verify → ProcessEvent (dedupe + persist ATOMICALLY in one tx, FIX 1).
	// The BillingStore is the EventProcessor.
	verifier := cloudbilling.NewRealSignatureVerifier(deps.Cfg.StripeWebhookSecret, prices)
	webhook := cloudbilling.NewHandler(verifier, store, slog.Default())

	// Authed billing handlers (Checkout/Portal/Current).
	stripeClient := cloudbilling.NewStripeClient(deps.Cfg.StripeSecretKey)
	// Billing's owner/admin gate re-checks the LIVE identity.member role (not the JWT
	// claim) via the core Store, strict-by-default with ALLOW_JWT_ROLE_FALLBACK —
	// the same confused-deputy guard the rest of the API uses (FIX 2).
	bh := cloudbilling.NewHandlers(store, stripeClient, prices, deps.Cfg.DashboardURL,
		storeRoleChecker{deps.Store}, deps.Cfg.AllowJWTRoleFallback, slog.Default()).
		WithAIMeteredPrice(deps.Cfg.StripeAIMeterPrice)
	if deps.Cfg.StripeAIMeterPrice != "" {
		slog.Info("cloud billing: AI metered price attached to new checkouts", "price", deps.Cfg.StripeAIMeterPrice)
	}

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
